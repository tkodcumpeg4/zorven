package agent

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/tkodcumpeg4/zorven/shared/protocol"
)

// closingServer: el sikismadan sonra baglantiyi WebSocket kapanis koduyla
// kapatan sahte sunucu; her baglanti anini kaydeder.
func closingServer(t *testing.T, code websocket.StatusCode) (*httptest.Server, func() []time.Time) {
	var mu sync.Mutex
	var at []time.Time
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c, err := websocket.Accept(w, r, nil)
		if err != nil {
			return
		}
		defer c.CloseNow()
		if _, _, err := c.Read(r.Context()); err != nil { // hello
			return
		}
		ack, _ := protocol.Marshal(protocol.HelloAck{Type: protocol.TypeHelloAck, ClientID: "c1", SessionID: "s", HeartbeatIntervalS: 30}, protocol.TypeHelloAck)
		if err := c.Write(r.Context(), websocket.MessageText, ack); err != nil {
			return
		}
		mu.Lock()
		at = append(at, time.Now())
		mu.Unlock()
		time.Sleep(50 * time.Millisecond)
		_ = c.Close(code, "server shutting down")
	}))
	t.Cleanup(srv.Close)
	return srv, func() []time.Time {
		mu.Lock()
		defer mu.Unlock()
		return append([]time.Time(nil), at...)
	}
}

func runAgentAgainst(t *testing.T, srv *httptest.Server, d time.Duration) {
	a := &Agent{ServerAddr: strings.TrimPrefix(srv.URL, "http://"), Insecure: true, Token: "t", NoAutoUpdate: true,
		NoNet: true, NoPosture: true, Log: slog.New(slog.NewTextHandler(io.Discard, nil))}
	ctx, cancel := context.WithTimeout(context.Background(), d)
	defer cancel()
	_ = a.Run(ctx)
}

// A3 #11: sunucu duzgun kapaninca (1000, yeniden baslatma) ajan 1-3 sn'lik
// jitter'li beklemeyle doner ve kisa oturumlara ragmen geri cekilme buyumez.
// Eskiden bekleme 1, 2, 4, 8... sn diye buyuyordu.
func TestReconnectAfterServerNormalClose(t *testing.T) {
	srv, times := closingServer(t, websocket.StatusNormalClosure)
	runAgentAgainst(t, srv, 9*time.Second)
	at := times()
	if len(at) < 4 {
		t.Fatalf("9 sn'de en az 4 baglanti beklenirdi, %d", len(at))
	}
	for i := 1; i < len(at); i++ {
		gap := at[i].Sub(at[i-1])
		if gap < 900*time.Millisecond || gap > 3300*time.Millisecond {
			t.Fatalf("baglanti %d: aralik %s (1-3 sn bekleniyordu)", i, gap)
		}
	}
}

// Kontrol: duzgun kapanis disindaki kopusta mevcut ussel geri cekilme surer.
func TestReconnectBackoffGrowsOnAbnormalClose(t *testing.T) {
	srv, times := closingServer(t, websocket.StatusInternalError)
	runAgentAgainst(t, srv, 8*time.Second)
	at := times()
	if len(at) < 3 {
		t.Fatalf("en az 3 baglanti beklenirdi, %d", len(at))
	}
	if g1, g2 := at[1].Sub(at[0]), at[2].Sub(at[1]); g2 < g1+500*time.Millisecond {
		t.Fatalf("geri cekilme buyumeliydi: %s sonra %s", g1, g2)
	}
}

func TestServerRestartWaitJitter(t *testing.T) {
	lo, hi := time.Hour, time.Duration(0)
	for i := 0; i < 2000; i++ {
		w := serverRestartWait()
		lo, hi = min(lo, w), max(hi, w)
	}
	if lo < time.Second || hi >= 3*time.Second || hi-lo < 1500*time.Millisecond {
		t.Fatalf("jitter araligi [%s, %s]", lo, hi)
	}
}

// A1 #14: uyanma algisi, uyanmadan SONRA kurulmus baglantiyi kapatmamali;
// duraksamadan once kurulmus baglantiyi kapatmali.
func TestOnWakeKeepsFreshConnection(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c, err := websocket.Accept(w, r, nil)
		if err != nil {
			return
		}
		defer c.CloseNow()
		for {
			if _, _, err := c.Read(context.Background()); err != nil {
				return
			}
		}
	}))
	defer srv.Close()
	dial := func() *websocket.Conn {
		c, _, err := websocket.Dial(context.Background(), "ws"+strings.TrimPrefix(srv.URL, "http"), nil)
		if err != nil {
			t.Fatal(err)
		}
		return c
	}
	a := &Agent{Log: slog.New(slog.NewTextHandler(io.Discard, nil)), wakeCh: make(chan struct{}, 1)}
	pausedBy := time.Now()

	fresh := dial()
	defer fresh.CloseNow()
	a.curConn, a.curConnAt = fresh, time.Now()
	a.onWake(pausedBy, 30*time.Second)
	if a.netChanged.Load() {
		t.Fatal("taze baglantida yenileme isaretlenmemeli")
	}
	if err := fresh.Write(ctxT(t), websocket.MessageText, []byte("x")); err != nil {
		t.Fatalf("taze baglanti kapatilmamaliydi: %v", err)
	}

	stale := dial()
	a.curConn, a.curConnAt = stale, pausedBy.Add(-10*time.Second)
	a.onWake(pausedBy, 30*time.Second)
	if !a.netChanged.Load() {
		t.Fatal("eski baglantida yenileme isaretlenmeli")
	}
	if err := stale.Write(ctxT(t), websocket.MessageText, []byte("x")); err == nil {
		t.Fatal("duraksamadan once kurulan baglanti kapatilmaliydi")
	}
}

func ctxT(t *testing.T) context.Context {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	t.Cleanup(cancel)
	return ctx
}
