package tunnel

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/coder/websocket"
)

func mkSess(id, client string) *Session {
	return &Session{ID: id, ClientID: client}
}

func TestHubTakeoverReplacesOld(t *testing.T) {
	h := NewHub()
	a, b := mkSess("a", "c1"), mkSess("b", "c1")
	if old, err := h.Takeover(a); err != nil || old != nil {
		t.Fatalf("ilk kayit: old=%v err=%v", old, err)
	}
	old, err := h.Takeover(b)
	if err != nil || old != a {
		t.Fatalf("devralma: old=%v err=%v", old, err)
	}
	if cur, _ := h.Get("c1"); cur != b {
		t.Fatalf("kayitli oturum yeni olmali")
	}
}

// Eski oturumun gec kalan Unregister'i yeni kaydi dusurmemeli ve false donmeli.
func TestHubUnregisterAfterTakeoverKeepsNew(t *testing.T) {
	h := NewHub()
	a, b := mkSess("a", "c1"), mkSess("b", "c1")
	_, _ = h.Takeover(a)
	_, _ = h.Takeover(b)
	if h.Unregister(a) {
		t.Fatal("devralinan oturum current sayilmamali")
	}
	if cur, ok := h.Get("c1"); !ok || cur != b {
		t.Fatal("yeni kayit silinmemeli")
	}
	if !h.Unregister(b) {
		t.Fatal("guncel oturum Unregister true donmeli")
	}
	if _, ok := h.Get("c1"); ok {
		t.Fatal("kayit silinmeli")
	}
	// Ayni ID'li ama farkli nesne (kimlik cakismasi) guncel kaydi dusuremez.
	c := mkSess("b", "c1")
	_, _ = h.Takeover(c)
	if h.Unregister(b) {
		t.Fatal("ayni ID, farkli nesne current olmamali")
	}
}

func TestHubTakeoverPingPongThrottle(t *testing.T) {
	h := NewHub()
	now := time.Unix(1000, 0)
	h.now = func() time.Time { return now }
	prev := mkSess("s0", "c1")
	_, _ = h.Takeover(prev)
	for i := 1; i <= takeoverMax; i++ {
		s := mkSess("s"+string(rune('0'+i)), "c1")
		if _, err := h.Takeover(s); err != nil {
			t.Fatalf("devralma %d reddedildi: %v", i, err)
		}
		prev = s
	}
	if !h.TakeoverBlocked("c1") {
		t.Fatal("esik asilinca engellenmeli")
	}
	if _, err := h.Takeover(mkSess("x", "c1")); !errors.Is(err, ErrTakeoverThrottled) {
		t.Fatalf("beklenen ErrTakeoverThrottled, alinan %v", err)
	}
	if cur, _ := h.Get("c1"); cur != prev {
		t.Fatal("reddedilen devralma kaydi degistirmemeli")
	}
	now = now.Add(takeoverWindow + time.Second)
	if h.TakeoverBlocked("c1") {
		t.Fatal("pencere dolunca engel kalkmali")
	}
	if _, err := h.Takeover(mkSess("y", "c1")); err != nil {
		t.Fatalf("pencere sonrasi devralma: %v", err)
	}
}

func TestSessionEvictClosesConn(t *testing.T) {
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	sessCh := make(chan *Session, 1)
	done := make(chan error, 1)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := websocket.Accept(w, r, nil)
		if err != nil {
			return
		}
		s := NewSession("ses_e", "cli_e", "e", r.RemoteAddr, conn, log)
		sessCh <- s
		done <- s.Run(r.Context())
	}))
	defer srv.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	c, _, err := websocket.Dial(ctx, "ws"+srv.URL[len("http"):], nil)
	if err != nil {
		t.Fatal(err)
	}
	defer c.CloseNow()
	s := <-sessCh
	s.Evict("replaced")
	select {
	case <-done:
	case <-ctx.Done():
		t.Fatal("Evict sonrasi Run donmedi")
	}
}
