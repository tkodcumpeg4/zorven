package tunnel

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/tkodcumpeg4/zorven/server/obs"
	"github.com/tkodcumpeg4/zorven/shared/protocol"
)

type panicTestSess struct {
	s    *Session
	done chan error
	ac   *websocket.Conn
}

// Acik surum: hata enjeksiyonu cihaz guncellemesi kancasiyla (onDeviceUpdate) yapilir;
// ozel surumdeki dagitim (Zorven Apps) kancasi burada yok.
func startPanicTestSession(t *testing.T, name string, panics bool) *panicTestSess {
	t.Helper()
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	ready := make(chan *Session, 1)
	done := make(chan error, 1)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c, err := websocket.Accept(w, r, nil)
		if err != nil {
			return
		}
		s := NewSession("ses_"+name, "cli_"+name, name, r.RemoteAddr, c, log)
		if panics {
			s.deviceUpdate = true
			s.onDeviceUpdate = func(context.Context, protocol.DeviceUpdate) { panic("enjekte panik") }
		}
		ready <- s
		done <- s.Run(context.Background())
	}))
	ctx, cancel := context.WithCancel(context.Background())
	ac, _, err := websocket.Dial(ctx, "ws"+strings.TrimPrefix(srv.URL, "http"), nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { cancel(); ac.CloseNow(); srv.Close() })
	return &panicTestSess{s: <-ready, done: done, ac: ac}
}

// B2: bir oturumun kontrol isleyicisindeki panik yalniz o oturumu kapatir:
// surec yasar, bekleyen istek hata alir, panik sayilir;
// ayni suresteki baska bir oturum calismaya devam eder.
func TestSessionPanicClosesOnlyThatSession(t *testing.T) {
	before := obs.Panics("tunnel.session")
	a := startPanicTestSession(t, "a", true)
	b := startPanicTestSession(t, "b", false)

	e, err := a.s.newExchange()
	if err != nil {
		t.Fatal(err)
	}
	msg, _ := protocol.Marshal(protocol.DeviceUpdate{Type: protocol.TypeDeviceUpdate, Hostname: "x"}, protocol.TypeDeviceUpdate)
	if err := a.ac.Write(context.Background(), websocket.MessageText, msg); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-a.done:
		if !errors.Is(err, errSessionPanic) {
			t.Fatalf("Run hatasi: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("panikleyen oturum kapanmadi")
	}
	if obs.Panics("tunnel.session") != before+1 {
		t.Fatal("panik sayilmadi")
	}
	if a.s.PendingCount() != 0 {
		t.Fatalf("bekleyen istekler temizlenmedi: %d", a.s.PendingCount())
	}
	buf := make([]byte, 1)
	if _, err := e.Read(buf); err == nil {
		t.Fatal("bekleyen istek hata almaliydi")
	}

	// Diger oturum canli: sunucudan ajana mesaj gider, ajandan gelen pong islenir.
	if err := b.s.Send(context.Background(), protocol.Ping{Type: protocol.TypePing}, protocol.TypePing); err != nil {
		t.Fatalf("diger oturum yazamiyor: %v", err)
	}
	rctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if _, data, err := b.ac.Read(rctx); err != nil {
		t.Fatalf("diger oturum ajani okuyamadi: %v", err)
	} else if mt, _ := protocol.PeekType(data); mt != protocol.TypePing {
		t.Fatalf("beklenmeyen mesaj: %s", mt)
	}
	select {
	case err := <-b.done:
		t.Fatalf("diger oturum kapanmamaliydi: %v", err)
	default:
	}
}

// B2: akis pompasindaki panik (kredi iadesi) yalniz o akisi iptal eder ve
// oturum akis bellegini geri birakir (kilit next/take icinde defer ile acilir).
func TestStreamQueuePumpPanicAborts(t *testing.T) {
	before := obs.Panics("tunnel.streamq")
	var mem atomic.Int64
	q := newStreamQueue(&mem, 0, 0)
	q.credit.Store(true)
	q.grant = func(int) { panic("kredi iadesi bozuk") }
	chunk := 64 << 10
	n := windowUpdateThreshold/creditCost(chunk) + 2
	for i := 0; i < n; i++ {
		if err := q.push(make([]byte, chunk)); err != nil {
			t.Fatal(err)
		}
	}
	for {
		select {
		case <-q.out:
			continue
		case <-q.aborted:
		case <-time.After(5 * time.Second):
			t.Fatal("akis iptal edilmedi")
		}
		break
	}
	select {
	case <-q.closed:
	default:
		t.Fatal("akis kapanmadi")
	}
	if got := mem.Load(); got != 0 {
		t.Fatalf("oturum akis bellegi birakilmadi: %d", got)
	}
	if obs.Panics("tunnel.streamq") != before+1 {
		t.Fatal("panik sayilmadi")
	}
}
