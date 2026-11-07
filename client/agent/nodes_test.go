package agent

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/coder/websocket"

	"github.com/tkodcumpeg4/zorven/shared/protocol"
)

func addrs(c []nodeCandidate) string {
	var s []string
	for _, x := range c {
		s = append(s, x.Addr)
	}
	return strings.Join(s, ",")
}

// Secim algoritmasi: tablo testi.
func TestRankNodes(t *testing.T) {
	ms := time.Millisecond
	nodes := []protocol.NodeHint{
		{NodeID: "a", Address: "a:443", Priority: 0, Healthy: true},
		{NodeID: "b", Address: "b:443", Priority: 0, Healthy: true},
		{NodeID: "c", Address: "c:443", Priority: 1, Healthy: true},
		{NodeID: "d", Address: "d:443", Priority: 0, Healthy: false},
	}
	cases := []struct {
		name string
		in   rankInput
		want string
	}{
		{"liste yok: tohum sirasi", rankInput{Seeds: []string{"s1:1", "s2:1"}}, "s1:1,s2:1"},
		{"tohumlar arasi en hizli", rankInput{Seeds: []string{"s1:1", "s2:1"},
			RTT: map[string]time.Duration{"s1:1": 90 * ms, "s2:1": 10 * ms}}, "s2:1,s1:1"},
		{"ayni oncelikte rtt belirler", rankInput{Nodes: nodes,
			RTT: map[string]time.Duration{"a:443": 50 * ms, "b:443": 5 * ms}}, "b:443,a:443,c:443,d:443"},
		{"oncelik rtt'den once gelir", rankInput{Nodes: nodes,
			RTT: map[string]time.Duration{"a:443": 90 * ms, "c:443": 1 * ms}}, "a:443,b:443,c:443,d:443"},
		{"atanmis once", rankInput{Nodes: nodes, Assigned: "c",
			RTT: map[string]time.Duration{"a:443": 1 * ms}}, "c:443,a:443,b:443,d:443"},
		{"atanmis basarisizsa sona", rankInput{Nodes: nodes, Assigned: "c",
			Failed: map[string]bool{"c:443": true}}, "a:443,b:443,d:443,c:443"},
		{"saglikli olmayan atanmis once degil", rankInput{Nodes: nodes, Assigned: "d"}, "a:443,b:443,c:443,d:443"},
		{"tohum listedeki adresle birlesir", rankInput{Nodes: nodes, Seeds: []string{"B:443", "z:9"}}, "a:443,b:443,c:443,z:9,d:443"},
	}
	for _, c := range cases {
		if got := addrs(rankNodes(c.in)); got != c.want {
			t.Errorf("%s: %s, beklenen %s", c.name, got, c.want)
		}
	}
}

// Yuk devri sirasi: atanmis dugum dusunce siradaki aday, sonra digerleri;
// hepsi basarisiz olunca (cooldown) yine de dongu surer.
func TestFailoverOrder(t *testing.T) {
	nodes := []protocol.NodeHint{
		{NodeID: "a", Address: "a:1", Healthy: true},
		{NodeID: "b", Address: "b:1", Priority: 1, Healthy: true},
		{NodeID: "c", Address: "c:1", Priority: 2, Healthy: true},
	}
	now := time.Now()
	b := newNodeBook([]string{"a:1"}, "", nil, nil)
	b.now = func() time.Time { return now }
	b.connected("a:1", protocol.HelloAck{NodeID: "a", AssignedNode: "a", Nodes: nodes})

	var seq []string
	for i := 0; i < 4; i++ {
		got := b.pick(context.Background(), "a:1")
		seq = append(seq, got)
		b.markFail(got)
	}
	if strings.Join(seq, ",") != "a:1,b:1,c:1,a:1" {
		// 4. denemede hepsi cooldown'da: en eski degil, kararli siralama (a) beklenir.
		t.Fatalf("sira: %v", seq)
	}
	// Cooldown dolunca yine atanmis dugum ilk aday.
	now = now.Add(failCooldown + time.Second)
	if got := b.pick(context.Background(), "a:1"); got != "a:1" {
		t.Fatalf("cooldown sonrasi %s", got)
	}
}

func TestNeedsProbe(t *testing.T) {
	if needsProbe([]nodeCandidate{{Addr: "a", Assigned: true}, {Addr: "b"}}) {
		t.Fatal("atanmis varken olcum gereksiz")
	}
	if !needsProbe([]nodeCandidate{{Addr: "a"}, {Addr: "b"}}) {
		t.Fatal("iki olculmemis aday olculmeli")
	}
	if needsProbe([]nodeCandidate{{Addr: "a"}}) {
		t.Fatal("tek aday")
	}
	if needsProbe([]nodeCandidate{{Addr: "a", RTT: 1}, {Addr: "b", RTT: 2}}) {
		t.Fatal("hepsi olculmus")
	}
}

// Olcumle en yakin secilir; olcumu basarisiz olan dugum elenir.
func TestPickMeasuresNearest(t *testing.T) {
	probe := func(_ context.Context, addr string) (time.Duration, error) {
		switch addr {
		case "far:1":
			return 80 * time.Millisecond, nil
		case "near:1":
			return 5 * time.Millisecond, nil
		}
		return 0, errors.New("ulasilamiyor")
	}
	b := newNodeBook([]string{"far:1", "near:1", "dead:1"}, "", nil, probe)
	if got := b.pick(context.Background(), "far:1"); got != "near:1" {
		t.Fatalf("pick=%s", got)
	}
	_, list := b.snapshot()
	if addrs(list) != "near:1,far:1,dead:1" {
		t.Fatalf("liste %s", addrs(list))
	}
}

func TestNodeCacheRoundTripAndCorrupt(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "nodes.json")
	now := time.Now()
	good := nodeCacheFile{Seed: "s:443", SavedAt: now,
		Nodes: []protocol.NodeHint{{NodeID: "a", Address: "https://a:443", Healthy: true}}, Assigned: "a"}
	if err := saveNodeCache(p, good); err != nil {
		t.Fatal(err)
	}
	c, ok := loadNodeCache(p, "S:443", now)
	if !ok || c.Assigned != "a" || c.Nodes[0].Address != "a:443" {
		t.Fatalf("gidis-donus: %v %+v", ok, c)
	}
	if _, ok := loadNodeCache(p, "baska:443", now); ok {
		t.Fatal("baska tohumun onbellegi yok sayilmali")
	}
	if _, ok := loadNodeCache(p, "s:443", now.Add(maxNodeCacheAge+time.Hour)); ok {
		t.Fatal("eski onbellek yok sayilmali")
	}
	for name, body := range map[string]string{
		"bozuk json":  "{not json",
		"bos":         "",
		"dizi":        "[1,2]",
		"adres bos":   `{"version":1,"seed":"s:443","nodes":[{"node_id":"a","address":""}]}`,
		"surum":       `{"version":9,"seed":"s:443","nodes":[{"node_id":"a","address":"a:1"}]}`,
		"dugum yok":   `{"version":1,"seed":"s:443","nodes":[]}`,
		"kimlik yok":  `{"version":1,"seed":"s:443","nodes":[{"address":"a:1"}]}`,
		"yanlis tipi": `{"version":1,"seed":"s:443","nodes":"x"}`,
	} {
		if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
		if _, ok := loadNodeCache(p, "s:443", now); ok {
			t.Errorf("%s: bozuk onbellek kabul edildi", name)
		}
		// Defter bozuk onbellekle de sorunsuz kurulur ve tohuma duser.
		b := newNodeBook([]string{"s:443"}, p, nil, nil)
		if got := b.pick(context.Background(), "s:443"); got != "s:443" {
			t.Errorf("%s: pick=%s", name, got)
		}
	}
	if _, ok := loadNodeCache(filepath.Join(dir, "yok.json"), "s:443", now); ok {
		t.Fatal("olmayan dosya")
	}
}

// Sahte dugum: hello alir, ack gonderir, sonra baglantiyi kapatir.
type fakeNode struct {
	srv   *httptest.Server
	mu    sync.Mutex
	hello []protocol.Hello
}

func (f *fakeNode) addr() string { return strings.TrimPrefix(f.srv.URL, "http://") }
func (f *fakeNode) hellos() []protocol.Hello {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]protocol.Hello(nil), f.hello...)
}

func newFakeNode(t *testing.T, ack func(f *fakeNode) []byte) *fakeNode {
	t.Helper()
	f := &fakeNode{}
	f.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c, err := websocket.Accept(w, r, nil)
		if err != nil {
			return
		}
		defer c.CloseNow()
		_, data, err := c.Read(r.Context())
		if err != nil {
			return
		}
		var h protocol.Hello
		_ = json.Unmarshal(data, &h)
		f.mu.Lock()
		f.hello = append(f.hello, h)
		f.mu.Unlock()
		_ = c.Write(r.Context(), websocket.MessageText, ack(f))
		c.Close(websocket.StatusNormalClosure, "")
	}))
	t.Cleanup(f.srv.Close)
	return f
}

func nodeTestAgent(t *testing.T, seed string) *Agent {
	t.Helper()
	return &Agent{ServerAddr: seed, Token: "t", Insecure: true, NoAutoUpdate: true,
		Log: slog.New(slog.NewTextHandler(io.Discard, nil))}
}

// Sahte sunucularla yeniden baglanma: A atanmis dugum dusup kapaninca ajan B'ye
// gecer, Hello.PrevNode eski dugumu bildirir, liste dosyaya yazilir.
func TestReconnectFailsOverToOtherNode(t *testing.T) {
	var a, b *fakeNode
	mk := func(self string) func(*fakeNode) []byte {
		return func(*fakeNode) []byte {
			ack, _ := json.Marshal(protocol.HelloAck{Type: protocol.TypeHelloAck, ClientID: "c", SessionID: "s",
				NodeID: self, AssignedNode: "a",
				Nodes: []protocol.NodeHint{
					{NodeID: "a", Address: "https://" + a.addr(), Healthy: true},
					{NodeID: "b", Address: b.addr(), Priority: 1, Healthy: true},
				}})
			return ack
		}
	}
	a = newFakeNode(t, mk("a"))
	b = newFakeNode(t, mk("b"))

	cache := filepath.Join(t.TempDir(), "nodes.json")
	ag := nodeTestAgent(t, a.addr())
	ag.NodeCachePath = cache
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	_ = ag.connectOnce(ctx) // 1: A, liste ogrenilir
	if len(a.hellos()) != 1 || a.hellos()[0].PrevNode != "" {
		t.Fatalf("A hello: %+v", a.hellos())
	}
	if _, ok := loadNodeCache(cache, a.addr(), time.Now()); !ok {
		t.Fatal("dugum onbellegi yazilmadi")
	}

	a.srv.Close() // atanmis dugum cokuyor
	err := ag.connectOnce(ctx)
	if err == nil {
		t.Fatal("kapali dugume baglanti hatasi bekleniyordu")
	}
	_ = ag.connectOnce(ctx) // sonraki aday: B
	hb := b.hellos()
	if len(hb) != 1 {
		t.Fatalf("B hello sayisi %d", len(hb))
	}
	if hb[0].PrevNode != "a" {
		t.Fatalf("PrevNode=%q, beklenen a", hb[0].PrevNode)
	}
	cur, pinned, nodes := ag.NodeStatus()
	if cur != b.addr() || pinned || len(nodes) != 2 {
		t.Fatalf("durum: %s %v %+v", cur, pinned, nodes)
	}

	// Yeniden baslatma: yeni ajan yalniz onbellekten (A olu) B'ye gider.
	ag2 := nodeTestAgent(t, a.addr())
	ag2.NodeCachePath = cache
	if got := ag2.pickAddr(ctx); got == "" {
		t.Fatal("bos adres")
	}
}

// Eski sunucu (dugum alani yok): tek adresli eski davranis, onbellek yazilmaz.
func TestOldServerNoNodes(t *testing.T) {
	old := newFakeNode(t, func(*fakeNode) []byte {
		ack, _ := json.Marshal(protocol.HelloAck{Type: protocol.TypeHelloAck, ClientID: "c", SessionID: "s"})
		return ack
	})
	cache := filepath.Join(t.TempDir(), "nodes.json")
	ag := nodeTestAgent(t, old.addr())
	ag.NodeCachePath = cache
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	_ = ag.connectOnce(ctx)
	_ = ag.connectOnce(ctx)
	if len(old.hellos()) != 2 {
		t.Fatalf("hello sayisi %d", len(old.hellos()))
	}
	if _, err := os.Stat(cache); err == nil {
		t.Fatal("liste yokken onbellek yazilmamali")
	}
	if _, _, nodes := ag.NodeStatus(); len(nodes) != 0 {
		t.Fatalf("liste olmamali: %+v", nodes)
	}
}

// Sabitlenmis sunucu: onbellekte baska dugumler olsa da yalniz ServerAddr.
func TestPinnedServerIgnoresList(t *testing.T) {
	cache := filepath.Join(t.TempDir(), "nodes.json")
	if err := saveNodeCache(cache, nodeCacheFile{Seed: "pin:1", SavedAt: time.Now(),
		Nodes: []protocol.NodeHint{{NodeID: "x", Address: "x:1", Healthy: true}}, Assigned: "x"}); err != nil {
		t.Fatal(err)
	}
	ag := nodeTestAgent(t, "pin:1")
	ag.NodeCachePath = cache
	ag.ServerPinned = true
	ag.Servers = []string{"other:1"}
	for i := 0; i < 3; i++ {
		ag.noteAddrFail("pin:1")
		if got := ag.pickAddr(context.Background()); got != "pin:1" {
			t.Fatalf("sabit sunucu degisti: %s", got)
		}
	}
	if _, pinned, nodes := ag.NodeStatus(); !pinned || len(nodes) != 0 {
		t.Fatal("sabit durum yanlis")
	}
	if ag.prevNodeHint() != "" {
		t.Fatal("sabitken PrevNode gonderilmez")
	}
}

// Birden cok sunucu adresi (servers) tohum olarak yuk devrine girer.
func TestServersListAsSeeds(t *testing.T) {
	ag := nodeTestAgent(t, "first:1")
	ag.Servers = []string{"second:1", "first:1"}
	b := ag.book()
	b.probe = nil
	if got := ag.pickAddr(context.Background()); got != "first:1" {
		t.Fatalf("ilk deneme %s", got)
	}
	ag.noteAddrFail("first:1")
	if got := ag.pickAddr(context.Background()); got != "second:1" {
		t.Fatalf("yuk devri %s", got)
	}
}

// Reload ile ServerAddr degisince eski liste gecerliligini yitirir.
func TestSeedChangeResetsList(t *testing.T) {
	ag := nodeTestAgent(t, "one:1")
	b := ag.book()
	b.probe = nil
	b.connected("one:1", protocol.HelloAck{NodeID: "n", Nodes: []protocol.NodeHint{
		{NodeID: "n", Address: "one:1", Healthy: true}, {NodeID: "m", Address: "m:1", Priority: 1, Healthy: true}}})
	ag.cfgMu.Lock()
	ag.ServerAddr = "two:1"
	ag.cfgMu.Unlock()
	if got := ag.pickAddr(context.Background()); got != "two:1" {
		t.Fatalf("yeni tohum %s", got)
	}
}

// Tohum alan adiyken IP ipucu (eski sunucularin ic kume adresi) aday olmaz.
func TestRankNodesSkipsIPHintsForHostnameSeed(t *testing.T) {
	in := rankInput{
		Seeds:    []string{"zorven.app:443"},
		Nodes:    []protocol.NodeHint{{NodeID: "p", Address: "203.0.113.7:443", Healthy: true}},
		Assigned: "p",
	}
	got := rankNodes(in)
	if len(got) != 1 || got[0].Addr != "zorven.app:443" {
		t.Fatalf("yalniz tohum bekleniyordu: %+v", got)
	}
	// Dogru (alan adli) ipucu ayni kimlikle gelince kullanilir.
	in.Nodes = []protocol.NodeHint{{NodeID: "p", Address: "zorven.app:443", Healthy: true}}
	got = rankNodes(in)
	if len(got) != 1 || got[0].NodeID != "p" || !got[0].Assigned {
		t.Fatalf("atanmis dugum bekleniyordu: %+v", got)
	}
	// Tohum IP ise (self-host) IP ipucu serbest.
	in.Seeds = []string{"10.0.0.5:443"}
	in.Nodes = []protocol.NodeHint{{NodeID: "q", Address: "10.0.0.6:443", Healthy: true}}
	if got = rankNodes(in); len(got) != 2 {
		t.Fatalf("IP tohumda IP ipucu kalmali: %+v", got)
	}
	if !isIPHost("[2001:db8::1]:443") || isIPHost("zorven.app") {
		t.Fatal("isIPHost")
	}
}

// 2026-11-07 olayi: geri cekilme (60 sn) eski isaret suresinden (30 sn) uzun
// oldugunda ajan her denemede bozuk atanmis dugumu seciyordu. Artik basarisiz
// dugum, geri cekilmeden sonra da sonda kalmali ve tohum denenmeli.
func TestPickDoesNotStickToFailedNodeAfterBackoff(t *testing.T) {
	now := time.Unix(1_800_000_000, 0)
	b := newNodeBook([]string{"panel.zorven.app"}, "", nil, nil)
	b.now = func() time.Time { return now }
	b.nodes = []protocol.NodeHint{{NodeID: "n1", Address: "edge1.zorven.app:443", Healthy: true}}
	b.assigned = "n1"
	if got := b.pick(context.Background(), "panel.zorven.app"); got != "edge1.zorven.app:443" {
		t.Fatalf("ilk deneme atanmis dugum olmali: %s", got)
	}
	b.markFail("edge1.zorven.app:443")
	for _, wait := range []time.Duration{65 * time.Second, 2 * time.Minute, 5 * time.Minute} {
		now = now.Add(wait)
		if got := b.pick(context.Background(), "panel.zorven.app"); got != "panel.zorven.app" {
			t.Fatalf("%s sonra tohum denenmeli: %s", wait, got)
		}
	}
	// Hepsi basarisizsa en eski basarisiz olan once (dongusel).
	b.markFail("panel.zorven.app")
	if got := b.pick(context.Background(), "panel.zorven.app"); got != "edge1.zorven.app:443" {
		t.Fatalf("en eski basarisiz once denenmeli: %s", got)
	}
	now = now.Add(time.Second)
	b.markFail("edge1.zorven.app:443")
	if got := b.pick(context.Background(), "panel.zorven.app"); got != "panel.zorven.app" {
		t.Fatalf("dongusel siralama bekleniyordu: %s", got)
	}
}
