package rawproxy

// Ham TCP/UDP replika dagitimi (replicas.go) testleri. Ajan oturumlari sahte
// (fakeClients): istemci basina cevrimici/cevrimdisi, akis acma hatasi, yerel
// hedefe baglanamama ve yanit vermeme (yari olu oturum) davranislari taklit edilir.

import (
	"context"
	"errors"
	"io"
	"net"
	"reflect"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/tkodcumpeg4/zorven/server/tunnel"
	"github.com/tkodcumpeg4/zorven/shared/protocol"
)

// --- sahte oturumlar ------------------------------------------------------------

// fakeClient, bir aday istemcinin (ajan oturumunun) sahte davranisi.
type fakeClient struct {
	offline bool
	openErr error  // akis acilamiyor (oturum yazamiyor)
	ackCode string // dolu: ajan yerel hedefe baglanamadi
	noAck   bool   // onay hic gelmez (yari olu oturum)
	echo    bool   // ziyaretci baytlarini aynen geri yollar
	streams []*fakeStream
}

// fakeClients, clientStreams'in sahte uygulamasi.
type fakeClients struct {
	mu       sync.Mutex
	clients  map[string]*fakeClient
	attempts []string // open cagrilari (istemci kimligi, sirayla)
	protos   []string // basarili acilan akislarin protokolu
	remotes  []string // basarili acilan akislarin uzak adresi
}

// newFakeClients, verilen istemcileri cevrimici ve echo yapar.
func newFakeClients(ids ...string) *fakeClients {
	f := &fakeClients{clients: map[string]*fakeClient{}}
	for _, id := range ids {
		f.clients[id] = &fakeClient{echo: true}
	}
	return f
}

func (f *fakeClients) set(id string, fn func(*fakeClient)) {
	f.mu.Lock()
	defer f.mu.Unlock()
	fn(f.clients[id])
}

func (f *fakeClients) online(id string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	c := f.clients[id]
	return c != nil && !c.offline
}

func (f *fakeClients) open(_ context.Context, _ TunnelInfo, id, proto, remote string) (bridgeStream, bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.attempts = append(f.attempts, id)
	c := f.clients[id]
	if c == nil || c.offline {
		return nil, true, nil
	}
	if c.openErr != nil {
		return nil, false, c.openErr
	}
	var s *fakeStream
	if c.noAck {
		s = &fakeStream{accept: make(chan protocol.StreamAck, 1), fromLocal: make(chan []byte, 64),
			closed: make(chan struct{}), echo: c.echo}
	} else {
		s = newFakeStream(c.ackCode, c.echo)
	}
	c.streams = append(c.streams, s)
	f.protos = append(f.protos, proto)
	f.remotes = append(f.remotes, remote)
	return s, false, nil
}

// attemptsOf, istemciye yapilan akis acma girisimi sayisi.
func (f *fakeClients) attemptsOf(id string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	n := 0
	for _, a := range f.attempts {
		if a == id {
			n++
		}
	}
	return n
}

func (f *fakeClients) totalAttempts() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.attempts)
}

// bytes, istemcinin akislarina iletilen toplam ziyaretci bayti.
func (f *fakeClients) bytes(id string) int64 {
	f.mu.Lock()
	defer f.mu.Unlock()
	var n int64
	for _, s := range f.clients[id].streams {
		n += s.got.Load()
	}
	return n
}

func (f *fakeClients) streamsOf(id string) []*fakeStream {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]*fakeStream(nil), f.clients[id].streams...)
}

// dropSession, istemcinin oturumunun dusmesini taklit eder: cevrimdisi olur ve
// acik tum akislari kapanir (tunnel.Session.closeAllRaw gibi).
func (f *fakeClients) dropSession(id string) {
	f.set(id, func(c *fakeClient) { c.offline = true })
	for _, s := range f.streamsOf(id) {
		s.Close("session lost")
	}
}

func isClosed(s *fakeStream) bool {
	select {
	case <-s.closed:
		return true
	default:
		return false
	}
}

// fakeBalancer, Balancer'in sahte uygulamasi: cagri sayar, acik-sayac tutar ve
// istenirse siralamayi kendisi belirler.
type fakeBalancer struct {
	mu        sync.Mutex
	pick      func(cands []string, start uint64, online func(string) bool) []string
	calls     int
	lastCands []string
	inflight  map[string]int
	maxSeen   map[string]int
	begins    map[string]int
}

func newFakeBalancer() *fakeBalancer {
	return &fakeBalancer{inflight: map[string]int{}, maxSeen: map[string]int{}, begins: map[string]int{}}
}

func (b *fakeBalancer) OrderClients(_ string, cands []string, start uint64, online func(string) bool) []string {
	b.mu.Lock()
	b.calls++
	b.lastCands = append([]string(nil), cands...)
	fn := b.pick
	b.mu.Unlock()
	if fn != nil {
		return fn(cands, start, online)
	}
	return rotateOnline(cands, start, online)
}

func (b *fakeBalancer) ClientBegin(id string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.inflight[id]++
	b.begins[id]++
	if b.inflight[id] > b.maxSeen[id] {
		b.maxSeen[id] = b.inflight[id]
	}
}

func (b *fakeBalancer) ClientEnd(id string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.inflight[id]--
}

func (b *fakeBalancer) open(id string) int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.inflight[id]
}

func (b *fakeBalancer) callCount() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.calls
}

// --- TCP yardimcilari -------------------------------------------------------------

// replicaTCPMgr, sahte oturumlarla replikali bir TCP tuneli dinleyen yonetici doner.
// setup fonksiyonlari Start'TAN ONCE calisir (bkz. rawTestMgr).
func replicaTCPMgr(t *testing.T, primary string, replicas []string, fc *fakeClients, setup ...func(*Manager)) (*Manager, string) {
	t.Helper()
	p := freePort(t)
	ti := TunnelInfo{TunnelID: "tun", TenantID: "ten", ClientID: primary, Replicas: replicas, Proto: "tcp", Port: p, BindAddr: "127.0.0.1"}
	m := New(tunnel.NewHub(), nil, quietLog(), func(context.Context) ([]TunnelInfo, error) { return []TunnelInfo{ti}, nil })
	m.streams = fc
	for _, f := range setup {
		f(m)
	}
	m.Start(context.Background())
	t.Cleanup(m.closeAll)
	return m, net.JoinHostPort("127.0.0.1", strconv.Itoa(p))
}

// echoOnce, bir baglanti acar, msg yollar ve ayni baytlarin geri geldigini dogrular.
func echoOnce(t *testing.T, addr, msg string) {
	t.Helper()
	c, err := net.Dial("tcp", addr)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	_ = c.SetDeadline(time.Now().Add(5 * time.Second))
	if _, err := c.Write([]byte(msg)); err != nil {
		t.Fatal(err)
	}
	buf := make([]byte, len(msg))
	if _, err := io.ReadFull(c, buf); err != nil || string(buf) != msg {
		t.Fatalf("yanki alinamadi: %q err=%v", buf, err)
	}
}

// expectClosed, sunucunun baglantiyi (reddederek) kapattigini dogrular.
func expectClosed(t *testing.T, c net.Conn) {
	t.Helper()
	_ = c.SetReadDeadline(time.Now().Add(5 * time.Second))
	if _, err := c.Read(make([]byte, 1)); err != io.EOF {
		t.Fatalf("baglanti kapatilmaliydi: %v", err)
	}
}

// --- aday kumesi / siralama birimleri ------------------------------------------------

func TestTunnelInfoCandidates(t *testing.T) {
	got := TunnelInfo{ClientID: "a", Replicas: []string{"b", "a", "", "c", "b"}}.candidates()
	if want := []string{"a", "b", "c"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("candidates = %v; istenen %v", got, want)
	}
	if got := (TunnelInfo{ClientID: "a"}).candidates(); !reflect.DeepEqual(got, []string{"a"}) {
		t.Fatalf("replikasiz = %v", got)
	}
}

func TestTunnelInfoNormalized(t *testing.T) {
	// Birincilin kendisi, bos ve tekrar eden kimlikler ayiklanir.
	got := TunnelInfo{ClientID: "a", Replicas: []string{"a", "b", "", "b", "c"}}.normalized()
	if want := []string{"b", "c"}; !reflect.DeepEqual(got.Replicas, want) {
		t.Fatalf("Replicas = %v; istenen %v", got.Replicas, want)
	}
	// Geriye replika kalmazsa nil: fiilen tek istemcili tunel hizli yolda kalir.
	if got := (TunnelInfo{ClientID: "a", Replicas: []string{"a", ""}}).normalized(); got.Replicas != nil {
		t.Fatalf("replika kalmadi, nil beklenirdi: %v", got.Replicas)
	}
	if got := (TunnelInfo{ClientID: "a"}).normalized(); got.Replicas != nil {
		t.Fatalf("replikasiz tunel degismemeli: %v", got.Replicas)
	}
	// Reload normalize eder: dinleyicideki bilgi temizlenmis olmalidir.
	p := freePort(t)
	ti := TunnelInfo{TunnelID: "tun", ClientID: "a", Replicas: []string{"a", "b"}, Proto: "tcp", Port: p, BindAddr: "127.0.0.1"}
	m := New(tunnel.NewHub(), nil, quietLog(), func(context.Context) ([]TunnelInfo, error) { return []TunnelInfo{ti}, nil })
	m.Start(context.Background())
	defer m.closeAll()
	m.mu.Lock()
	cur := m.byPort[ti.key()]
	m.mu.Unlock()
	if !reflect.DeepEqual(cur.Replicas, []string{"b"}) {
		t.Fatalf("Reload replikalari normalize etmeli: %v", cur.Replicas)
	}
}

func TestSameCandidates(t *testing.T) {
	a := TunnelInfo{ClientID: "a", Replicas: []string{"b", "c"}}
	if !sameCandidates(a, TunnelInfo{ClientID: "a", Replicas: []string{"b", "c"}}) {
		t.Fatal("ayni kume ayni sayilmali")
	}
	for _, other := range []TunnelInfo{
		{ClientID: "x", Replicas: []string{"b", "c"}},
		{ClientID: "a", Replicas: []string{"b"}},
		{ClientID: "a", Replicas: []string{"c", "b"}},
		{ClientID: "a"},
	} {
		if sameCandidates(a, other) {
			t.Fatalf("farkli kume ayni sayildi: %+v", other)
		}
	}
}

func TestRotateOnline(t *testing.T) {
	cands := []string{"a", "b", "c"}
	all := func(string) bool { return true }
	for start, want := range map[uint64][]string{0: {"a", "b", "c"}, 1: {"b", "c", "a"}, 2: {"c", "a", "b"}, 3: {"a", "b", "c"}} {
		if got := rotateOnline(cands, start, all); !reflect.DeepEqual(got, want) {
			t.Errorf("start=%d -> %v; istenen %v", start, got, want)
		}
	}
	// Cevrimdisi aday atlanir; hicbiri cevrimici degilse nil.
	if got := rotateOnline(cands, 0, func(c string) bool { return c != "a" }); !reflect.DeepEqual(got, []string{"b", "c"}) {
		t.Errorf("failover sirasi = %v", got)
	}
	if got := rotateOnline(cands, 0, func(string) bool { return false }); got != nil {
		t.Errorf("hic cevrimici yokken = %v; istenen nil", got)
	}
	if got := rotateOnline(nil, 0, all); got != nil {
		t.Errorf("aday yokken = %v", got)
	}
}

func TestNextRRPerTunnel(t *testing.T) {
	m := &Manager{}
	for i := uint64(0); i < 4; i++ {
		if got := m.nextRR("t1"); got != i {
			t.Fatalf("nextRR(t1) cagri %d = %d", i, got)
		}
	}
	if got := m.nextRR("t2"); got != 0 {
		t.Fatalf("baska tunel bagimsiz sayac: %d", got)
	}
}

func TestAwaitAck(t *testing.T) {
	ctx := context.Background()
	if res, _ := awaitAck(ctx, newFakeStream("", false), time.Second); res != ackOK {
		t.Fatalf("onay: %v", res)
	}
	if res, ack := awaitAck(ctx, newFakeStream("dial_failed", false), time.Second); res != ackRejected || ack.Code != "dial_failed" {
		t.Fatalf("ret: %v %+v", res, ack)
	}
	closed := newFakeStream("", false)
	<-closed.accept
	closed.Close("")
	if res, _ := awaitAck(ctx, closed, time.Second); res != ackRejected {
		t.Fatalf("kapanan akis: %v", res)
	}
	// Onay ve kapanis birlikte hazirsa onay kazanir (ajan baglandi, hedef hemen kapatti).
	for i := 0; i < 50; i++ {
		both := newFakeStream("", false)
		both.Close("")
		if res, _ := awaitAck(ctx, both, time.Second); res != ackOK {
			t.Fatalf("onay+kapanis birlikte: %v (deneme %d)", res, i)
		}
	}
	silent := &fakeStream{accept: make(chan protocol.StreamAck, 1), closed: make(chan struct{})}
	start := time.Now()
	if res, _ := awaitAck(ctx, silent, 50*time.Millisecond); res != ackTimeout || time.Since(start) > 2*time.Second {
		t.Fatalf("zaman asimi: %v", res)
	}
	cctx, cancel := context.WithCancel(ctx)
	cancel()
	if res, _ := awaitAck(cctx, silent, time.Second); res != ackCanceled {
		t.Fatalf("iptal: %v", res)
	}
}

func TestHubStreamsOfflineAndNil(t *testing.T) {
	hub := tunnel.NewHub()
	cs := hubStreams{hub: hub}
	if cs.online("yok") {
		t.Fatal("kayitsiz istemci cevrimici olmamali")
	}
	if st, off, err := cs.open(context.Background(), TunnelInfo{}, "yok", "tcp", "1.2.3.4:5"); st != nil || !off || err != nil {
		t.Fatalf("cevrimdisi open: %v %v %v", st, off, err)
	}
	var nilHub hubStreams
	if nilHub.online("x") {
		t.Fatal("hub yokken cevrimici olmamali")
	}
	if _, off, _ := nilHub.open(context.Background(), TunnelInfo{}, "x", "tcp", ""); !off {
		t.Fatal("hub yokken cevrimdisi sayilmali")
	}
}

// --- TCP: dagitim ---------------------------------------------------------------------

func TestReplicaTCPRoundRobinAcrossClients(t *testing.T) {
	fc := newFakeClients("a", "b", "c")
	m, addr := replicaTCPMgr(t, "a", []string{"b", "c"}, fc)
	for i := 0; i < 9; i++ {
		echoOnce(t, addr, "x")
	}
	for _, id := range []string{"a", "b", "c"} {
		if n := fc.bytes(id); n != 3 {
			t.Errorf("%s 3 baglanti almaliydi (3 bayt), %d bayt aldi", id, n)
		}
	}
	live, _ := m.TCPStats("tun")
	if live.ConnsTotal != 9 || live.Rejected != 0 {
		t.Fatalf("istatistik: %+v", live)
	}
	for _, p := range fc.protos {
		if p != "tcp" {
			t.Fatalf("akis protokolu %q", p)
		}
	}
}

func TestReplicaTCPOfflineClientSkipped(t *testing.T) {
	fc := newFakeClients("a", "b")
	fc.set("a", func(c *fakeClient) { c.offline = true })
	_, addr := replicaTCPMgr(t, "a", []string{"b"}, fc)
	for i := 0; i < 4; i++ {
		echoOnce(t, addr, "x")
	}
	if fc.attemptsOf("a") != 0 {
		t.Fatalf("cevrimdisi istemciye akis acilmamali: %d", fc.attemptsOf("a"))
	}
	if fc.bytes("b") != 4 {
		t.Fatalf("hepsi b'ye gitmeliydi: %d", fc.bytes("b"))
	}
}

func TestReplicaTCPAllOfflineRejected(t *testing.T) {
	fc := newFakeClients("a", "b")
	fc.set("a", func(c *fakeClient) { c.offline = true })
	fc.set("b", func(c *fakeClient) { c.offline = true })
	m, addr := replicaTCPMgr(t, "a", []string{"b"}, fc)
	dialAndWait(t, addr)
	waitFor(t, "offline reddi", func() bool { l, _ := m.TCPStats("tun"); return l.RejectedBy[TCPRejectOffline] == 1 })
	if live, _ := m.TCPStats("tun"); live.Rejected != 1 || live.ConnsTotal != 0 {
		t.Fatalf("istatistik: %+v", live)
	}
	if fc.totalAttempts() != 0 {
		t.Fatal("cevrimici aday yokken akis acilmaya calisilmamali")
	}
}

// --- TCP: failover --------------------------------------------------------------------

func TestReplicaTCPFailoverOnOpenError(t *testing.T) {
	fc := newFakeClients("a", "b")
	fc.set("a", func(c *fakeClient) { c.openErr = errors.New("oturum yazamiyor") })
	m, addr := replicaTCPMgr(t, "a", []string{"b"}, fc)
	for i := 0; i < 4; i++ {
		echoOnce(t, addr, "x")
	}
	if fc.bytes("b") != 4 || fc.bytes("a") != 0 {
		t.Fatalf("hepsi b'ye gitmeliydi: a=%d b=%d", fc.bytes("a"), fc.bytes("b"))
	}
	// Siralama a,b / b,a / a,b / b,a: a iki kez denendi ve her seferinde siradakine gecildi.
	if n := fc.attemptsOf("a"); n != 2 {
		t.Fatalf("a 2 kez denenmeliydi: %d", n)
	}
	live, _ := m.TCPStats("tun")
	if live.Rejected != 0 || live.ConnsTotal != 4 {
		t.Fatalf("kurtarilan denemeler reddedilen sayilmamali: %+v", live)
	}
}

func TestReplicaTCPFailoverOnAckRejected(t *testing.T) {
	fc := newFakeClients("a", "b")
	fc.set("a", func(c *fakeClient) { c.ackCode = "local_unreachable" })
	m, addr := replicaTCPMgr(t, "a", []string{"b"}, fc)
	echoOnce(t, addr, "hello") // ilk sira a: yerel hedefe baglanamaz -> b
	if fc.bytes("a") != 0 || fc.bytes("b") != 5 {
		t.Fatalf("baytlar b'ye gitmeliydi: a=%d b=%d", fc.bytes("a"), fc.bytes("b"))
	}
	as := fc.streamsOf("a")
	if len(as) != 1 || !isClosed(as[0]) {
		t.Fatalf("basarisiz denemenin akisi kapatilmali: %d akis", len(as))
	}
	if live, _ := m.TCPStats("tun"); live.Rejected != 0 || live.ConnsTotal != 1 {
		t.Fatalf("istatistik: %+v", live)
	}
}

func TestReplicaTCPFailoverOnAckTimeout(t *testing.T) {
	old := replicaAckTimeout
	replicaAckTimeout = 150 * time.Millisecond
	t.Cleanup(func() { replicaAckTimeout = old })

	fc := newFakeClients("a", "b")
	fc.set("a", func(c *fakeClient) { c.noAck = true }) // yari olu oturum: onay vermiyor
	_, addr := replicaTCPMgr(t, "a", []string{"b"}, fc)
	start := time.Now()
	echoOnce(t, addr, "x") // ilk sira a: zaman asimi -> b
	if d := time.Since(start); d > 3*time.Second {
		t.Fatalf("failover cok gec: %v", d)
	}
	if fc.bytes("b") != 1 || fc.bytes("a") != 0 {
		t.Fatalf("baytlar b'ye gitmeliydi: a=%d b=%d", fc.bytes("a"), fc.bytes("b"))
	}
	if as := fc.streamsOf("a"); len(as) != 1 || !isClosed(as[0]) {
		t.Fatal("zaman asimina ugrayan akis kapatilmali")
	}
}

func TestReplicaTCPAllFailRejectedAsDial(t *testing.T) {
	fc := newFakeClients("a", "b")
	fc.set("a", func(c *fakeClient) { c.ackCode = "local_unreachable" })
	fc.set("b", func(c *fakeClient) { c.openErr = errors.New("x") })
	m, addr := replicaTCPMgr(t, "a", []string{"b"}, fc)
	dialAndWait(t, addr)
	waitFor(t, "dial reddi", func() bool { l, _ := m.TCPStats("tun"); return l.RejectedBy[TCPRejectDial] == 1 })
	if live, _ := m.TCPStats("tun"); live.Rejected != 1 || live.ConnsTotal != 0 || live.Active != 0 {
		t.Fatalf("istatistik: %+v", live)
	}
}

// Birincil akis acarken cevrimdisi gorunurse (siralama ile open arasinda dustu)
// siradakine gecilir; hepsi boyleyse offline sayilir.
func TestReplicaTCPOfflineRaceFallsThrough(t *testing.T) {
	fc := newFakeClients("a", "b")
	bal := newFakeBalancer()
	bal.pick = func(cands []string, _ uint64, _ func(string) bool) []string { return []string{"a", "b"} } // a'yi cevrimici sanir
	fc.set("a", func(c *fakeClient) { c.offline = true })
	m, addr := replicaTCPMgr(t, "a", []string{"b"}, fc, func(m *Manager) { m.Balancer = bal })
	echoOnce(t, addr, "x")
	if fc.bytes("b") != 1 {
		t.Fatalf("b servis etmeliydi: %d", fc.bytes("b"))
	}
	fc.set("b", func(c *fakeClient) { c.offline = true })
	dialAndWait(t, addr)
	waitFor(t, "offline reddi", func() bool { l, _ := m.TCPStats("tun"); return l.RejectedBy[TCPRejectOffline] == 1 })
}

// --- TCP: Balancer / sayaclar ------------------------------------------------------------

func TestReplicaTCPUsesBalancerOrderAndCounts(t *testing.T) {
	fc := newFakeClients("a", "b")
	bal := newFakeBalancer()
	bal.pick = func(cands []string, _ uint64, online func(string) bool) []string { return []string{"b", "a"} }
	_, addr := replicaTCPMgr(t, "a", []string{"b"}, fc, func(m *Manager) { m.Balancer = bal })

	// Iki baglanti acik tutulur: sayac secilen istemcide artar, kapaninca azalir.
	var conns []net.Conn
	for i := 0; i < 2; i++ {
		c, err := net.Dial("tcp", addr)
		if err != nil {
			t.Fatal(err)
		}
		conns = append(conns, c)
		_, _ = c.Write([]byte("x"))
		_ = c.SetReadDeadline(time.Now().Add(5 * time.Second))
		if _, err := io.ReadFull(c, make([]byte, 1)); err != nil {
			t.Fatal(err)
		}
	}
	if bal.open("b") != 2 || bal.open("a") != 0 {
		t.Fatalf("acik sayac b=%d a=%d; b=2 a=0 beklenirdi", bal.open("b"), bal.open("a"))
	}
	if !reflect.DeepEqual(bal.lastCands, []string{"a", "b"}) {
		t.Fatalf("balancer'a aday kumesi (birincil basta) verilmeli: %v", bal.lastCands)
	}
	for _, c := range conns {
		c.Close()
	}
	waitFor(t, "sayac sifirlanir", func() bool { return bal.open("b") == 0 })
}

func TestReplicaTCPFailedAttemptReleasesCounter(t *testing.T) {
	fc := newFakeClients("a", "b")
	fc.set("a", func(c *fakeClient) { c.ackCode = "local_unreachable" })
	bal := newFakeBalancer()
	bal.pick = func(cands []string, _ uint64, _ func(string) bool) []string { return []string{"a", "b"} }
	_, addr := replicaTCPMgr(t, "a", []string{"b"}, fc, func(m *Manager) { m.Balancer = bal })
	echoOnce(t, addr, "x")
	waitFor(t, "sayaclar sifirlanir", func() bool { return bal.open("a") == 0 && bal.open("b") == 0 })
	bal.mu.Lock()
	defer bal.mu.Unlock()
	if bal.begins["a"] != 1 || bal.begins["b"] != 1 {
		t.Fatalf("her deneme sayilmali: %v", bal.begins)
	}
	if bal.maxSeen["a"] != 1 {
		t.Fatalf("basarisiz deneme sirasinda a=1 gorulmeliydi: %v", bal.maxSeen)
	}
}

// --- TCP: sinirlar aynen korunur ---------------------------------------------------------

func TestReplicaTCPPerTunnelLimitNotMultipliedByReplicas(t *testing.T) {
	fc := newFakeClients("a", "b", "c")
	m, addr := replicaTCPMgr(t, "a", []string{"b", "c"}, fc, func(m *Manager) {
		m.limits = testLimits(2, 0, 0) // tunel basina 2
	})
	var keep []net.Conn
	for i := 0; i < 2; i++ {
		c, err := net.Dial("tcp", addr)
		if err != nil {
			t.Fatal(err)
		}
		keep = append(keep, c)
	}
	waitFor(t, "2 aktif", func() bool { l, _ := m.TCPStats("tun"); return l.Active == 2 })
	c3, err := net.Dial("tcp", addr)
	if err != nil {
		t.Fatal(err)
	}
	expectClosed(t, c3)
	if got := fc.totalAttempts(); got != 2 {
		t.Fatalf("sinir asilinca replika secimine/akis acmaya gecilmemeli: %d deneme", got)
	}
	for _, c := range keep {
		c.Close()
	}
	// Yer acilinca yeni baglanti kabul edilir.
	waitFor(t, "0 aktif", func() bool { l, _ := m.TCPStats("tun"); return l.Active == 0 })
	echoOnce(t, addr, "x")
}

func TestReplicaTCPPerIPLimit(t *testing.T) {
	fc := newFakeClients("a", "b")
	m, addr := replicaTCPMgr(t, "a", []string{"b"}, fc, func(m *Manager) {
		m.limits = testLimits(0, 2, 0) // tunel x kaynak IP basina 2
	})
	var keep []net.Conn
	for i := 0; i < 2; i++ {
		c, err := net.Dial("tcp", addr)
		if err != nil {
			t.Fatal(err)
		}
		keep = append(keep, c)
	}
	waitFor(t, "2 aktif", func() bool { l, _ := m.TCPStats("tun"); return l.Active == 2 })
	c3, err := net.Dial("tcp", addr)
	if err != nil {
		t.Fatal(err)
	}
	expectClosed(t, c3)
	for _, c := range keep {
		c.Close()
	}
}

func TestReplicaTCPThrottleStillApplies(t *testing.T) {
	// 20 KB/s, burst 20 KB: 40 KB aktarim en az ~1 sn surmeli (replikasiz yolla ayni limiter).
	fu := newFakeUsage(limitedState(20 * 1024))
	fc := newFakeClients("a", "b")
	fc.set("a", func(c *fakeClient) { c.echo = false })
	fc.set("b", func(c *fakeClient) { c.echo = false })
	m, addr := replicaTCPMgr(t, "a", []string{"b"}, fc, func(m *Manager) { m.Usage = fu })
	c, err := net.Dial("tcp", addr)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	start := time.Now()
	if _, err := c.Write(make([]byte, 40*1024)); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "aktarim", func() bool { l, _ := m.TCPStats("tun"); return l.BytesIn >= 40*1024 })
	if el := time.Since(start); el < 800*time.Millisecond {
		t.Fatalf("kiraci hiz siniri replika yolunda uygulanmadi: %v", el)
	}
}

// SNI modu (HandleSNIConn) ayni bridgeTCP'yi kullanir: replikalar orada da kullanilir.
func TestReplicaSNIConnUsesReplicas(t *testing.T) {
	fc := newFakeClients("a", "b")
	fc.set("a", func(c *fakeClient) { c.ackCode = "local_unreachable" })
	m := New(tunnel.NewHub(), nil, quietLog(), nil)
	m.streams = fc
	ti := TunnelInfo{TunnelID: "tun", TenantID: "ten", ClientID: "a", Replicas: []string{"b"}, Proto: "tcp"}

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Skip(err)
	}
	defer ln.Close()
	done := make(chan struct{})
	go func() {
		defer close(done)
		srv, err := ln.Accept()
		if err != nil {
			return
		}
		m.HandleSNIConn(srv, ti)
	}()
	c, err := net.Dial("tcp", ln.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	_ = c.SetDeadline(time.Now().Add(5 * time.Second))
	_, _ = c.Write([]byte("sni"))
	buf := make([]byte, 3)
	if _, err := io.ReadFull(c, buf); err != nil || string(buf) != "sni" {
		t.Fatalf("yanki: %q %v", buf, err)
	}
	c.Close()
	<-done
	if fc.bytes("b") != 3 || fc.attemptsOf("a") != 1 {
		t.Fatalf("a denenip b'ye gecilmeliydi: a=%d deneme, b=%d bayt", fc.attemptsOf("a"), fc.bytes("b"))
	}
}

// Replikasiz tunelde replika/Balancer mekanizmasina HIC girilmez (eski yol, ek is yok).
func TestSingleClientTCPIgnoresReplicaMachinery(t *testing.T) {
	fc := newFakeClients("cli")
	bal := newFakeBalancer()
	m, addr := rawTestMgr(t, func() (bridgeStream, bool, error) { return newFakeStream("", true), false, nil }, func(m *Manager) {
		m.streams = fc
		m.Balancer = bal
	})
	echoOnce(t, addr, "x")
	echoOnce(t, addr, "y")
	if bal.callCount() != 0 || fc.totalAttempts() != 0 {
		t.Fatalf("replikasiz tunel balancer/oturum katmanina girmemeli: calls=%d attempts=%d", bal.callCount(), fc.totalAttempts())
	}
	bal.mu.Lock()
	n := len(bal.begins)
	bal.mu.Unlock()
	if n != 0 {
		t.Fatal("replikasiz tunel acik-baglanti sayacini kullanmamali")
	}
	if live, _ := m.TCPStats("tun"); live.ConnsTotal != 2 {
		t.Fatalf("istatistik: %+v", live)
	}
}

// --- UDP ---------------------------------------------------------------------------------

type udpRepHarness struct {
	t  *testing.T
	m  *Manager
	ul *udpListener
	fc *fakeClients
}

// setup fonksiyonlari dinleyici (okuma dongusu) baslamadan ONCE calisir:
// yonetici alanlarini sonradan yazmak veri yarisidir (go test -race).
func newUDPRep(t *testing.T, ti TunnelInfo, fc *fakeClients, bal Balancer, setup ...func(*Manager)) *udpRepHarness {
	t.Helper()
	m := New(tunnel.NewHub(), nil, quietLog(), nil)
	if fc != nil { // nil *fakeClients'i arayuze koymak "nil olmayan" arayuz yapar
		m.streams = fc
	}
	m.Balancer = bal
	for _, f := range setup {
		f(m)
	}
	ti.Proto = "udp"
	if ti.TunnelID == "" {
		ti.TunnelID = "u"
	}
	ti.TenantID = "ten"
	ul, err := m.newUDPListener("127.0.0.1", 0, ti)
	if err != nil {
		t.Skip("udp dinlenemedi:", err)
	}
	t.Cleanup(ul.close)
	return &udpRepHarness{t: t, m: m, ul: ul, fc: fc}
}

// source, dinleyiciye bagli yeni bir ziyaretci soketi (yeni kaynak adres:port) acar.
func (h *udpRepHarness) source() *net.UDPConn {
	h.t.Helper()
	c, err := net.DialUDP("udp", nil, h.ul.conn.LocalAddr().(*net.UDPAddr))
	if err != nil {
		h.t.Fatal(err)
	}
	h.t.Cleanup(func() { c.Close() })
	return c
}

func (h *udpRepHarness) flowCount() int {
	h.ul.mu.Lock()
	defer h.ul.mu.Unlock()
	return len(h.ul.flows)
}

// flowClient, kaynak soketin flow'unun yapistigi istemci ("" = flow yok).
func (h *udpRepHarness) flowClient(c *net.UDPConn) string {
	h.ul.mu.Lock()
	defer h.ul.mu.Unlock()
	if f := h.ul.flows[c.LocalAddr().String()]; f != nil {
		return f.clientID
	}
	return ""
}

func send(t *testing.T, c *net.UDPConn, n int) {
	t.Helper()
	if _, err := c.Write(make([]byte, n)); err != nil {
		t.Fatal(err)
	}
}

func fcNoEcho(ids ...string) *fakeClients {
	fc := newFakeClients(ids...)
	for _, id := range ids {
		fc.set(id, func(c *fakeClient) { c.echo = false })
	}
	return fc
}

func TestReplicaUDPFlowIsSticky(t *testing.T) {
	fc := fcNoEcho("a", "b")
	h := newUDPRep(t, TunnelInfo{ClientID: "a", Replicas: []string{"b"}}, fc, nil)
	src := h.source()
	for i := 0; i < 10; i++ {
		send(t, src, 10)
	}
	waitFor(t, "paketler iletildi", func() bool { return fc.bytes("a")+fc.bytes("b") == 100 })
	if fc.totalAttempts() != 1 || h.flowCount() != 1 {
		t.Fatalf("tek flow, tek oturum beklenirdi: acilan=%d flow=%d", fc.totalAttempts(), h.flowCount())
	}
	if owner := h.flowClient(src); owner == "" || fc.bytes(owner) != 100 {
		t.Fatalf("tum paketler ayni istemciye gitmeliydi: sahip=%q a=%d b=%d", owner, fc.bytes("a"), fc.bytes("b"))
	}
	if fc.protos[0] != "udp" || fc.remotes[0] != src.LocalAddr().String() {
		t.Fatalf("akis udp ve kaynak adresiyle acilmali: %v %v", fc.protos, fc.remotes)
	}
}

func TestReplicaUDPDistributesNewFlows(t *testing.T) {
	fc := fcNoEcho("a", "b", "c")
	h := newUDPRep(t, TunnelInfo{ClientID: "a", Replicas: []string{"b", "c"}}, fc, nil)
	var srcs []*net.UDPConn
	for i := 0; i < 6; i++ {
		s := h.source()
		srcs = append(srcs, s)
		send(t, s, 10)
		i := i
		waitFor(t, "flow olustu", func() bool { return h.flowCount() == i+1 })
	}
	count := map[string]int{}
	for _, s := range srcs {
		count[h.flowClient(s)]++
	}
	if count["a"] != 2 || count["b"] != 2 || count["c"] != 2 {
		t.Fatalf("yeni flow'lar esit dagilmali: %v", count)
	}
	// Yapiskanlik: ikinci tur paketler ayni istemcilere gider, yeni oturum acilmaz.
	before := fc.totalAttempts()
	for _, s := range srcs {
		send(t, s, 10)
	}
	waitFor(t, "ikinci tur iletildi", func() bool { return fc.bytes("a")+fc.bytes("b")+fc.bytes("c") == 120 })
	if fc.totalAttempts() != before {
		t.Fatalf("var olan flow yeni oturum acmamali: %d -> %d", before, fc.totalAttempts())
	}
	for _, id := range []string{"a", "b", "c"} {
		if fc.bytes(id) != 40 {
			t.Errorf("%s 40 bayt almaliydi: %d", id, fc.bytes(id))
		}
	}
}

func TestReplicaUDPRemapsAfterSessionLoss(t *testing.T) {
	fc := fcNoEcho("a", "b")
	h := newUDPRep(t, TunnelInfo{ClientID: "a", Replicas: []string{"b"}}, fc, nil)
	s1, s2 := h.source(), h.source()
	send(t, s1, 10)
	waitFor(t, "ilk flow", func() bool { return h.flowCount() == 1 })
	send(t, s2, 10)
	waitFor(t, "ikinci flow", func() bool { return h.flowCount() == 2 })
	if h.flowClient(s1) != "a" || h.flowClient(s2) != "b" {
		t.Fatalf("baslangic esleme a/b beklenirdi: %q %q", h.flowClient(s1), h.flowClient(s2))
	}

	// a'nin oturumu duser: akislari kapanir, flow silinir.
	fc.dropSession("a")
	waitFor(t, "a'nin flow'u silindi", func() bool { return h.flowCount() == 1 })
	if h.flowClient(s1) != "" {
		t.Fatal("dusen oturumun flow'u silinmeliydi")
	}

	// Sonraki paket yeniden eslenir: a artik cevrimdisi oldugundan b'ye.
	send(t, s1, 10)
	waitFor(t, "yeniden esleme", func() bool { return h.flowClient(s1) == "b" })
	waitFor(t, "paket b'ye ulasti", func() bool { return fc.bytes("b") == 20 })
	if fc.bytes("a") != 10 {
		t.Fatalf("a yalniz ilk paketi almis olmali: %d", fc.bytes("a"))
	}
}

// Akis acma hatasi ilk pakette siradaki adaya gecis yapar (paket kaybolmaz).
func TestReplicaUDPOpenErrorFailsOverImmediately(t *testing.T) {
	fc := fcNoEcho("a", "b")
	fc.set("a", func(c *fakeClient) { c.openErr = errors.New("oturum yazamiyor") })
	h := newUDPRep(t, TunnelInfo{ClientID: "a", Replicas: []string{"b"}}, fc, nil)
	src := h.source()
	send(t, src, 10) // sira a: acilamaz -> b
	waitFor(t, "paket b'ye ulasti", func() bool { return fc.bytes("b") == 10 })
	if h.flowClient(src) != "b" || fc.bytes("a") != 0 {
		t.Fatalf("flow b'ye yapismali: %q a=%d", h.flowClient(src), fc.bytes("a"))
	}
}

// Ajan yerel hedefe baglanamazsa (onay hatasi) flow silinir; sonraki paket yeniden eslenir.
func TestReplicaUDPAckErrorRemapsOnNextPacket(t *testing.T) {
	fc := fcNoEcho("a", "b")
	fc.set("a", func(c *fakeClient) { c.ackCode = "local_unreachable" })
	h := newUDPRep(t, TunnelInfo{ClientID: "a", Replicas: []string{"b"}}, fc, nil)
	src := h.source()
	send(t, src, 10) // a'ya eslenir; onay hatasi flow'u siler
	waitFor(t, "flow acildi", func() bool { return h.ul.stats.flowsNew.Load() == 1 })
	waitFor(t, "a'nin flow'u onay hatasiyla silindi", func() bool { return h.flowCount() == 0 })
	send(t, src, 10) // sayac ilerledi: b'ye eslenir
	waitFor(t, "b'ye yeniden esleme", func() bool { return h.flowClient(src) == "b" })
	waitFor(t, "paket b'ye ulasti", func() bool { return fc.bytes("b") == 10 })
}

func TestReplicaUDPAllOfflineDropsPacket(t *testing.T) {
	fc := fcNoEcho("a", "b")
	fc.set("a", func(c *fakeClient) { c.offline = true })
	fc.set("b", func(c *fakeClient) { c.offline = true })
	h := newUDPRep(t, TunnelInfo{ClientID: "a", Replicas: []string{"b"}}, fc, nil)
	src := h.source()
	send(t, src, 10)
	time.Sleep(100 * time.Millisecond)
	if h.flowCount() != 0 || fc.totalAttempts() != 0 {
		t.Fatalf("cevrimici aday yokken flow acilmamali: flow=%d deneme=%d", h.flowCount(), fc.totalAttempts())
	}
}

// PPS sinirlari replika sayisindan bagimsizdir: tunel siniri TUM replikalar icin toplamdir.
func TestReplicaUDPTunnelPPSLimitIsTunnelWide(t *testing.T) {
	fc := fcNoEcho("a", "b")
	h := newUDPRep(t, TunnelInfo{ClientID: "a", Replicas: []string{"b"},
		UDP: UDPLimits{MaxPPS: 5, MaxFlowPPS: -1}}, fc, nil)
	var srcs []*net.UDPConn
	for i := 0; i < 4; i++ {
		srcs = append(srcs, h.source())
	}
	for round := 0; round < 5; round++ {
		for _, s := range srcs {
			send(t, s, 10)
		}
	}
	waitFor(t, "20 paket islendi", func() bool {
		return h.ul.stats.packetsIn.Load()+h.ul.stats.droppedRate.Load() == 20
	})
	got := h.ul.stats.packetsIn.Load()
	if got < 5 || got > 6 { // 1 sn patlama payi + olasi kucuk dolum
		t.Fatalf("tunel PPS 5: %d paket iletildi", got)
	}
	if delivered := (fc.bytes("a") + fc.bytes("b")) / 10; delivered != got {
		t.Fatalf("iletilen paket sayaci tutarsiz: %d / %d", delivered, got)
	}
}

func TestReplicaUDPFlowPPSLimitIsPerFlow(t *testing.T) {
	fc := fcNoEcho("a", "b")
	h := newUDPRep(t, TunnelInfo{ClientID: "a", Replicas: []string{"b"},
		UDP: UDPLimits{MaxFlowPPS: 3, MaxPPS: -1}}, fc, nil)
	s1, s2 := h.source(), h.source()
	for i := 0; i < 10; i++ {
		send(t, s1, 10)
	}
	waitFor(t, "ilk flow", func() bool { return h.flowCount() == 1 })
	for i := 0; i < 10; i++ {
		send(t, s2, 10)
	}
	waitFor(t, "20 paket islendi", func() bool {
		return h.ul.stats.packetsIn.Load()+h.ul.stats.droppedRate.Load() == 20
	})
	a, b := fc.bytes(h.flowClient(s1))/10, fc.bytes(h.flowClient(s2))/10
	if h.flowClient(s1) == h.flowClient(s2) {
		t.Fatalf("iki flow farkli replikalara dagilmaliydi: %q %q", h.flowClient(s1), h.flowClient(s2))
	}
	if a < 3 || a > 4 || b < 3 || b > 4 {
		t.Fatalf("flow basina 3 paket beklenirdi: %d, %d", a, b)
	}
}

func TestReplicaUDPDropsFlowsOfRemovedReplica(t *testing.T) {
	fc := fcNoEcho("a", "b")
	bal := newFakeBalancer()
	h := newUDPRep(t, TunnelInfo{ClientID: "a", Replicas: []string{"b"}}, fc, bal)
	s1, s2 := h.source(), h.source()
	send(t, s1, 10)
	waitFor(t, "ilk flow", func() bool { return h.flowCount() == 1 })
	send(t, s2, 10)
	waitFor(t, "ikinci flow", func() bool { return h.flowCount() == 2 })
	if h.flowClient(s1) != "a" || h.flowClient(s2) != "b" {
		t.Fatalf("baslangic esleme: %q %q", h.flowClient(s1), h.flowClient(s2))
	}

	// Replika eklenmesi var olan flow'lari bozmaz.
	info := h.ul.info()
	info.Replicas = []string{"b", "c"}
	h.ul.setInfo(info)
	time.Sleep(100 * time.Millisecond)
	if h.flowCount() != 2 {
		t.Fatalf("aday kumesi buyurken flow'lar korunmali: %d", h.flowCount())
	}

	// b kaldirilinca ona yapisik flow birakilir; a'nin flow'u kalir (c hic baglanmadi).
	info = h.ul.info()
	info.Replicas = []string{"c"}
	h.ul.setInfo(info)
	waitFor(t, "kaldirilan replikanin flow'u silindi", func() bool { return h.flowCount() == 1 })
	if h.flowClient(s1) != "a" || h.flowClient(s2) != "" {
		t.Fatalf("a kalmali, b silinmeli: %q %q", h.flowClient(s1), h.flowClient(s2))
	}
	waitFor(t, "b sayaci birakildi", func() bool { return bal.open("b") == 0 })
	if bal.open("a") != 1 {
		t.Fatalf("a hala bir flow tasiyor: %d", bal.open("a"))
	}
	// Sonraki paket kaldirilan replikaya degil, aday kumesine esler.
	send(t, s2, 10)
	waitFor(t, "yeniden esleme", func() bool { return h.flowClient(s2) == "a" })
}

// Replikasiz tunelde setInfo flow'lara dokunmaz (eski davranis).
func TestSingleClientUDPSetInfoKeepsFlows(t *testing.T) {
	ul := newTestListener()
	_, cancel := context.WithCancel(context.Background())
	fs := newFakeStream("", false)
	ul.flows["x"] = &udpFlow{stream: fs, cancel: cancel, clientID: "gone"}
	ul.setInfo(TunnelInfo{TunnelID: "t1", ClientID: "other"})
	time.Sleep(50 * time.Millisecond)
	ul.mu.Lock()
	n := len(ul.flows)
	ul.mu.Unlock()
	if n != 1 || isClosed(fs) {
		t.Fatalf("replikasiz tunelde flow korunmali: flow=%d kapali=%v", n, isClosed(fs))
	}
}

func TestReplicaUDPIdleSweepReleasesCounters(t *testing.T) {
	fc := fcNoEcho("a", "b")
	bal := newFakeBalancer()
	h := newUDPRep(t, TunnelInfo{ClientID: "a", Replicas: []string{"b"},
		UDP: UDPLimits{IdleTimeout: 10 * time.Second}}, fc, bal)
	s1, s2 := h.source(), h.source()
	send(t, s1, 10)
	waitFor(t, "ilk flow", func() bool { return h.flowCount() == 1 })
	send(t, s2, 10)
	waitFor(t, "ikinci flow", func() bool { return h.flowCount() == 2 })
	if bal.open("a") != 1 || bal.open("b") != 1 {
		t.Fatalf("acik-flow sayaci: a=%d b=%d", bal.open("a"), bal.open("b"))
	}

	// Henuz bosta degil: supurme flow'lari korur.
	h.ul.sweepIdle(time.Now())
	if h.flowCount() != 2 {
		t.Fatalf("etkin flow'lar korunmali: %d", h.flowCount())
	}
	// Bosta kalma suresi gecince esleme temizlenir ve sayac birakilir.
	h.ul.sweepIdle(time.Now().Add(time.Minute))
	if h.flowCount() != 0 {
		t.Fatalf("bosta flow'lar temizlenmeli: %d", h.flowCount())
	}
	if bal.open("a") != 0 || bal.open("b") != 0 {
		t.Fatalf("sayaclar birakilmali: a=%d b=%d", bal.open("a"), bal.open("b"))
	}
	for _, id := range []string{"a", "b"} {
		for _, s := range fc.streamsOf(id) {
			if !isClosed(s) {
				t.Fatalf("%s akisi kapatilmali", id)
			}
		}
	}
	// Temizlik sonrasi yeni paket yeni esleme olusturur.
	send(t, s1, 10)
	waitFor(t, "yeni esleme", func() bool { return h.flowClient(s1) != "" })
}

// removeFlow birden cok kez cagrilsa da (idle + kapi iptali + oturum kaybi) sayac bir kez azalir.
func TestReplicaUDPReleaseOnce(t *testing.T) {
	fc := fcNoEcho("a", "b")
	bal := newFakeBalancer()
	h := newUDPRep(t, TunnelInfo{ClientID: "a", Replicas: []string{"b"}}, fc, bal)
	src := h.source()
	send(t, src, 10)
	waitFor(t, "flow", func() bool { return h.flowCount() == 1 })
	key := src.LocalAddr().String()
	h.ul.mu.Lock()
	f := h.ul.flows[key]
	h.ul.mu.Unlock()
	h.ul.removeFlow(key, f)
	h.ul.removeFlow(key, f)
	h.ul.removeFlow(key, f)
	if bal.open("a") != 0 {
		t.Fatalf("sayac bir kez azalmali: %d", bal.open("a"))
	}
}

func TestReplicaUDPListenerCloseReleasesCounters(t *testing.T) {
	fc := fcNoEcho("a", "b")
	bal := newFakeBalancer()
	h := newUDPRep(t, TunnelInfo{ClientID: "a", Replicas: []string{"b"}}, fc, bal)
	s1, s2 := h.source(), h.source()
	send(t, s1, 10)
	waitFor(t, "ilk flow", func() bool { return h.flowCount() == 1 })
	send(t, s2, 10)
	waitFor(t, "ikinci flow", func() bool { return h.flowCount() == 2 })
	h.ul.close()
	if bal.open("a") != 0 || bal.open("b") != 0 {
		t.Fatalf("dinleyici kapaninca sayaclar birakilmali: a=%d b=%d", bal.open("a"), bal.open("b"))
	}
}

// Replikasiz tunelde replika/Balancer mekanizmasina HIC girilmez.
func TestSingleClientUDPIgnoresReplicaMachinery(t *testing.T) {
	fc := fcNoEcho("a")
	bal := newFakeBalancer()
	h := newUDPRep(t, TunnelInfo{ClientID: "a"}, fc, bal)
	src := h.source()
	send(t, src, 10) // Hub'da oturum yok: eski yol flow acmaz
	time.Sleep(100 * time.Millisecond)
	if fc.totalAttempts() != 0 || bal.callCount() != 0 || h.flowCount() != 0 {
		t.Fatalf("replikasiz tunel replika katmanina girmemeli: deneme=%d balancer=%d flow=%d",
			fc.totalAttempts(), bal.callCount(), h.flowCount())
	}
}
