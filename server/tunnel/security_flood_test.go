package tunnel

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
)

func TestSecurity_MaxPendingExchanges_Enforcement(t *testing.T) {
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	sess := NewSession("ses_test", "cli_test", "Test Client", "127.0.0.1:1234", nil, log)

	// 1. Allocate up to MaxPendingExchanges
	for i := 0; i < MaxPendingExchanges; i++ {
		e, err := sess.newExchange()
		if err != nil {
			t.Fatalf("expected exchange %d to succeed, got error: %v", i, err)
		}
		if e == nil {
			t.Fatalf("exchange %d was nil", i)
		}
	}

	// 2. The 513th request MUST fail with ErrTooManyRequests
	_, err := sess.newExchange()
	if err != ErrTooManyRequests {
		t.Fatalf("expected ErrTooManyRequests when exceeding limit, got: %v", err)
	}

	// 3. Releasing an exchange allows a new one to be allocated
	sess.FinishExchange(1)

	e, err := sess.newExchange()
	if err != nil {
		t.Fatalf("expected new exchange after release to succeed, got error: %v", err)
	}
	if e == nil {
		t.Fatal("expected non-nil exchange")
	}
}

func TestSecurity_BadFrame_Flooding_Disconnect(t *testing.T) {
	log := slog.New(slog.NewTextHandler(io.Discard, nil))

	// Set up WebSocket echo/server
	serverCh := make(chan error, 1)
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := websocket.Accept(w, r, &websocket.AcceptOptions{InsecureSkipVerify: true})
		if err != nil {
			return
		}
		sess := NewSession("ses_flood", "cli_flood", "Flood Client", r.RemoteAddr, conn, log)
		serverCh <- sess.Run(r.Context())
	}))
	defer s.Close()

	wsURL := "ws" + strings.TrimPrefix(s.URL, "http")
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	clientConn, _, err := websocket.Dial(ctx, wsURL, nil)
	if err != nil {
		t.Fatalf("websocket dial failed: %v", err)
	}
	defer clientConn.CloseNow()

	// Send 5 consecutive invalid JSON control frames
	for i := 0; i < 5; i++ {
		err := clientConn.Write(ctx, websocket.MessageText, []byte(fmt.Sprintf("{invalid_json_%d", i)))
		if err != nil {
			break // Connection already closed
		}
	}

	// The server should terminate the connection with StatusPolicyViolation
	select {
	case err := <-serverCh:
		if err == nil {
			t.Fatal("expected error due to bad frames, got nil")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timeout waiting for server to disconnect bad client")
	}
}
