package netconnect

import (
	"context"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
)

// fakeServer, Zorven'in /api/v1/network/connect ucunu taklit eder:
// token ve hedefi dogrular, basarida gelen veriyi aynen geri yollar (echo).
func fakeServer(t *testing.T, gotAuth *string) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		*gotAuth = r.Header.Get("Authorization")
		switch r.URL.Query().Get("target") {
		case "yok.internal:5432":
			http.Error(w, "yok", http.StatusNotFound)
			return
		case "db.internal:5432":
		default:
			http.Error(w, "kotu", http.StatusBadRequest)
			return
		}
		c, err := websocket.Accept(w, r, nil)
		if err != nil {
			return
		}
		nc := websocket.NetConn(r.Context(), c, websocket.MessageBinary)
		defer nc.Close()
		_, _ = io.Copy(nc, nc)
	}))
}

func startSocks(t *testing.T, srvURL string) (addr string, stop func()) {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr = ln.Addr().String()
	ln.Close()
	s := &Server{
		ServerAddr: strings.TrimPrefix(srvURL, "http://"),
		Token:      "zrv_api_test",
		Insecure:   true,
		Listen:     addr,
		Log:        slog.New(slog.NewTextHandler(io.Discard, nil)),
	}
	ctx, cancel := context.WithCancel(context.Background())
	go func() { _ = s.ListenAndServe(ctx) }()
	// Dinleyici hazir olana kadar bekle.
	for i := 0; i < 50; i++ {
		if c, err := net.Dial("tcp", addr); err == nil {
			c.Close()
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	return addr, cancel
}

// socksConnect, elle yazilmis minimal bir SOCKS5 istemcisi; yanit kodunu doner.
func socksConnect(t *testing.T, addr, host string, port uint16) (net.Conn, byte) {
	t.Helper()
	c, err := net.Dial("tcp", addr)
	if err != nil {
		t.Fatal(err)
	}
	_ = c.SetDeadline(time.Now().Add(10 * time.Second))
	if _, err := c.Write([]byte{0x05, 0x01, 0x00}); err != nil {
		t.Fatal(err)
	}
	greet := make([]byte, 2)
	if _, err := io.ReadFull(c, greet); err != nil || greet[1] != 0x00 {
		t.Fatalf("selamlasma = %x, %v", greet, err)
	}
	req := []byte{0x05, 0x01, 0x00, 0x03, byte(len(host))}
	req = append(req, host...)
	req = append(req, byte(port>>8), byte(port))
	if _, err := c.Write(req); err != nil {
		t.Fatal(err)
	}
	rep := make([]byte, 10)
	if _, err := io.ReadFull(c, rep); err != nil {
		t.Fatalf("yanit okunamadi: %v", err)
	}
	return c, rep[1]
}

// Uctan uca: SOCKS5 -> WSS -> echo -> geri.
func TestEndToEndEcho(t *testing.T) {
	var auth string
	srv := fakeServer(t, &auth)
	defer srv.Close()
	addr, stop := startSocks(t, srv.URL)
	defer stop()

	c, rep := socksConnect(t, addr, "db.internal", 5432)
	defer c.Close()
	if rep != RepSucceeded {
		t.Fatalf("yanit kodu = %#x", rep)
	}
	if auth != "Bearer zrv_api_test" {
		t.Errorf("Authorization = %q; token basliga konmali", auth)
	}
	msg := []byte("SELECT 1;")
	if _, err := c.Write(msg); err != nil {
		t.Fatal(err)
	}
	got := make([]byte, len(msg))
	if _, err := io.ReadFull(c, got); err != nil {
		t.Fatal(err)
	}
	if string(got) != string(msg) {
		t.Errorf("echo = %q", got)
	}
}

// Sunucu 404 derse istemciye "host unreachable" (0x04) gider, baglanti acilmaz.
func TestNotFoundMapsToHostUnreachable(t *testing.T) {
	var auth string
	srv := fakeServer(t, &auth)
	defer srv.Close()
	addr, stop := startSocks(t, srv.URL)
	defer stop()

	c, rep := socksConnect(t, addr, "yok.internal", 5432)
	defer c.Close()
	if rep != RepHostUnreachable {
		t.Errorf("yanit kodu = %#x, 0x04 beklenirdi", rep)
	}
}

// Token asla sorgu dizisine konmamali (erisim loglarina sizar).
func TestTokenNotInQuery(t *testing.T) {
	var seenQuery string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seenQuery = r.URL.RawQuery
		http.Error(w, "yok", http.StatusNotFound)
	}))
	defer srv.Close()
	addr, stop := startSocks(t, srv.URL)
	defer stop()
	c, _ := socksConnect(t, addr, "db.internal", 5432)
	c.Close()
	if strings.Contains(seenQuery, "zrv_api_test") {
		t.Errorf("token sorguda: %q", seenQuery)
	}
}
