package rawproxy

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/tkodcumpeg4/zorven/server/tunnel"
	"github.com/tkodcumpeg4/zorven/shared/protocol"
)

// --- sahte ajanli gercek oturum ---------------------------------------------

// fakeAgent, gercek *tunnel.Session'in karsisinda protokolu elle konusur.
// mode: "writeclose" (n bayt yaz + StreamClose), "echo" (yanki + yari kapama).
type fakeAgent struct {
	t      *testing.T
	c      *websocket.Conn
	sess   *tunnel.Session
	caps   []string // StreamAck'te kabul edilen yetenekler
	mode   string
	n      int
	wmu    sync.Mutex
	cancel context.CancelFunc
}

func newFakeAgentSession(t *testing.T, mode string, n int, caps []string) *fakeAgent {
	t.Helper()
	ready := make(chan *tunnel.Session, 1)
	ctx, cancel := context.WithCancel(context.Background())
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c, err := websocket.Accept(w, r, nil)
		if err != nil {
			return
		}
		c.SetReadLimit(protocol.WSReadLimit)
		s := tunnel.NewSession("s1", "cli", "c", "127.0.0.1:1", c, quietLog())
		ready <- s
		_ = s.Run(ctx)
	}))
	ac, _, err := websocket.Dial(ctx, "ws"+strings.TrimPrefix(srv.URL, "http"), nil)
	if err != nil {
		t.Fatal(err)
	}
	ac.SetReadLimit(protocol.WSReadLimit)
	a := &fakeAgent{t: t, c: ac, sess: <-ready, caps: caps, mode: mode, n: n, cancel: cancel}
	t.Cleanup(func() { cancel(); ac.CloseNow(); srv.Close() })
	go a.loop(ctx)
	return a
}

func (a *fakeAgent) ctl(v any, ty protocol.MessageType) {
	b, _ := protocol.Marshal(v, ty)
	a.wmu.Lock()
	defer a.wmu.Unlock()
	_ = a.c.Write(context.Background(), websocket.MessageText, b)
}

func (a *fakeAgent) data(id uint64, p []byte, eof bool) {
	f := protocol.BodyFrame{FrameType: protocol.FrameStreamData, ReqID: id, Payload: p, EOF: eof}
	a.wmu.Lock()
	defer a.wmu.Unlock()
	_ = a.c.Write(context.Background(), websocket.MessageBinary, f.Encode())
}

func (a *fakeAgent) loop(ctx context.Context) {
	for {
		typ, data, err := a.c.Read(ctx)
		if err != nil {
			return
		}
		if typ == websocket.MessageBinary {
			f, err := protocol.DecodeBodyFrame(data)
			if err != nil || a.mode != "echo" {
				continue
			}
			// Kredi iadesi (sunucunun Send penceresi) + yanki.
			if protocol.HasCap(a.caps, protocol.StreamCapCredit) && len(f.Payload) > 0 {
				a.ctl(protocol.WindowUpdate{Type: protocol.TypeWindowUpdate, ReqID: f.ReqID, Increment: len(f.Payload) + protocol.StreamFrameCost}, protocol.TypeWindowUpdate)
			}
			if len(f.Payload) == 0 {
				a.data(f.ReqID, nil, true) // ziyaretci EOF -> hedef de yanitini bitirdi
				continue
			}
			a.data(f.ReqID, append([]byte(nil), f.Payload...), false)
			continue
		}
		if pt, _ := protocol.PeekType(data); pt != protocol.TypeStreamOpen {
			continue
		}
		var open protocol.StreamOpen
		_ = json.Unmarshal(data, &open)
		a.ctl(protocol.StreamAck{Type: protocol.TypeStreamAck, ReqID: open.ReqID, Caps: a.caps}, protocol.TypeStreamAck)
		if a.mode == "writeclose" {
			go func(id uint64) {
				// Kredi modunda gercek ajan pencereyi bekler; burada pencereyi
				// asmamak icin eski ajan gibi davranip (caps yok) hizla yazariz.
				buf := make([]byte, 32<<10)
				for left := a.n; left > 0; {
					k := min(left, len(buf))
					a.data(id, buf[:k], false)
					left -= k
				}
				a.ctl(protocol.StreamClose{Type: protocol.TypeStreamClose, ReqID: id}, protocol.TypeStreamClose)
			}(open.ReqID)
		}
	}
}

func rawMgrWithSession(t *testing.T, a *fakeAgent) string {
	t.Helper()
	_, addr := rawTestMgr(t, nil, func(m *Manager) {
		m.openStream = func(ctx context.Context, ti TunnelInfo, remote string) (bridgeStream, bool, error) {
			rs, err := a.sess.OpenStreamHalf(ctx, ti.TunnelID, "tcp", remote)
			return rs, false, err
		}
	})
	return addr
}

// Y2 regresyonu: hedef 5 MB yazip kapatinca ziyaretci baytlarin TAMAMINI almali
// (eskiden 10 denemenin 5'i eksik geliyordu: Closed, kuyruktaki veriden once seciliyordu).
func TestRawWriteCloseDeliversAll(t *testing.T) {
	const size = 5 << 20
	a := newFakeAgentSession(t, "writeclose", size, nil) // eski ajan: kredisiz hizli yazim
	addr := rawMgrWithSession(t, a)
	for i := 0; i < 20; i++ {
		c, err := net.Dial("tcp", addr)
		if err != nil {
			t.Fatal(err)
		}
		_ = c.SetDeadline(time.Now().Add(10 * time.Second))
		n, err := io.Copy(io.Discard, c)
		c.Close()
		if err != nil || n != size {
			t.Fatalf("deneme %d: %d/%d bayt (hata %v)", i, n, size, err)
		}
	}
}

// O12 regresyonu: ziyaretci yazmayi kapatinca (CloseWrite) akis yikilmamali;
// yanki hedefinden 1 MB'in tamami donmeli, sonra EOF gelmeli.
func TestRawHalfCloseEcho(t *testing.T) {
	a := newFakeAgentSession(t, "echo", 0, []string{protocol.StreamCapCredit, protocol.StreamCapHalfClose})
	addr := rawMgrWithSession(t, a)
	want := bytes.Repeat([]byte("0123456789abcdef"), 1<<16) // 1 MB
	for i := 0; i < 5; i++ {
		c, err := net.Dial("tcp", addr)
		if err != nil {
			t.Fatal(err)
		}
		_ = c.SetDeadline(time.Now().Add(10 * time.Second))
		go func() {
			_, _ = c.Write(want)
			_ = c.(*net.TCPConn).CloseWrite()
		}()
		got, err := io.ReadAll(c)
		c.Close()
		if err != nil || !bytes.Equal(got, want) {
			t.Fatalf("deneme %d: %d/%d bayt (hata %v)", i, len(got), len(want), err)
		}
	}
}
