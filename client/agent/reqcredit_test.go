package agent

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/tkodcumpeg4/zorven/shared/protocol"
)

// reqCreditRig: sahte sunucu (testin kendisi) + gercek clientSession + yavas
// tuketen yerel HTTP servisi.
type reqCreditRig struct {
	t     *testing.T
	srv   *websocket.Conn // sunucu tarafi
	cs    *clientSession
	wu    chan protocol.WindowUpdate
	order chan string // ajandan gelen mesajlarin tip sirasi
	resp  chan protocol.HTTPResponse
	body  chan []byte // yanit govdesi (EOF'a kadar)
}

func newReqCreditRig(t *testing.T, readDelay time.Duration) *reqCreditRig {
	t.Helper()
	local := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := sha256.New()
		buf := make([]byte, 32<<10)
		var n int64
		for {
			k, err := r.Body.Read(buf)
			h.Write(buf[:k])
			n += int64(k)
			if err != nil {
				break
			}
			time.Sleep(readDelay) // yavas tuketici
		}
		_, _ = io.WriteString(w, strconv.FormatInt(n, 10)+" "+hex.EncodeToString(h.Sum(nil)))
	}))
	t.Cleanup(local.Close)

	srvC := make(chan *websocket.Conn, 1)
	hs := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c, err := websocket.Accept(w, r, nil)
		if err != nil {
			return
		}
		c.SetReadLimit(protocol.WSReadLimit)
		srvC <- c
		<-r.Context().Done()
	}))
	t.Cleanup(hs.Close)
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	cc, _, err := websocket.Dial(ctx, "ws"+strings.TrimPrefix(hs.URL, "http"), nil)
	if err != nil {
		t.Fatal(err)
	}
	cc.SetReadLimit(protocol.WSReadLimit)
	g := &reqCreditRig{t: t, srv: <-srvC, wu: make(chan protocol.WindowUpdate, 1024),
		order: make(chan string, 1024), resp: make(chan protocol.HTTPResponse, 4), body: make(chan []byte, 4)}
	g.cs = newClientSession(cc, "", slog.New(slog.NewTextHandler(io.Discard, nil)))
	g.cs.flowControl = true
	g.cs.SetTargets([]protocol.TunnelSpec{{ID: "t1", Target: local.URL}})
	t.Cleanup(func() { cc.CloseNow(); g.srv.CloseNow() })

	// Ajanin okuma dongusu (conn.go'nun ilgili kismi).
	go func() {
		for {
			typ, data, err := cc.Read(ctx)
			if err != nil {
				return
			}
			if typ == websocket.MessageBinary {
				if f, err := protocol.DecodeBodyFrame(data); err == nil {
					g.cs.routeBody(f)
				}
				continue
			}
			switch pt, _ := protocol.PeekType(data); pt {
			case protocol.TypeHTTPRequest:
				var req protocol.HTTPRequest
				_ = json.Unmarshal(data, &req)
				g.cs.handleRequest(ctx, req)
			case protocol.TypeWindowUpdate:
				var wu protocol.WindowUpdate
				_ = json.Unmarshal(data, &wu)
				g.cs.windows.add(wu.ReqID, wu.Increment)
			}
		}
	}()
	// Sahte sunucunun okuyucusu.
	go func() {
		var acc []byte
		for {
			typ, data, err := g.srv.Read(context.Background())
			if err != nil {
				return
			}
			if typ == websocket.MessageBinary {
				f, err := protocol.DecodeBodyFrame(data)
				if err != nil || f.FrameType != protocol.FrameResponseBody {
					continue
				}
				acc = append(acc, f.Payload...)
				if f.EOF {
					g.body <- acc
					acc = nil
				}
				continue
			}
			pt, _ := protocol.PeekType(data)
			select {
			case g.order <- string(pt):
			default:
			}
			switch pt {
			case protocol.TypeWindowUpdate:
				var wu protocol.WindowUpdate
				_ = json.Unmarshal(data, &wu)
				g.wu <- wu
			case protocol.TypeHTTPResponse:
				var r protocol.HTTPResponse
				_ = json.Unmarshal(data, &r)
				g.resp <- r
			case protocol.TypeHTTPError:
				g.t.Errorf("ajan hata dondu: %s", data)
			}
		}
	}()
	return g
}

func (g *reqCreditRig) sendCtl(v any, ty protocol.MessageType) {
	b, _ := protocol.Marshal(v, ty)
	if err := g.srv.Write(context.Background(), websocket.MessageText, b); err != nil {
		g.t.Fatal(err)
	}
}

func (g *reqCreditRig) sendBody(id uint64, seq uint32, p []byte, eof bool) {
	f := protocol.BodyFrame{FrameType: protocol.FrameRequestBody, ReqID: id, Seq: seq, Payload: p, EOF: eof}
	if err := g.srv.Write(context.Background(), websocket.MessageBinary, f.Encode()); err != nil {
		g.t.Fatal(err)
	}
}

// Yeni sunucu (body_credit): ajan once kabul yollar (yanit basligindan once),
// yazdikca kredi iade eder; pencereye uyan sunucuyla yavas tuketen yerel
// servise 12 MB eksiksiz ulasir. Eskiden 4 MB kuyruk tasip istek kesiliyordu.
func TestReqCreditAgentSlowLocal(t *testing.T) {
	g := newReqCreditRig(t, 2*time.Millisecond)
	body := make([]byte, 12<<20)
	_, _ = rand.Read(body)
	const id = 41
	g.sendCtl(protocol.HTTPRequest{Type: protocol.TypeHTTPRequest, ReqID: id, TunnelID: "t1", Method: "POST", Path: "/",
		HasBody: true, ContentLength: int64(len(body)), Caps: []string{protocol.ReqCapBodyCredit}}, protocol.TypeHTTPRequest)

	avail := protocol.InitialWindowBytes
	acked := false
	var seq uint32
	for off := 0; off < len(body); {
		n := min(protocol.BodyChunkSize, len(body)-off)
		cost := n + protocol.StreamFrameCost
		for avail < cost {
			select {
			case wu := <-g.wu:
				if wu.ReqID != id {
					continue
				}
				if wu.Increment == 0 {
					acked = true
				}
				avail += wu.Increment
			case <-time.After(15 * time.Second):
				t.Fatalf("kredi gelmedi (gonderilen %d)", off)
			}
		}
		avail -= cost
		g.sendBody(id, seq, body[off:off+n], false)
		seq++
		off += n
	}
	g.sendBody(id, seq, nil, true)

	select {
	case r := <-g.resp:
		if r.Status != 200 {
			t.Fatalf("durum %d", r.Status)
		}
	case <-time.After(30 * time.Second):
		t.Fatal("yanit gelmedi")
	}
	got := <-g.body
	sum := sha256.Sum256(body)
	if want := strconv.Itoa(len(body)) + " " + hex.EncodeToString(sum[:]); string(got) != want {
		t.Fatalf("yerel servis govdeyi eksik/bozuk aldi: %s", got)
	}
	// Kabul basliktan once: sira kanalinda ilk mesaj window_update olmali.
	if first := <-g.order; first != string(protocol.TypeWindowUpdate) {
		t.Fatalf("ilk mesaj kabul olmali, %s geldi", first)
	}
	_ = acked
}

// Eski sunucu (Caps yok): ajan istek yonunde hic window_update yollamaz.
func TestReqCreditAgentLegacyServer(t *testing.T) {
	g := newReqCreditRig(t, 0)
	const id = 7
	g.sendCtl(protocol.HTTPRequest{Type: protocol.TypeHTTPRequest, ReqID: id, TunnelID: "t1", Method: "POST", Path: "/",
		HasBody: true, ContentLength: 3 << 20}, protocol.TypeHTTPRequest)
	chunk := make([]byte, protocol.BodyChunkSize)
	var seq uint32
	for sent := 0; sent < 3<<20; sent += len(chunk) {
		g.sendBody(id, seq, chunk, false)
		seq++
	}
	g.sendBody(id, seq, nil, true)
	select {
	case r := <-g.resp:
		if r.Status != 200 {
			t.Fatalf("durum %d", r.Status)
		}
	case <-time.After(15 * time.Second):
		t.Fatal("yanit gelmedi")
	}
	<-g.body
	select {
	case wu := <-g.wu:
		t.Fatalf("eski sunucuya window_update gitmemeli: %+v", wu)
	case <-time.After(200 * time.Millisecond):
	}
}

// bodyGrant muhasebesi: yazilan ve atilan veri ayni birimle, esik asilinca
// iade edilir; kill sonrasi yazim cift sayilmaz.
func TestBodyGrantAccounting(t *testing.T) {
	g := &bodyGrant{}
	var sent int
	// cs nil: send no-op; takeLocked'i dogrudan gozlemle.
	step := func(f func()) { f() }
	frame := 60 << 10
	cost := reqFrameCost(frame)
	n := 0
	for g.recv+cost <= protocol.InitialWindowBytes {
		step(func() { g.received(frame) })
		n++
	}
	// Hepsi yazilinca esik asilir, done sifirlanir, recv azalir.
	for i := 0; i < n; i++ {
		before := g.recv
		g.written(frame)
		if g.recv < before {
			sent += before - g.recv
		}
	}
	if sent == 0 || g.done >= reqGrantThreshold {
		t.Fatalf("esikte iade bekleniyordu: sent=%d done=%d", sent, g.done)
	}
	if sent+g.done != n*cost {
		t.Fatalf("iade + bekleyen = alinan olmali: %d + %d != %d", sent, g.done, n*cost)
	}
	// kill: alinmis her sey atilmis sayilir; sonraki written etkisiz.
	g.received(frame)
	g.kill()
	if g.done != g.recv {
		t.Fatalf("kill sonrasi done=recv olmali: %d %d", g.done, g.recv)
	}
	d := g.done
	g.written(frame)
	if g.done != d {
		t.Fatal("kill sonrasi yazim cift sayildi")
	}
}
