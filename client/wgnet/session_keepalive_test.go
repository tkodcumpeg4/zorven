package wgnet

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
)

// Pong donmeyen (okumayan) es: keepalive baglantiyi kapatmali.
func TestSessionKeepaliveClosesDeadPeer(t *testing.T) {
	release := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c, err := websocket.Accept(w, r, nil)
		if err != nil {
			return
		}
		defer c.CloseNow()
		<-release // hic okumaz => pong uretilmez
	}))
	defer srv.Close()
	defer close(release)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	c, _, err := websocket.Dial(ctx, "ws"+strings.TrimPrefix(srv.URL, "http"), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer c.CloseNow()

	go sessionKeepalive(ctx, c, 50*time.Millisecond, 200*time.Millisecond)
	readErr := make(chan error, 1)
	go func() { _, _, err := c.Read(ctx); readErr <- err }()
	select {
	case err := <-readErr:
		if err == nil {
			t.Fatal("hata bekleniyordu")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("olu es algilanmadi")
	}
}
