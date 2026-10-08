package agent

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"

	"github.com/tkodcumpeg4/zorven/shared/protocol"
)

// captureHello, sahte bir sunucuya baglanip gelen hello mesajini ve
// X-Tunnel-Client-Version basligini dondurur.
func captureHello(t *testing.T, a *Agent) (protocol.Hello, string) {
	t.Helper()
	type res struct {
		h   protocol.Hello
		hdr string
	}
	ch := make(chan res, 1)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
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
		ch <- res{h, r.Header.Get("X-Tunnel-Client-Version")}
		c.Close(websocket.StatusNormalClosure, "")
	}))
	defer srv.Close()

	a.ServerAddr = strings.TrimPrefix(srv.URL, "http://")
	a.Insecure = true
	a.Token = "t"
	a.NoAutoUpdate = true
	a.Log = slog.New(slog.NewTextHandler(io.Discard, nil))

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	go func() { _ = a.connectOnce(ctx) }()
	select {
	case r := <-ch:
		return r.h, r.hdr
	case <-ctx.Done():
		t.Fatal("hello alinamadi")
		return protocol.Hello{}, ""
	}
}

func TestHelloCarriesBinaryVersion(t *testing.T) {
	h, hdr := captureHello(t, &Agent{Version: "0.2.2", AppKind: "desktop"})
	if h.ClientVersion != "0.2.2" {
		t.Fatalf("client_version=%q, beklenen 0.2.2", h.ClientVersion)
	}
	if h.ProtocolVersion != protocol.Version {
		t.Fatalf("protocol_version=%q", h.ProtocolVersion)
	}
	if h.AppKind != "desktop" {
		t.Fatalf("app_kind=%q", h.AppKind)
	}
	// Baslik PROTOKOL surumu olarak kalir (sunucu uyumluluk kontrolu).
	if hdr != protocol.Version {
		t.Fatalf("X-Tunnel-Client-Version=%q, beklenen protokol %q", hdr, protocol.Version)
	}
}

func TestHelloVersionDefaultsToDev(t *testing.T) {
	h, _ := captureHello(t, &Agent{})
	if h.ClientVersion != "dev" {
		t.Fatalf("client_version=%q, beklenen dev", h.ClientVersion)
	}
}
