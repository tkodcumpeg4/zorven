package agent

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"

	"github.com/tkodcumpeg4/zorven/client/config"
	"github.com/tkodcumpeg4/zorven/shared/protocol"
)

func quietLog() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

// fakeTunnelServer: hello'yu okur, hello_ack yollar, sonra davranisi mode belirler.
func fakeTunnelServer(t *testing.T, status int, hold time.Duration) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if status != 0 {
			http.Error(w, "x", status)
			return
		}
		c, err := websocket.Accept(w, r, nil)
		if err != nil {
			return
		}
		defer c.CloseNow()
		ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
		defer cancel()
		if _, _, err := c.Read(ctx); err != nil {
			return
		}
		ack, _ := protocol.Marshal(protocol.HelloAck{
			Type: protocol.TypeHelloAck, ClientID: "c1", SessionID: "s1", HeartbeatIntervalS: 30,
			Features: []string{protocol.FeatureUDPProbe, protocol.FeatureDeviceUpdate},
		}, protocol.TypeHelloAck)
		_ = c.Write(ctx, websocket.MessageText, ack)
		time.Sleep(hold)
		c.Close(websocket.StatusNormalClosure, "bye")
	}))
	t.Cleanup(srv.Close)
	return srv
}

func testAgent(srv *httptest.Server) *Agent {
	a := &Agent{ServerAddr: strings.TrimPrefix(srv.URL, "http://"), Token: "t", Insecure: true, Log: quietLog()}
	a.settings = newSettingsState(localCaps{}, false)
	a.zapp = newZappTable()
	return a
}

// 409 artik FatalError degil: ErrAlreadyConnected (yeniden denenir).
func TestConnectOnce409IsRetryableSentinel(t *testing.T) {
	srv := fakeTunnelServer(t, http.StatusConflict, 0)
	a := testAgent(srv)
	err := a.connectOnce(context.Background())
	if !errors.Is(err, ErrAlreadyConnected) {
		t.Fatalf("beklenen ErrAlreadyConnected, alinan %v", err)
	}
	var fatal *FatalError
	if errors.As(err, &fatal) {
		t.Fatal("409 ilk denemede FatalError olmamali")
	}
	// Sure dolunca Run'in verdigi fatal hata da sentinel'i tasir.
	fe := &FatalError{Msg: msgAlreadyConnected, Cause: ErrAlreadyConnected}
	if !errors.Is(fe, ErrAlreadyConnected) {
		t.Fatal("FatalError.Unwrap calismiyor")
	}
}

// Her baglanti basina goroutine'ler baglanti bitince kapanmali (sizinti yok).
func TestReconnectsDoNotLeakGoroutines(t *testing.T) {
	srv := fakeTunnelServer(t, 0, 50*time.Millisecond)
	a := testAgent(srv)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	a.runCtx.Store(&ctx)
	// ilk tur: isinma (http istemci havuzlari vb.)
	_ = a.connectOnce(ctx)
	time.Sleep(200 * time.Millisecond)
	base := runtime.NumGoroutine()
	for i := 0; i < 6; i++ {
		_ = a.connectOnce(ctx)
	}
	deadline := time.Now().Add(3 * time.Second)
	var now int
	for time.Now().Before(deadline) {
		now = runtime.NumGoroutine()
		if now <= base+3 {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if now > base+3 {
		buf := make([]byte, 1<<16)
		n := runtime.Stack(buf, true)
		t.Fatalf("goroutine sizintisi: ilk %d, sonra %d\n%s", base, now, buf[:n])
	}
	if got := a.StatusInfo().Reconnects; got != 6 {
		t.Errorf("reconnects = %d, beklenen 6", got)
	}
}

func TestWakeJump(t *testing.T) {
	tick := 5 * time.Second
	if wakeJump(5*time.Second, 5*time.Second, tick) {
		t.Error("normal tik uyanma sayilmamali")
	}
	if !wakeJump(60*time.Second, 5*time.Second, tick) {
		t.Error("duvar saati sicramasi uyanma sayilmali")
	}
	if !wakeJump(30*time.Second, 30*time.Second, tick) {
		t.Error("cok gec gelen tik duraksama sayilmali")
	}
}

func TestStatusInfoDisconnectRing(t *testing.T) {
	a := &Agent{ServerAddr: "s:1"}
	for i := 0; i < 15; i++ {
		a.observ().recordDisconnect(errors.New("e" + string(rune('a'+i))))
	}
	si := a.StatusInfo()
	if len(si.Disconnects) != 10 || si.Disconnects[9].Reason != "eo" || si.Disconnects[0].Reason != "ef" {
		t.Fatalf("halka tampon: %+v", si.Disconnects)
	}
	if si.LastError != "eo" || si.Server != "s:1" || si.State != "idle" {
		t.Fatalf("durum: %+v", si)
	}
}

func TestTunnelMetricsPercentiles(t *testing.T) {
	tm := newTunnelMetrics()
	for i := 1; i <= 100; i++ {
		tm.request("t")
		tm.observe("t", time.Duration(i)*time.Millisecond)
	}
	tm.addIn("t", 10)
	tm.addOut("t", 20)
	tm.addError("t")
	s := tm.snapshot("t")
	if s.requests != 100 || s.bytesIn != 10 || s.bytesOut != 20 || s.errors != 1 {
		t.Fatalf("sayaclar: %+v", s)
	}
	if s.p50 < 49 || s.p50 > 51 || s.p95 < 94 || s.p95 > 96 {
		t.Fatalf("p50=%v p95=%v", s.p50, s.p95)
	}
	infos := tunnelInfos([]protocol.TunnelSpec{{ID: "t", Hostnames: []string{"a.zorven.app"}, Target: "http://localhost:80"}}, tm)
	if infos[0].Protocol != "http" || infos[0].URL != "https://a.zorven.app" || infos[0].Requests != 100 {
		t.Fatalf("info: %+v", infos[0])
	}
}

func TestWindowAddIgnoresUnknown(t *testing.T) {
	w := newWindowTable()
	w.add(99, 1000)
	if len(w.remaining) != 0 {
		t.Fatal("bilinmeyen istek icin kayit olusmamali")
	}
	if err := w.acquire(context.Background(), 5, 10); err != nil {
		t.Fatal(err)
	}
	w.add(5, 100)
	w.release(5)
	w.add(5, 100) // bitmis istek
	if len(w.remaining) != 0 {
		t.Fatal("bitmis istek icin kayit olusmamali")
	}
}

func TestWriteQueueOverflowCancelsOnlyThatQueue(t *testing.T) {
	block := make(chan struct{})
	failed := make(chan error, 1)
	q := newWriteQueue(func(b []byte, d time.Duration) error { <-block; return nil }, nil, func(e error) { failed <- e })
	q.maxBytes = 1000
	defer close(block)
	start := time.Now()
	var err error
	for i := 0; i < 100 && err == nil; i++ {
		err = q.push(make([]byte, 100))
	}
	if !errors.Is(err, errWriteQueueOverflow) {
		t.Fatalf("tasma bekleniyordu: %v", err)
	}
	if time.Since(start) > time.Second {
		t.Fatal("push bloklamamali")
	}
	select {
	case e := <-failed:
		if !errors.Is(e, errWriteQueueOverflow) {
			t.Fatalf("onFail: %v", e)
		}
	case <-time.After(time.Second):
		t.Fatal("onFail cagrilmadi")
	}
}

func TestWriteQueueOrderAndFin(t *testing.T) {
	var got []byte
	done := make(chan struct{})
	q := newWriteQueue(func(b []byte, d time.Duration) error { got = append(got, b...); return nil }, func() { close(done) }, nil)
	for _, s := range []string{"a", "b", "c"} {
		_ = q.push([]byte(s))
	}
	q.finish()
	<-done
	if string(got) != "abc" {
		t.Fatalf("siralama bozuk: %q", got)
	}
}

func TestWriteQueueStalledConnFails(t *testing.T) {
	a, b := net.Pipe() // b okunmuyor -> yazim bloklanir
	defer a.Close()
	defer b.Close()
	failed := make(chan error, 1)
	q := newConnWriteQueue(a, func(e error) { failed <- e })
	q.timeout = 100 * time.Millisecond
	_ = q.push([]byte("x"))
	select {
	case e := <-failed:
		if !errors.Is(e, errWriteStalled) {
			t.Fatalf("zaman asimi hatasi bekleniyordu: %v", e)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("yazim zaman asimi calismadi")
	}
}

// Yavas yerel servis okuma dongusunu BLOKLAMAMALI: routeBody hemen doner,
// tasarsa yalniz o istek iptal edilir.
func TestRouteBodyDoesNotBlockOnSlowLocal(t *testing.T) {
	release := make(chan struct{})
	local := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-release // govdeyi hic okumaz
	}))
	defer local.Close()
	defer close(release)
	cs, srvConn, ctx := probePair(t)
	cs.log = quietLog()
	go func() { // sunucu ucunu bosalt
		for {
			if _, _, err := srvConn.Read(ctx); err != nil {
				return
			}
		}
	}()
	cs.SetTargets([]protocol.TunnelSpec{{ID: "t", Target: local.URL}})
	cs.handleRequest(ctx, protocol.HTTPRequest{Type: protocol.TypeHTTPRequest, ReqID: 1, TunnelID: "t", Method: "POST", Path: "/", HasBody: true, ContentLength: 100 << 20})
	chunk := make([]byte, protocol.BodyChunkSize)
	start := time.Now()
	for i := 0; i < 200; i++ { // 12.8 MB > kuyruk tavani
		cs.routeBody(protocol.BodyFrame{FrameType: protocol.FrameRequestBody, ReqID: 1, Payload: chunk})
	}
	if d := time.Since(start); d > 2*time.Second {
		t.Fatalf("routeBody bloklandi: %v", d)
	}
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		cs.mu.Lock()
		n := len(cs.inflight)
		cs.mu.Unlock()
		if n == 0 {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("tasan istek iptal edilmedi")
}

func TestHTTPClientHasNoOverallTimeout(t *testing.T) {
	cs := newClientSession(nil, "", quietLog())
	if cs.http.Timeout != 0 {
		t.Fatal("genel Timeout olmamali (uzun yanitlari keser)")
	}
	tr := cs.http.Transport.(*http.Transport)
	if tr.MaxIdleConnsPerHost < 32 || tr.ResponseHeaderTimeout <= 0 {
		t.Fatalf("transport: perhost=%d hdr=%v", tr.MaxIdleConnsPerHost, tr.ResponseHeaderTimeout)
	}
}

func TestIdleBodyCancelsStalledRead(t *testing.T) {
	pr, pw := io.Pipe()
	defer pw.Close()
	ctx, cancel := context.WithCancel(context.Background())
	r := newIdleBody(pr, 80*time.Millisecond, func() { cancel(); pr.CloseWithError(context.Canceled) })
	_, err := r.Read(make([]byte, 8))
	if err == nil || ctx.Err() == nil {
		t.Fatalf("bosta zaman asimi calismadi: %v", err)
	}
}

func TestSetTargetsReportsChangedAndClosesConns(t *testing.T) {
	cs := newClientSession(nil, "", quietLog())
	cs.SetTargets([]protocol.TunnelSpec{{ID: "a", Target: "x:1"}, {ID: "b", Target: "x:2"}, {ID: "c", Target: "x:3"}})
	a1, a2 := net.Pipe()
	b1, b2 := net.Pipe()
	defer a2.Close()
	defer b2.Close()
	cs.registerStream(1, a1)
	cs.noteConn(1, "a")
	cs.registerWS(2, b1)
	cs.noteConn(2, "b")
	changed := cs.SetTargets([]protocol.TunnelSpec{{ID: "a", Target: "x:1"}, {ID: "b", Target: "x:9"}})
	got := map[string]bool{}
	for _, id := range changed {
		got[id] = true
	}
	if !got["b"] || !got["c"] || got["a"] {
		t.Fatalf("changed = %v", changed)
	}
	cs.closeTunnelConns(changed)
	cs.streamMu.Lock()
	_, aOpen := cs.streamConns[1]
	cs.streamMu.Unlock()
	cs.wsMu.Lock()
	_, bOpen := cs.wsConns[2]
	cs.wsMu.Unlock()
	if !aOpen || bOpen {
		t.Fatalf("a acik kalmali, b kapanmali: a=%v b=%v", aOpen, bOpen)
	}
}

func TestGuardedDialResolvesAndDialsCheckedIP(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Skip(err)
	}
	defer ln.Close()
	_, port, _ := net.SplitHostPort(ln.Addr().String())
	c, err := guardedDial(context.Background(), "tcp", "localhost:"+port, false, time.Second)
	if err != nil {
		t.Fatalf("localhost cozulup baglanmali: %v", err)
	}
	c.Close()
	// Cozumlenemeyen ad: hata yutulmaz.
	if _, err := guardedDial(context.Background(), "tcp", "yok.invalid:80", false, time.Second); err == nil || !strings.Contains(err.Error(), "cozumlenemedi") {
		t.Fatalf("DNS hatasi yutuldu: %v", err)
	}
}

func TestSwapBackAndMarker(t *testing.T) {
	dir := t.TempDir()
	exe := filepath.Join(dir, "zorven")
	old := exe + ".old"
	os.WriteFile(exe, []byte("yeni"), 0o755)
	os.WriteFile(old, []byte("eski"), 0o755)
	if err := swapBack(exe, old); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(exe); string(b) != "eski" {
		t.Fatalf("exe = %q", b)
	}
	if _, err := os.Stat(old); !os.IsNotExist(err) {
		t.Fatal("old tuketilmeli")
	}
	if err := writeMarker(exe, updateMarker{From: "1", To: "2", OldPath: old}); err != nil {
		t.Fatal(err)
	}
	if m, ok := readMarker(exe); !ok || m.To != "2" {
		t.Fatalf("marker: %+v %v", m, ok)
	}
	os.WriteFile(badPath(exe), []byte("2\n"), 0o600)
	if !badVersion(exe, "2") || badVersion(exe, "3") {
		t.Fatal("badVersion")
	}
}

func TestDownloadToFileStreamsAndHashes(t *testing.T) {
	payload := strings.Repeat("zorven", 100000)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { io.WriteString(w, payload) }))
	defer srv.Close()
	a := &Agent{Insecure: true, ServerAddr: strings.TrimPrefix(srv.URL, "http://"), Log: quietLog()}
	dir := t.TempDir()
	p, sum, err := a.downloadToFile(context.Background(), "/x", dir, 10<<20)
	if err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(p)
	if string(b) != payload || len(sum) != 64 {
		t.Fatal("icerik/sha bozuk")
	}
	if _, _, err := a.downloadToFile(context.Background(), "/x", dir, 1000); err == nil {
		t.Fatal("boyut siniri uygulanmadi")
	}
	if ents, _ := os.ReadDir(dir); len(ents) != 1 {
		t.Fatalf("basarisiz indirme temp dosya birakti: %d", len(ents))
	}
}

func TestWaitIdleForUpdateDefersThenProceeds(t *testing.T) {
	a := &Agent{Log: quietLog()}
	a.curFM = newFileManager(nil, quietLog(), t.TempDir(), 0)
	a.curFM.xfers[1] = &agentXfer{}
	go func() { time.Sleep(60 * time.Millisecond); a.curFM.finish(1) }()
	start := time.Now()
	if !a.waitIdleForUpdate(context.Background(), 5*time.Second, 10*time.Millisecond) {
		t.Fatal("ctx bitmedi, true beklenir")
	}
	if time.Since(start) < 50*time.Millisecond {
		t.Fatal("aktif oturum varken ertelenmedi")
	}
	a.curFM.xfers[2] = &agentXfer{}
	start = time.Now()
	a.waitIdleForUpdate(context.Background(), 80*time.Millisecond, 10*time.Millisecond)
	if time.Since(start) > time.Second {
		t.Fatal("azami erteleme asildi")
	}
}

func TestApplyReloadCapsAndServer(t *testing.T) {
	a := &Agent{ServerAddr: "old:1", Token: "t1", Log: quietLog()}
	a.settings = newSettingsState(localCaps{terminal: true, screen: true}, true)
	a.wakeCh = make(chan struct{}, 1)
	info, err := a.applyReload(config.Config{ServerAddr: "new:2", Token: "t2", EnableTerminal: false, EnableScreen: true})
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]bool{"terminal": true, "server": true, "token": true}
	for _, c := range info.Changed {
		delete(want, c)
	}
	if len(want) != 0 || !info.Reconnecting {
		t.Fatalf("reload sonucu: %+v", info)
	}
	if a.serverAddr() != "new:2" || a.token() != "t2" {
		t.Fatal("sunucu/token uygulanmadi")
	}
	if c := a.Caps(); c.TerminalCap || !c.ScreenCap {
		t.Fatalf("caps: %+v", c)
	}
	if len(a.wakeCh) != 1 || !a.netChanged.Load() {
		t.Fatal("yeniden baglanma tetiklenmedi")
	}
	// Degisiklik yoksa sessiz.
	info, _ = a.applyReload(config.Config{ServerAddr: "new:2", Token: "t2", EnableScreen: true})
	if len(info.Changed) != 0 || info.Reconnecting {
		t.Fatalf("degisiklik yokken: %+v", info)
	}
}

// Sunucunun gonderdigi Bye.Code yapisal olarak okunur; serbest metinde "token" gecmesi fatal degil.
func TestByeCodeJSON(t *testing.T) {
	b, _ := json.Marshal(protocol.Bye{Type: protocol.TypeBye, Reason: "token", Code: protocol.ByeCodeReplaced})
	var got protocol.Bye
	_ = json.Unmarshal(b, &got)
	if got.Code != protocol.ByeCodeReplaced {
		t.Fatal("Bye.Code tasinmiyor")
	}
}
