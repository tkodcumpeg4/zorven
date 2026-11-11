package tunnel

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/json"
	"io"
	"sync/atomic"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/tkodcumpeg4/zorven/shared/protocol"
)

// reqAgent, istek govdesi kredisi testleri icin sahte ajan: sunucudan gelen
// mesajlari ayri goroutine'de okur.
type reqAgent struct {
	h      *fileHarness
	req    chan protocol.HTTPRequest
	frames chan protocol.BodyFrame
}

func newReqAgent(t *testing.T, flowControl bool) *reqAgent {
	h := newFileHarness(t, false)
	h.s.flowControl = flowControl
	a := &reqAgent{h: h, req: make(chan protocol.HTTPRequest, 16), frames: make(chan protocol.BodyFrame, 4096)}
	go func() {
		for {
			typ, data, err := h.agent.Read(context.Background())
			if err != nil {
				return
			}
			if typ == websocket.MessageBinary {
				f, err := protocol.DecodeBodyFrame(data)
				if err == nil && f.FrameType == protocol.FrameRequestBody {
					a.frames <- f
				}
				continue
			}
			if pt, _ := protocol.PeekType(data); pt == protocol.TypeHTTPRequest {
				var r protocol.HTTPRequest
				_ = json.Unmarshal(data, &r)
				a.req <- r
			}
		}
	}()
	return a
}

func (a *reqAgent) nextReq(t *testing.T) protocol.HTTPRequest {
	t.Helper()
	select {
	case r := <-a.req:
		return r
	case <-time.After(5 * time.Second):
		t.Fatal("http_request gelmedi")
	}
	return protocol.HTTPRequest{}
}

func (a *reqAgent) grant(id uint64, n int) {
	a.h.sendCtl(protocol.WindowUpdate{Type: protocol.TypeWindowUpdate, ReqID: id, Increment: n}, protocol.TypeWindowUpdate)
}

func randBody(t *testing.T, n int) []byte {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		t.Fatal(err)
	}
	return b
}

// Yeni ajan: kabul + yavas iade. Sunucu pencereyi asla asmamali, govde tam ve
// dogru ulasmali (yavas tuketen yerel servis: Faz 1 "yeni bulgu").
func TestReqCreditNewAgentRespectsWindow(t *testing.T) {
	a := newReqAgent(t, true)
	body := randBody(t, 12<<20)
	sendErr := make(chan error, 1)
	var reqID uint64
	go func() {
		ex, err := a.h.s.SendRequest(context.Background(), protocol.HTTPRequest{TunnelID: "t", Method: "POST", Path: "/", HasBody: true})
		if err != nil {
			sendErr <- err
			return
		}
		atomic.StoreUint64(&reqID, ex.ReqID())
		sendErr <- a.h.s.StreamRequestBodyUnbounded(context.Background(), ex.ReqID(), io.NopCloser(bytes.NewReader(body)))
	}()
	r := a.nextReq(t)
	if !protocol.HasCap(r.Caps, protocol.ReqCapBodyCredit) {
		t.Fatalf("body_credit onerilmeli: %v", r.Caps)
	}
	a.grant(r.ReqID, 0) // kabul

	got := sha256.New()
	var total, outstanding, maxOut, pendingGrant int
	deadline := time.After(30 * time.Second)
	for {
		var f protocol.BodyFrame
		select {
		case f = <-a.frames:
		case <-deadline:
			t.Fatalf("govde tamamlanmadi: %d/%d", total, len(body))
		}
		if f.EOF && len(f.Payload) == 0 {
			break
		}
		c := len(f.Payload) + protocol.StreamFrameCost
		outstanding += c
		if outstanding > maxOut {
			maxOut = outstanding
		}
		if outstanding > protocol.InitialWindowBytes {
			t.Fatalf("pencere asildi: %d", outstanding)
		}
		got.Write(f.Payload)
		total += len(f.Payload)
		// Yavas tuketici: ajandaki esikle ayni (pencerenin yarisi), biraz bekleyip iade et.
		pendingGrant += c
		if pendingGrant >= protocol.InitialWindowBytes/2 {
			time.Sleep(5 * time.Millisecond)
			a.grant(r.ReqID, pendingGrant)
			outstanding -= pendingGrant
			pendingGrant = 0
		}
	}
	if err := <-sendErr; err != nil {
		t.Fatal(err)
	}
	want := sha256.Sum256(body)
	if total != len(body) || !bytes.Equal(got.Sum(nil), want[:]) {
		t.Fatalf("govde bozuk: %d/%d", total, len(body))
	}
	if a.h.s.reqCreditMode.Load() != reqCreditYes {
		t.Fatal("oturum 'kredi var' ogrenmeliydi")
	}
	t.Logf("en yuksek ucustaki kredi: %d", maxOut)
}

// Kredi gelmezse gonderen beklemeli; yanit bitince (closeBody) ya da ctx
// iptalinde hemen birakmali.
func TestReqCreditBlocksAndReleases(t *testing.T) {
	a := newReqAgent(t, true)
	for _, viaCtx := range []bool{false, true} {
		ctx, cancel := context.WithCancel(context.Background())
		ex, err := a.h.s.SendRequest(ctx, protocol.HTTPRequest{TunnelID: "t", Method: "POST", Path: "/", HasBody: true})
		if err != nil {
			t.Fatal(err)
		}
		r := a.nextReq(t)
		a.grant(r.ReqID, 0)
		var sent atomic.Int64
		done := make(chan error, 1)
		go func() {
			done <- a.h.s.StreamRequestBodyUnbounded(ctx, ex.ReqID(), io.NopCloser(&countingReader{n: &sent, left: 64 << 20}))
		}()
		waitFor(t, "pencere dolsun", func() bool { return sent.Load() >= int64(protocol.InitialWindowBytes)-(256<<10) })
		time.Sleep(200 * time.Millisecond)
		before := sent.Load()
		time.Sleep(200 * time.Millisecond)
		if sent.Load() != before || before > int64(protocol.InitialWindowBytes)+(128<<10) {
			t.Fatalf("kredisiz gonderim surdu: %d -> %d", before, sent.Load())
		}
		if viaCtx {
			cancel()
			select {
			case err := <-done:
				if err == nil {
					t.Fatal("iptal hatasi bekleniyordu")
				}
			case <-time.After(5 * time.Second):
				t.Fatal("ctx iptali bekleyeni birakmadi")
			}
		} else {
			// Yanit basligi + EOF: ajan istegi bitirdi; kalan govde kredisiz akar.
			a.h.sendCtl(protocol.HTTPResponse{Type: protocol.TypeHTTPResponse, ReqID: r.ReqID, Status: 413}, protocol.TypeHTTPResponse)
			select {
			case err := <-done:
				if err != nil {
					t.Fatal(err)
				}
			case <-time.After(20 * time.Second):
				t.Fatal("yanit bitince gonderen birakilmadi")
			}
		}
		cancel()
		a.h.s.FinishExchange(ex.ReqID())
		drain(a.frames)
	}
}

// Eski ajan (kabul yok) + yanit basligi: gonderen beklemeden devam eder ve
// oturum "kredi yok" ogrenir; sonraki istekte bekleme yok.
func TestReqCreditLegacyAgentByHead(t *testing.T) {
	old := reqCreditAckWait
	reqCreditAckWait = time.Hour // basliktan ogrenmeli, zaman asimindan degil
	t.Cleanup(func() { reqCreditAckWait = old })
	a := newReqAgent(t, true)
	ex, err := a.h.s.SendRequest(context.Background(), protocol.HTTPRequest{TunnelID: "t", Method: "POST", Path: "/", HasBody: true})
	if err != nil {
		t.Fatal(err)
	}
	r := a.nextReq(t)
	a.h.sendCtl(protocol.HTTPResponse{Type: protocol.TypeHTTPResponse, ReqID: r.ReqID, Status: 200, HasBody: true}, protocol.TypeHTTPResponse)
	waitFor(t, "baslik islensin", func() bool { return a.h.s.reqCreditMode.Load() == reqCreditNo })
	start := time.Now()
	if err := a.h.s.StreamRequestBodyUnbounded(context.Background(), ex.ReqID(), io.NopCloser(bytes.NewReader(make([]byte, 8<<20)))); err != nil {
		t.Fatal(err)
	}
	a.h.s.FinishExchange(ex.ReqID())
	// Ikinci istek: oturum eski ajan biliniyor, hic beklemeden akar.
	ex2, err := a.h.s.SendRequest(context.Background(), protocol.HTTPRequest{TunnelID: "t", Method: "POST", Path: "/", HasBody: true})
	if err != nil {
		t.Fatal(err)
	}
	if err := a.h.s.StreamRequestBodyUnbounded(context.Background(), ex2.ReqID(), io.NopCloser(bytes.NewReader(make([]byte, 8<<20)))); err != nil {
		t.Fatal(err)
	}
	if d := time.Since(start); d > 10*time.Second {
		t.Fatalf("eski ajanda gonderim bekledi: %s", d)
	}
	drain(a.frames)
}

// Eski ajan, baslik da gelmeden (yerel servis govdenin tamamini bekliyor):
// kabul beklemesi zaman asimina ugrayinca kredisiz devam edilir.
func TestReqCreditLegacyAgentByTimeout(t *testing.T) {
	old := reqCreditAckWait
	reqCreditAckWait = 300 * time.Millisecond
	t.Cleanup(func() { reqCreditAckWait = old })
	a := newReqAgent(t, true)
	ex, err := a.h.s.SendRequest(context.Background(), protocol.HTTPRequest{TunnelID: "t", Method: "POST", Path: "/", HasBody: true})
	if err != nil {
		t.Fatal(err)
	}
	_ = a.nextReq(t)
	start := time.Now()
	if err := a.h.s.StreamRequestBodyUnbounded(context.Background(), ex.ReqID(), io.NopCloser(bytes.NewReader(make([]byte, 8<<20)))); err != nil {
		t.Fatal(err)
	}
	if d := time.Since(start); d > 5*time.Second {
		t.Fatalf("zaman asimi uygulanmadi: %s", d)
	}
	if a.h.s.reqCreditMode.Load() != reqCreditNo {
		t.Fatal("oturum 'kredi yok' ogrenmeliydi")
	}
	drain(a.frames)
}

// Yanit yonunde akis kontrolu olmayan (cok eski) ajana hic onerilmez; govdesiz
// isteklerde de oneri yok.
func TestReqCreditNotOffered(t *testing.T) {
	a := newReqAgent(t, false)
	if _, err := a.h.s.SendRequest(context.Background(), protocol.HTTPRequest{TunnelID: "t", Method: "POST", Path: "/", HasBody: true}); err != nil {
		t.Fatal(err)
	}
	if r := a.nextReq(t); len(r.Caps) != 0 {
		t.Fatalf("flow_control yokken oneri olmamali: %v", r.Caps)
	}
	b := newReqAgent(t, true)
	if _, err := b.h.s.SendRequest(context.Background(), protocol.HTTPRequest{TunnelID: "t", Method: "GET", Path: "/"}); err != nil {
		t.Fatal(err)
	}
	if r := b.nextReq(t); len(r.Caps) != 0 {
		t.Fatalf("govdesiz istekte oneri olmamali: %v", r.Caps)
	}
}

type countingReader struct {
	n    *atomic.Int64
	left int
}

func (r *countingReader) Read(p []byte) (int, error) {
	if r.left <= 0 {
		return 0, io.EOF
	}
	n := min(len(p), r.left)
	r.left -= n
	r.n.Add(int64(n))
	return n, nil
}

func drain(ch chan protocol.BodyFrame) {
	for {
		select {
		case <-ch:
		case <-time.After(100 * time.Millisecond):
			return
		}
	}
}
