package tunnel

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"

	"github.com/tkodcumpeg4/zorven/server/store"
	"github.com/tkodcumpeg4/zorven/shared/protocol"
)

// versionStore, handshake'in dokundugu cagrilari yakalayan sahte depo.
// Digerleri (gomulu nil arayuz) cagrilirsa panikler: test yalnizca bilinen yolu kullanir.
type versionStore struct {
	store.Store
	info store.DeviceInfo
}

func (f *versionStore) UpdateDeviceInfo(_ context.Context, _ string, d store.DeviceInfo) error {
	f.info = d
	return nil
}
func (f *versionStore) ListTunnelsByClient(context.Context, string) ([]store.Tunnel, error) {
	return nil, nil
}
func (f *versionStore) ListHostnamesByClient(context.Context, string) ([]store.Hostname, error) {
	return nil, nil
}
func (f *versionStore) GetDeviceConfigByClient(context.Context, string) (*protocol.AgentSettings, error) {
	return nil, nil
}

func runHello(t *testing.T, hello protocol.Hello) (*Session, *versionStore) {
	t.Helper()
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	fs := &versionStore{}
	h := &Handler{Store: fs, Log: log}
	done := make(chan *Session, 1)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := websocket.Accept(w, r, nil)
		if err != nil {
			return
		}
		s := NewSession("ses_v", "cli_v", "v", r.RemoteAddr, conn, log)
		if err := h.handshake(r.Context(), s); err != nil {
			t.Errorf("handshake: %v", err)
		}
		done <- s
	}))
	defer srv.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	c, _, err := websocket.Dial(ctx, "ws"+strings.TrimPrefix(srv.URL, "http"), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer c.CloseNow()
	b, err := protocol.Marshal(hello, protocol.TypeHello)
	if err != nil {
		t.Fatal(err)
	}
	if err := c.Write(ctx, websocket.MessageText, b); err != nil {
		t.Fatal(err)
	}
	if _, _, err := c.Read(ctx); err != nil { // hello_ack
		t.Fatal(err)
	}
	select {
	case s := <-done:
		return s, fs
	case <-ctx.Done():
		t.Fatal("zaman asimi")
		return nil, nil
	}
}

func TestHandshakeStoresRealClientVersion(t *testing.T) {
	s, fs := runHello(t, protocol.Hello{
		Type: protocol.TypeHello, ClientVersion: "0.2.2",
		ProtocolVersion: protocol.Version, AppKind: "desktop", Platform: "windows/amd64",
	})
	if s.Version != "0.2.2" {
		t.Fatalf("session version=%q, beklenen 0.2.2 (protokol %s degil)", s.Version, protocol.Version)
	}
	if s.AppKind != "desktop" {
		t.Fatalf("app kind=%q", s.AppKind)
	}
	if fs.info.AgentVersion != "0.2.2" {
		t.Fatalf("kalici agent_version=%q", fs.info.AgentVersion)
	}
}

// Eski istemciler yeni alanlari gondermez ve protokol surumunu client_version'da tasir.
func TestHandshakeOldClientStillAccepted(t *testing.T) {
	s, _ := runHello(t, protocol.Hello{
		Type: protocol.TypeHello, ClientVersion: "0.1.0", Platform: "linux/amd64",
	})
	if s.Version != "0.1.0" || s.AppKind != "" {
		t.Fatalf("version=%q kind=%q", s.Version, s.AppKind)
	}
}
