package ingress

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/tkodcumpeg4/zorven/server/reqlog"
	"github.com/tkodcumpeg4/zorven/server/tunnel"
	"github.com/tkodcumpeg4/zorven/shared/protocol"
)

// wsIDAgent: ws_open basliklarini kaydeden ve verilen basliklarla 101 donen
// sahte ajan.
func wsIDAgent(t *testing.T, respHdr map[string][]string) (*tunnel.Session, <-chan protocol.WSOpen) {
	t.Helper()
	ready := make(chan *tunnel.Session, 1)
	opens := make(chan protocol.WSOpen, 4)
	ctx, cancel := context.WithCancel(context.Background())
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c, err := websocket.Accept(w, r, nil)
		if err != nil {
			return
		}
		s := tunnel.NewSession("s1", "cli", "c", "127.0.0.1:1", c, slog.New(slog.NewTextHandler(io.Discard, nil)))
		ready <- s
		_ = s.Run(ctx)
	}))
	ac, _, err := websocket.Dial(ctx, "ws"+strings.TrimPrefix(srv.URL, "http"), nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { cancel(); ac.CloseNow(); srv.Close() })
	go func() {
		for {
			typ, data, err := ac.Read(ctx)
			if err != nil {
				return
			}
			if typ != websocket.MessageText {
				continue
			}
			if pt, _ := protocol.PeekType(data); pt == protocol.TypeWSOpen {
				var m protocol.WSOpen
				_ = json.Unmarshal(data, &m)
				opens <- m
				b, _ := protocol.Marshal(protocol.WSAccept{Type: protocol.TypeWSAccept, ReqID: m.ReqID,
					Status: http.StatusSwitchingProtocols, Headers: respHdr}, protocol.TypeWSAccept)
				_ = ac.Write(ctx, websocket.MessageText, b)
			}
		}
	}()
	return <-ready, opens
}

func wsHandshake(t *testing.T, url string, extra string) *http.Response {
	t.Helper()
	c, err := net.Dial("tcp", strings.TrimPrefix(url, "http://"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { c.Close() })
	_, _ = c.Write([]byte("GET /ws HTTP/1.1\r\nHost: x\r\nUpgrade: websocket\r\nConnection: Upgrade\r\n" + extra + "\r\n"))
	_ = c.SetReadDeadline(time.Now().Add(5 * time.Second))
	resp, err := http.ReadResponse(bufio.NewReader(c), nil)
	if err != nil {
		t.Fatal(err)
	}
	return resp
}

// B1: WebSocket yolunda HTTP ile ayni kimlik kurali. Ziyaretcinin sahte
// X-Zorven-Request-Id'si ezilir, gecerli X-Request-Id korunur; hedef ve 101
// ayni Zorven kimligini gorur ve bu kimlik request_logs kaydinin kimligidir.
func TestWSRequestIDLocal(t *testing.T) {
	sess, opens := wsIDAgent(t, map[string][]string{"Upgrade": {"websocket"}})
	h := &Handler{ReqLog: reqlog.New(10)}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h.serveWebSocket(w, r, sess, "tun", "ten")
	}))
	t.Cleanup(srv.Close)

	resp := wsHandshake(t, srv.URL, "X-Zorven-Request-Id: sahte\r\nX-Request-Id: app-123\r\n")
	if resp.StatusCode != http.StatusSwitchingProtocols {
		t.Fatalf("101 bekleniyordu: %d", resp.StatusCode)
	}
	op := <-opens
	sent := http.Header(op.Headers)
	zid := sent.Get(headerZorvenRequestID)
	if !strings.HasPrefix(zid, "req_") {
		t.Fatalf("hedefe Zorven kimligi gitmedi: %q", zid)
	}
	if got := sent.Values(headerZorvenRequestID); len(got) != 1 {
		t.Fatalf("hedefe tek kimlik gitmeli: %v", got)
	}
	if sent.Get("X-Request-Id") != "app-123" {
		t.Fatalf("ziyaretcinin X-Request-Id'si korunmali: %q", sent.Get("X-Request-Id"))
	}
	if got := resp.Header.Get(headerZorvenRequestID); got != zid {
		t.Fatalf("101 kimligi %q, hedefe giden %q", got, zid)
	}
	if got := resp.Header.Get("X-Request-Id"); got != "app-123" {
		t.Fatalf("101'de X-Request-Id yankilanmali: %q", got)
	}
	deadline := time.Now().Add(3 * time.Second)
	for len(h.ReqLog.List(0, "")) == 0 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	l := h.ReqLog.List(0, "")
	if len(l) != 1 || l[0].ID != zid {
		t.Fatalf("istek logu kimligi eslesmeli: %+v (beklenen %s)", l, zid)
	}
}

// Kume aktarimi: kimligi kenar dugum uretip loglar; hedef dugum ayni degeri
// benimser. Hedefin kendi X-Request-Id'si (kanonik olmayan anahtar) ezilmez,
// hedefin X-Zorven-Request-Id'si atilir.
func TestWSRequestIDCluster(t *testing.T) {
	sess, opens := wsIDAgent(t, map[string][]string{
		"Upgrade": {"websocket"}, "x-request-id": {"backend-1"}, "x-zorven-request-id": {"hedef"},
	})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		(&Handler{}).ServeClusterWS(w, r, sess, "tun", "ten")
	}))
	t.Cleanup(srv.Close)

	resp := wsHandshake(t, srv.URL, "X-Zorven-Request-Id: req_00aa11bb22cc33dd\r\nX-Request-Id: req_00aa11bb22cc33dd\r\n")
	op := <-opens
	if got := http.Header(op.Headers).Get(headerZorvenRequestID); got != "req_00aa11bb22cc33dd" {
		t.Fatalf("kume kimligi benimsenmedi: %q", got)
	}
	if got := resp.Header.Values(headerZorvenRequestID); len(got) != 1 || got[0] != "req_00aa11bb22cc33dd" {
		t.Fatalf("101 kimligi: %v", got)
	}
	if got := resp.Header.Values("X-Request-Id"); len(got) != 1 || got[0] != "backend-1" {
		t.Fatalf("hedefin X-Request-Id'si korunmali: %v", got)
	}
}

// Hijack oncesi hata (ajan akisi acamadi) yanitinda da kimlik bulunur.
func TestWSRequestIDOnError(t *testing.T) {
	sess, _ := wsIDAgent(t, nil)
	_ = sess.Close("bitti")
	time.Sleep(50 * time.Millisecond)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		(&Handler{}).serveWebSocket(w, r, sess, "tun", "ten")
	}))
	t.Cleanup(srv.Close)
	resp := wsHandshake(t, srv.URL, "")
	if resp.StatusCode == http.StatusSwitchingProtocols {
		t.Fatal("kapali oturumda 101 beklenmiyordu")
	}
	if !strings.HasPrefix(resp.Header.Get(headerZorvenRequestID), "req_") {
		t.Fatalf("hata yanitinda kimlik yok: %v", resp.Header)
	}
}
