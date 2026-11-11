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
	"sync"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/tkodcumpeg4/zorven/server/tunnel"
	"github.com/tkodcumpeg4/zorven/shared/protocol"
)

// wsFlowAgent, gercek *tunnel.Session karsisinda WS akisini kabul edip veri
// basan sahte ajan.
type wsFlowAgent struct {
	c      *websocket.Conn
	sess   *tunnel.Session
	credit bool
	wmu    sync.Mutex
	closed chan uint64 // ajana gelen ws_close

	cmu   sync.Mutex // tek akisli testler icin tek pencere yeterli
	cond  *sync.Cond
	avail int
}

func newWSFlowAgent(t *testing.T, credit bool) *wsFlowAgent {
	t.Helper()
	ready := make(chan *tunnel.Session, 1)
	ctx, cancel := context.WithCancel(context.Background())
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c, err := websocket.Accept(w, r, nil)
		if err != nil {
			return
		}
		c.SetReadLimit(protocol.WSReadLimit)
		s := tunnel.NewSession("s1", "cli", "c", "127.0.0.1:1", c, slog.New(slog.NewTextHandler(io.Discard, nil)))
		ready <- s
		_ = s.Run(ctx)
	}))
	ac, _, err := websocket.Dial(ctx, "ws"+strings.TrimPrefix(srv.URL, "http"), nil)
	if err != nil {
		t.Fatal(err)
	}
	ac.SetReadLimit(protocol.WSReadLimit)
	a := &wsFlowAgent{c: ac, sess: <-ready, credit: credit, closed: make(chan uint64, 16), avail: protocol.InitialWindowBytes}
	a.cond = sync.NewCond(&a.cmu)
	t.Cleanup(func() { cancel(); ac.CloseNow(); srv.Close() })
	go a.loop(ctx)
	return a
}

func (a *wsFlowAgent) write(typ websocket.MessageType, b []byte) error {
	a.wmu.Lock()
	defer a.wmu.Unlock()
	return a.c.Write(context.Background(), typ, b)
}

func (a *wsFlowAgent) loop(ctx context.Context) {
	for {
		typ, data, err := a.c.Read(ctx)
		if err != nil || typ != websocket.MessageText {
			if err != nil {
				return
			}
			continue
		}
		switch pt, _ := protocol.PeekType(data); pt {
		case protocol.TypeWindowUpdate:
			var wu protocol.WindowUpdate
			_ = json.Unmarshal(data, &wu)
			a.cmu.Lock()
			a.avail += wu.Increment
			a.cmu.Unlock()
			a.cond.Broadcast()
		case protocol.TypeWSClose:
			var m protocol.WSClose
			_ = json.Unmarshal(data, &m)
			a.closed <- m.ReqID
			a.cmu.Lock()
			a.avail = 1 << 40 // bekleyen flood'u birak (yazim hata verip biter)
			a.cmu.Unlock()
			a.cond.Broadcast()
		case protocol.TypeWSOpen:
			var m protocol.WSOpen
			_ = json.Unmarshal(data, &m)
			var caps []string
			if a.credit {
				caps = []string{protocol.StreamCapCredit}
			}
			b, _ := protocol.Marshal(protocol.WSAccept{Type: protocol.TypeWSAccept, ReqID: m.ReqID,
				Status: http.StatusSwitchingProtocols, Headers: map[string][]string{"Upgrade": {"websocket"}}, Caps: caps}, protocol.TypeWSAccept)
			_ = a.write(websocket.MessageText, b)
			go a.flood(m.ReqID)
		}
	}
}

// flood, yerel WS servisinin surekli veri basmasini taklit eder. Kredi modunda
// gercek ajan gibi pencere bitince WindowUpdate bekler (bkz. loop).
func (a *wsFlowAgent) flood(id uint64) {
	chunk := make([]byte, 32<<10)
	cost := len(chunk) + protocol.StreamFrameCost
	for i := 0; i < 2000; i++ { // en fazla ~64 MB
		if a.credit {
			a.cmu.Lock()
			for a.avail < cost {
				a.cond.Wait()
			}
			a.avail -= cost
			a.cmu.Unlock()
		} else if i >= 320 { // eski ajan: 10 MB
			return
		}
		f := protocol.BodyFrame{FrameType: protocol.FrameWSData, ReqID: id, Payload: chunk}
		if a.write(websocket.MessageBinary, f.Encode()) != nil {
			return
		}
	}
}

// dialNonReader, WS yukseltmesini yapip 101'i okur, sonra HIC okumaz. Alma
// tamponu kucuk tutulur ki sunucunun yazimi cabuk bloklansin.
func dialNonReader(t *testing.T, url string) net.Conn {
	t.Helper()
	d := net.Dialer{Control: func(_, _ string, rc syscall.RawConn) error {
		return rc.Control(func(fd uintptr) { _ = syscall.SetsockoptInt(int(fd), syscall.SOL_SOCKET, syscall.SO_RCVBUF, 4096) })
	}}
	c, err := d.Dial("tcp", strings.TrimPrefix(url, "http://"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { c.Close() })
	_, _ = c.Write([]byte("GET /ws HTTP/1.1\r\nHost: x\r\nUpgrade: websocket\r\nConnection: Upgrade\r\n\r\n"))
	_ = c.SetReadDeadline(time.Now().Add(5 * time.Second))
	resp, err := http.ReadResponse(bufio.NewReaderSize(c, 64), nil)
	if err != nil || resp.StatusCode != http.StatusSwitchingProtocols {
		t.Fatalf("101 bekleniyordu: %v %v", resp, err)
	}
	return c
}

func wsIngress(t *testing.T, a *wsFlowAgent) string {
	t.Helper()
	h := &Handler{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h.ServeClusterWS(w, r, a.sess, "tun", "ten")
	}))
	t.Cleanup(srv.Close)
	return srv.URL
}

func waitWSClose(t *testing.T, a *wsFlowAgent, what string) {
	t.Helper()
	select {
	case <-a.closed:
	case <-time.After(10 * time.Second):
		t.Fatal(what)
	}
}

// Y1 (eski ajan): okumayan tarayici varken yerel servis veri basarsa sunucuda
// akis basina en fazla ~2 MB birikir, akis kesilir ve bloklu yazim cozulur.
// Eskiden 20 akis 1.06 GB heap tutuyordu (akis basina ~32 MB).
func TestWSNonReadingBrowserLegacyBounded(t *testing.T) {
	a := newWSFlowAgent(t, false)
	url := wsIngress(t, a)
	for i := 0; i < 5; i++ {
		dialNonReader(t, url)
	}
	for i := 0; i < 5; i++ {
		waitWSClose(t, a, "tavan asilinca akis kapanmali (ajana ws_close)")
	}
	waitForCond(t, "oturum bellegi bosalmali", func() bool { return a.sess.StreamMemBytes() == 0 })
}

// Y1 (yeni ajan): kredi ile bellek pencerede kalir; tarayici wsWriteStall
// boyunca okumazsa akis kapanir.
func TestWSNonReadingBrowserCreditWriteStall(t *testing.T) {
	old := wsWriteStall
	wsWriteStall = 300 * time.Millisecond
	t.Cleanup(func() { wsWriteStall = old })
	a := newWSFlowAgent(t, true)
	url := wsIngress(t, a)
	dialNonReader(t, url)
	var peak atomic.Int64
	stop := make(chan struct{})
	go func() {
		for {
			select {
			case <-stop:
				return
			case <-time.After(time.Millisecond):
				if v := a.sess.StreamMemBytes(); v > peak.Load() {
					peak.Store(v)
				}
			}
		}
	}()
	waitWSClose(t, a, "yazim ilerlemezse akis kapanmali")
	close(stop)
	// Pencere 2 MB (len+maliyet); bellek cap() ile sayilir, payla 4 MB.
	if p := peak.Load(); p > 2*protocol.InitialWindowBytes {
		t.Fatalf("kredi modunda bellek pencereyi asti: %d", p)
	}
	waitForCond(t, "oturum bellegi bosalmali", func() bool { return a.sess.StreamMemBytes() == 0 })
}

func waitForCond(t *testing.T, what string, cond func() bool) {
	t.Helper()
	for i := 0; i < 500; i++ {
		if cond() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("zaman asimi: %s", what)
}
