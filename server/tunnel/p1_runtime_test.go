package tunnel

import (
	"context"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/coder/websocket"

	"github.com/tkodcumpeg4/zorven/server/auth"
	"github.com/tkodcumpeg4/zorven/server/store"
	"github.com/tkodcumpeg4/zorven/shared/protocol"
)

// fullStore, ServeHTTP'nin (kimlik + el sikisma + kopus) dokundugu sahte depo.
type fullStore struct {
	versionStore
	client store.Client
}

func (f *fullStore) GetClientByTokenID(_ context.Context, id string) (store.Client, error) {
	if id == f.client.TokenID {
		return f.client, nil
	}
	return store.Client{}, store.ErrNotFound
}
func (f *fullStore) TouchDeviceSeen(context.Context, string) error { return nil }

// closeTrackListener, kabul edilen her baglantinin Close'unu kaydeder.
type closeTrackListener struct {
	net.Listener
	mu     sync.Mutex
	closed []chan struct{}
}

type trackConn struct {
	net.Conn
	once sync.Once
	ch   chan struct{}
}

func (c *trackConn) Close() error {
	c.once.Do(func() { close(c.ch) })
	return c.Conn.Close()
}

func (l *closeTrackListener) Accept() (net.Conn, error) {
	c, err := l.Listener.Accept()
	if err != nil {
		return nil, err
	}
	tc := &trackConn{Conn: c, ch: make(chan struct{})}
	l.mu.Lock()
	l.closed = append(l.closed, tc.ch)
	l.mu.Unlock()
	return tc, nil
}

// O6: ajan kapanis cercevesi gondermeden (TCP kesme / kill -9) giderse sunucu
// alttaki soketi kapatmali; onceden fd surec omru boyunca acik kaliyordu.
func TestAgentAbruptDisconnectClosesServerConn(t *testing.T) {
	full, id, hash, err := auth.GenerateClient()
	if err != nil {
		t.Fatal(err)
	}
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	fs := &fullStore{client: store.Client{ID: "cli_fd", TokenID: id, TokenHash: hash, Name: "fd"}}
	h := &Handler{Store: fs, Hub: NewHub(), Log: log}

	srv := httptest.NewUnstartedServer(h)
	tl := &closeTrackListener{Listener: srv.Listener}
	srv.Listener = tl
	srv.Start()
	defer srv.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	var raw net.Conn
	httpc := &http.Client{Transport: &http.Transport{
		DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
			c, err := (&net.Dialer{}).DialContext(ctx, network, addr)
			raw = c
			return c, err
		},
	}}
	c, _, err := websocket.Dial(ctx, "ws"+strings.TrimPrefix(srv.URL, "http"), &websocket.DialOptions{
		HTTPClient: httpc,
		HTTPHeader: http.Header{"Authorization": {"Bearer " + full}},
	})
	if err != nil {
		t.Fatal(err)
	}
	b, _ := protocol.Marshal(protocol.Hello{Type: protocol.TypeHello, ClientVersion: "0.3.7", Platform: "linux/amd64"}, protocol.TypeHello)
	if err := c.Write(ctx, websocket.MessageText, b); err != nil {
		t.Fatal(err)
	}
	if _, _, err := c.Read(ctx); err != nil { // hello_ack
		t.Fatal(err)
	}
	// Oturum Hub'a kaydolana kadar bekle.
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if _, ok := h.Hub.Get("cli_fd"); ok {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if raw == nil {
		t.Fatal("ham baglanti yakalanamadi")
	}
	// Kapanis cercevesi OLMADAN ham TCP'yi kes.
	_ = raw.Close()

	tl.mu.Lock()
	chs := append([]chan struct{}(nil), tl.closed...)
	tl.mu.Unlock()
	if len(chs) != 1 {
		t.Fatalf("kabul edilen baglanti %d, beklenen 1", len(chs))
	}
	select {
	case <-chs[0]:
	case <-time.After(5 * time.Second):
		t.Fatal("sunucu tarafi baglanti kapatilmadi (fd sizintisi)")
	}
}

// A4 #11: asiri uzun / gecersiz UTF-8 hello alanlari kesilir, el sikisma reddedilmez.
func TestHelloFieldsSanitized(t *testing.T) {
	long := strings.Repeat("9", 20000) // runHello okuma siniri 32 KB
	s, fs := runHello(t, protocol.Hello{
		Type: protocol.TypeHello, ClientVersion: long, Platform: "lin\xffux/amd64\n",
		Hostname: strings.Repeat("h", 1000), IPs: append([]string{"10.0.0.1", strings.Repeat("1", 300)}, make([]string, 200)...),
	})
	if len(s.Version) > maxHelloShort || len(fs.info.AgentVersion) > maxHelloShort {
		t.Fatalf("surum kesilmedi: %d / %d", len(s.Version), len(fs.info.AgentVersion))
	}
	// JSON gecersiz UTF-8 baytini U+FFFD yapar; kontrol karakteri (\n) atilir.
	if s.Platform != "lin\uFFFDux/amd64" || !utf8.ValidString(s.Platform) {
		t.Fatalf("platform %q", s.Platform)
	}
	if len(fs.info.Hostname) > maxHelloHostname {
		t.Fatalf("hostname %d", len(fs.info.Hostname))
	}
	if len(fs.info.IPs) != 2 || fs.info.IPs[0] != "10.0.0.1" || len(fs.info.IPs[1]) > maxHelloIP {
		t.Fatalf("ips %d %v", len(fs.info.IPs), fs.info.IPs[:1])
	}
	// Normal degerler degismeden kalir (eski istemci uyumu).
	s2, _ := runHello(t, protocol.Hello{Type: protocol.TypeHello, ClientVersion: "0.3.7", Platform: "windows/amd64"})
	if s2.Version != "0.3.7" || s2.Platform != "windows/amd64" {
		t.Fatalf("normal degerler bozuldu: %q %q", s2.Version, s2.Platform)
	}
}

func TestCleanHelloFieldRuneBoundary(t *testing.T) {
	got := cleanHelloField(strings.Repeat("ş", 40), 63) // 2 baytlik rune; 63 tek sayi
	if !utf8.ValidString(got) || len(got) != 62 {
		t.Fatalf("rune sinirinda kesilmedi: len=%d valid=%v", len(got), utf8.ValidString(got))
	}
	if got := cleanHelloField("a\xff\xfeb\x1b[31m", 64); got != "ab[31m" {
		t.Fatalf("gecersiz UTF-8/kontrol karakteri temizlenmedi: %q", got)
	}
}

// countHandler, WARN satirlarini sayan slog isleyicisi.
type countHandler struct {
	mu    sync.Mutex
	warns []string
}

func (c *countHandler) Enabled(context.Context, slog.Level) bool { return true }
func (c *countHandler) Handle(_ context.Context, r slog.Record) error {
	if r.Level >= slog.LevelWarn {
		c.mu.Lock()
		c.warns = append(c.warns, r.Message)
		c.mu.Unlock()
	}
	return nil
}
func (c *countHandler) WithAttrs([]slog.Attr) slog.Handler { return c }
func (c *countHandler) WithGroup(string) slog.Handler      { return c }

// A2 #7 / A4 #14: beklenmeyen cerceve seli oturum basina sinirli log uretir.
func TestUnexpectedFrameLogsBounded(t *testing.T) {
	ch := &countHandler{}
	log := slog.New(ch)
	done := make(chan error, 1)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := websocket.Accept(w, r, nil)
		if err != nil {
			return
		}
		done <- NewSession("ses_l", "cli_l", "l", r.RemoteAddr, conn, log).Run(r.Context())
	}))
	defer srv.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	c, _, err := websocket.Dial(ctx, "ws"+strings.TrimPrefix(srv.URL, "http"), nil)
	if err != nil {
		t.Fatal(err)
	}
	for range 300 {
		if err := c.Write(ctx, websocket.MessageText, []byte(`{"type":"bilinmeyen_tip"}`)); err != nil {
			t.Fatal(err)
		}
	}
	c.Close(websocket.StatusNormalClosure, "")
	select {
	case <-done:
	case <-ctx.Done():
		t.Fatal("oturum bitmedi")
	}
	ch.mu.Lock()
	defer ch.mu.Unlock()
	n, summary := 0, 0
	for _, m := range ch.warns {
		switch m {
		case "beklenmeyen mesaj tipi":
			n++
		case "bozuk/beklenmeyen cerceve uyarilari bastirildi":
			summary++
		}
	}
	if n != maxFrameLogs || summary != 1 {
		t.Fatalf("uyari=%d ozet=%d, beklenen %d ve 1 (toplam WARN %d)", n, summary, maxFrameLogs, len(ch.warns))
	}
}

// stallTimings, testin suresince donmus ajan esiklerini kisaltir.
func stallTimings(t *testing.T) {
	t.Helper()
	stallMu.Lock()
	oe, oa, og := stallCheckEvery, stallAfter, stallGrace
	stallCheckEvery, stallAfter, stallGrace = 20*time.Millisecond, 150*time.Millisecond, 150*time.Millisecond
	stallMu.Unlock()
	t.Cleanup(func() {
		stallMu.Lock()
		stallCheckEvery, stallAfter, stallGrace = oe, oa, og
		stallMu.Unlock()
	})
}

// runStallSession, sunucu oturumunu baslatir; respond true ise istemci ping'e
// pong doner (canli ajan), false ise hic okumaz/yazmaz (donmus ajan).
func runStallSession(t *testing.T, respond bool) (sess chan *Session, done chan error, stop func()) {
	t.Helper()
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	sess = make(chan *Session, 1)
	done = make(chan error, 1)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := websocket.Accept(w, r, nil)
		if err != nil {
			return
		}
		s := NewSession("ses_s", "cli_s", "s", r.RemoteAddr, conn, log)
		if _, err := s.newExchange(); err != nil { // bekleyen bir ziyaretci istegi
			t.Error(err)
		}
		sess <- s
		done <- s.Run(r.Context())
	}))
	ctx, cancel := context.WithCancel(context.Background())
	c, _, err := websocket.Dial(ctx, "ws"+strings.TrimPrefix(srv.URL, "http"), nil)
	if err != nil {
		t.Fatal(err)
	}
	var pongs atomic.Int32
	if respond {
		go func() {
			for {
				_, data, err := c.Read(ctx)
				if err != nil {
					return
				}
				if mt, _ := protocol.PeekType(data); mt == protocol.TypePing {
					b, _ := protocol.Marshal(protocol.Pong{Type: protocol.TypePong}, protocol.TypePong)
					_ = c.Write(ctx, websocket.MessageText, b)
					pongs.Add(1)
				}
			}
		}()
	}
	return sess, done, func() { cancel(); c.CloseNow(); srv.Close() }
}

// O15: bekleyen istek varken donmus ajan ~stallAfter+stallGrace icinde dusurulur
// (onceden 68-87 sn). Canli (pong donen) ajan dusurulmez.
func TestFrozenAgentWithPendingDetectedFast(t *testing.T) {
	stallTimings(t)
	_, done, stop := runStallSession(t, false)
	defer stop()
	start := time.Now()
	select {
	case <-done:
		if d := time.Since(start); d > 2*time.Second {
			t.Fatalf("tespit %v surdu", d)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("donmus ajan dusurulmedi")
	}
}

func TestAliveAgentWithPendingNotDropped(t *testing.T) {
	stallTimings(t)
	sc, done, stop := runStallSession(t, true)
	defer stop()
	s := <-sc
	select {
	case err := <-done:
		t.Fatalf("canli ajan dusuruldu: %v", err)
	case <-time.After(1500 * time.Millisecond): // ~5 tespit penceresi
	}
	if s.PendingCount() != 1 {
		t.Fatalf("bekleyen istek %d", s.PendingCount())
	}
}

// Bosta (bekleyen istek yok) oturum erken ping/dusurme yapmaz: davranis eskisi gibi.
func TestStallStepIdleNoProbe(t *testing.T) {
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	s := NewSession("ses_i", "cli_i", "i", "", nil, log)
	now := time.Now().Add(time.Hour)
	if _, probe, dead := s.stallStep(now, time.Time{}, stallAfter, stallGrace); probe || dead {
		t.Fatalf("bosta probe=%v dead=%v", probe, dead)
	}
	if _, err := s.newExchange(); err != nil {
		t.Fatal(err)
	}
	pa, probe, dead := s.stallStep(now, time.Time{}, stallAfter, stallGrace)
	if !probe || dead || pa.IsZero() {
		t.Fatalf("bekleyen varken sessiz: probe=%v dead=%v", probe, dead)
	}
	if _, _, dead := s.stallStep(pa.Add(stallGrace/2), pa, stallAfter, stallGrace); dead {
		t.Fatal("grace dolmadan olu sayildi")
	}
	if _, _, dead := s.stallStep(pa.Add(stallGrace), pa, stallAfter, stallGrace); !dead {
		t.Fatal("grace doldu, olu sayilmadi")
	}
}
