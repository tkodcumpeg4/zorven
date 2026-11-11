package ingress

import (
	"bufio"
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/coder/websocket"

	"github.com/tkodcumpeg4/zorven/server/reqlog"
	"github.com/tkodcumpeg4/zorven/server/store"
	"github.com/tkodcumpeg4/zorven/server/tunnel"
	"github.com/tkodcumpeg4/zorven/shared/protocol"
)

// rawAgent: testin protokolu tamamen elle surdugu ajan ucu (sahte ajan
// davranisi yetmiyor: yarim govde, sessiz kalma, basliktan sonra http_error).
type rawAgent struct {
	c   *websocket.Conn
	ctx context.Context
	// reqs: gelen http_request mesajlari; cancels: gelen cancel req_id'leri.
	reqs    chan protocol.HTTPRequest
	cancels chan protocol.Cancel
}

func newRawAgent(t *testing.T, hub *tunnel.Hub, clientID string) *rawAgent {
	t.Helper()
	ready := make(chan *tunnel.Session, 1)
	ctx, cancel := context.WithCancel(context.Background())
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c, err := websocket.Accept(w, r, nil)
		if err != nil {
			return
		}
		c.SetReadLimit(protocol.WSReadLimit)
		s := tunnel.NewSession("s-"+clientID, clientID, "agent", "127.0.0.1:1", c, slog.New(slog.NewTextHandler(io.Discard, nil)))
		ready <- s
		_ = s.Run(ctx)
	}))
	t.Cleanup(srv.Close)
	ac, _, err := websocket.Dial(ctx, "ws"+strings.TrimPrefix(srv.URL, "http"), nil)
	if err != nil {
		t.Fatal(err)
	}
	ac.SetReadLimit(protocol.WSReadLimit)
	s := <-ready
	if err := hub.Register(s); err != nil {
		t.Fatal(err)
	}
	a := &rawAgent{c: ac, ctx: ctx, reqs: make(chan protocol.HTTPRequest, 16), cancels: make(chan protocol.Cancel, 16)}
	t.Cleanup(func() { cancel(); ac.CloseNow() })
	go func() {
		for {
			typ, data, err := ac.Read(ctx)
			if err != nil {
				return
			}
			if typ != websocket.MessageText {
				continue
			}
			switch pt, _ := protocol.PeekType(data); pt {
			case protocol.TypeHTTPRequest:
				var m protocol.HTTPRequest
				_ = json.Unmarshal(data, &m)
				a.reqs <- m
			case protocol.TypeCancel:
				var m protocol.Cancel
				_ = json.Unmarshal(data, &m)
				a.cancels <- m
			}
		}
	}()
	return a
}

func (a *rawAgent) text(t *testing.T, v any, typ protocol.MessageType) {
	t.Helper()
	b, err := protocol.Marshal(v, typ)
	if err != nil {
		t.Fatal(err)
	}
	if err := a.c.Write(a.ctx, websocket.MessageText, b); err != nil {
		return // yazim hatasi test sonu kapanisi olabilir; iddia degil
	}
}

func (a *rawAgent) head(t *testing.T, reqID uint64, hdr map[string][]string) {
	a.text(t, protocol.HTTPResponse{Type: protocol.TypeHTTPResponse, ReqID: reqID, Status: 200, Headers: hdr, HasBody: true}, protocol.TypeHTTPResponse)
}

func (a *rawAgent) body(t *testing.T, reqID uint64, p []byte, eof bool) {
	t.Helper()
	f := protocol.BodyFrame{FrameType: protocol.FrameResponseBody, ReqID: reqID, Payload: p, EOF: eof}
	if err := a.c.Write(a.ctx, websocket.MessageBinary, f.Encode()); err != nil {
		return // yazim hatasi test sonu kapanisi olabilir; iddia degil
	}
}

func (a *rawAgent) nextReq(t *testing.T) protocol.HTTPRequest {
	t.Helper()
	select {
	case r := <-a.reqs:
		return r
	case <-time.After(3 * time.Second):
		t.Error("ajana istek gelmedi")
	}
	return protocol.HTTPRequest{}
}

func (a *rawAgent) expectCancel(t *testing.T, reqID uint64, d time.Duration) protocol.Cancel {
	t.Helper()
	deadline := time.After(d)
	for {
		select {
		case c := <-a.cancels:
			if c.ReqID == reqID {
				return c
			}
		case <-deadline:
			t.Fatalf("req_id=%d icin ajana iptal gitmedi", reqID)
			return protocol.Cancel{}
		}
	}
}

// doneCounter, ingress isleyicisinin dondugunu (goroutine serbest) sayar.
type doneCounter struct {
	h    http.Handler
	done atomic.Int32
}

func (d *doneCounter) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	defer d.done.Add(1)
	d.h.ServeHTTP(w, r)
}

func (d *doneCounter) wait(t *testing.T, n int32, within time.Duration) {
	t.Helper()
	deadline := time.Now().Add(within)
	for time.Now().Before(deadline) {
		if d.done.Load() >= n {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("isleyici donmedi (biten=%d, beklenen=%d)", d.done.Load(), n)
}

func streamEnv(t *testing.T) (*rawAgent, *doneCounter, *Handler) {
	t.Helper()
	tun := rejectRoute()
	tun.Plan = "pro"
	rt := NewRouter(&fakeStore{routes: []store.HostRoute{tun}})
	if err := rt.Reload(context.Background()); err != nil {
		t.Fatal(err)
	}
	hub := tunnel.NewHub()
	a := newRawAgent(t, hub, tun.ClientID)
	h := &Handler{Router: rt, Hub: hub, ReqLog: reqlog.New(100), Log: slog.New(slog.NewTextHandler(io.Discard, nil))}
	return a, &doneCounter{h: h}, h
}

func visitorReq(t *testing.T, ctx context.Context, url, method string, body io.Reader) *http.Request {
	t.Helper()
	r, err := http.NewRequestWithContext(ctx, method, url, body)
	if err != nil {
		t.Fatal(err)
	}
	r.Host = testHost
	return r
}

// Y4: ajan basliktan sonra http_error yollarsa ziyaretci yaniti TAMAMLANMIS
// gormemeli (eskiden chunked sonlandirici ile "basarili" bitiyordu).
func TestUpstreamBodyAbortIsNotCleanEOF_HTTP1(t *testing.T) {
	a, dc, _ := streamEnv(t)
	srv := httptest.NewServer(dc)
	defer srv.Close()

	go func() {
		req := a.nextReq(t)
		a.head(t, req.ReqID, map[string][]string{"Content-Type": {"text/plain"}})
		a.body(t, req.ReqID, []byte("partial"), false)
		a.text(t, protocol.HTTPError{Type: protocol.TypeHTTPError, ReqID: req.ReqID, Code: protocol.CodeLocalUnreachable, Message: "kesildi"}, protocol.TypeHTTPError)
	}()
	resp, err := http.DefaultClient.Do(visitorReq(t, context.Background(), srv.URL+"/x", "GET", nil))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(resp.Body)
	if err == nil {
		t.Fatalf("kesik govde temiz EOF ile bitti (%q); baglanti abort edilmeliydi", b)
	}
	dc.wait(t, 1, 2*time.Second)
}

// Y4 (HTTP/2): ayni durumda akis RST_STREAM ile bitmeli.
func TestUpstreamBodyAbortIsNotCleanEOF_HTTP2(t *testing.T) {
	a, dc, _ := streamEnv(t)
	srv := httptest.NewUnstartedServer(dc)
	srv.EnableHTTP2 = true
	srv.StartTLS()
	defer srv.Close()
	cl := srv.Client()

	go func() {
		req := a.nextReq(t)
		a.head(t, req.ReqID, map[string][]string{"Content-Type": {"text/plain"}})
		a.body(t, req.ReqID, []byte("partial"), false)
		a.text(t, protocol.HTTPError{Type: protocol.TypeHTTPError, ReqID: req.ReqID, Code: protocol.CodeLocalUnreachable, Message: "kesildi"}, protocol.TypeHTTPError)
	}()
	r := visitorReq(t, context.Background(), srv.URL+"/x", "GET", nil)
	// r.TLS.ServerName (127.0.0.1) Host ile uyusmazsa 421 olur; SNI'yi bos birak.
	cl.Transport.(*http.Transport).TLSClientConfig = &tls.Config{InsecureSkipVerify: true}
	resp, err := cl.Do(r)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.ProtoMajor != 2 {
		t.Fatalf("HTTP/2 bekleniyordu: %s", resp.Proto)
	}
	if b, err := io.ReadAll(resp.Body); err == nil {
		t.Fatalf("HTTP/2 kesik govde temiz bitti (%q, durum %d)", b, resp.StatusCode)
	}
}

// Eski ajan (hata yollamaz, susar): ziyaretci vazgecince isleyici donmeli ve
// ajana iptal gitmeli; eskiden Exchange.Read oturum sonuna kadar bekliyordu.
func TestSilentAgentReleasedWhenVisitorLeaves(t *testing.T) {
	a, dc, _ := streamEnv(t)
	srv := httptest.NewServer(dc)
	defer srv.Close()

	const n = 5
	for i := 0; i < n; i++ {
		go func() {
			req := a.nextReq(t)
			a.head(t, req.ReqID, map[string][]string{"Content-Length": {"1000"}})
			a.body(t, req.ReqID, []byte("partial"), false)
		}()
		ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
		resp, err := http.DefaultClient.Do(visitorReq(t, ctx, srv.URL+"/halfclose", "GET", nil))
		if err == nil {
			_, _ = io.ReadAll(resp.Body)
			resp.Body.Close()
		}
		cancel()
	}
	dc.wait(t, n, 3*time.Second)
	for i := 0; i < n; i++ {
		select {
		case <-a.cancels:
		case <-time.After(2 * time.Second):
			t.Fatalf("%d/%d iptal geldi", i, n)
		}
	}
}

// Y5: buyuk indirmede ziyaretci yarida kapatinca ajana iptal gitmeli.
func TestVisitorAbortMidDownloadCancelsAgent(t *testing.T) {
	a, dc, _ := streamEnv(t)
	srv := httptest.NewServer(dc)
	defer srv.Close()

	gotID := make(chan uint64, 1)
	go func() {
		req := a.nextReq(t)
		gotID <- req.ReqID
		a.head(t, req.ReqID, map[string][]string{"Content-Type": {"application/octet-stream"}})
		chunk := make([]byte, 64<<10)
		for i := 0; i < 16; i++ { // 1 MB; pencere icinde (kredisiz test oturumu)
			a.body(t, req.ReqID, chunk, false)
		}
		// Sonra susar: ajan kredi bekliyormus gibi.
	}()
	ctx, cancel := context.WithCancel(context.Background())
	resp, err := http.DefaultClient.Do(visitorReq(t, ctx, srv.URL+"/big", "GET", nil))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := io.CopyN(io.Discard, resp.Body, 100<<10); err != nil {
		t.Fatal(err)
	}
	cancel()
	resp.Body.Close()
	c := a.expectCancel(t, <-gotID, 3*time.Second)
	if c.Reason != "client_disconnected" {
		t.Fatalf("iptal nedeni: %q", c.Reason)
	}
	dc.wait(t, 1, 3*time.Second)
}

// O8: yarida kesilen yuklemede ajana iptal gitmeli.
func TestPartialUploadCancelsAgent(t *testing.T) {
	a, dc, _ := streamEnv(t)
	srv := httptest.NewServer(dc)
	defer srv.Close()

	c, err := net.Dial("tcp", strings.TrimPrefix(srv.URL, "http://"))
	if err != nil {
		t.Fatal(err)
	}
	fmt.Fprintf(c, "POST /up HTTP/1.1\r\nHost: %s\r\nContent-Length: 100000\r\n\r\n%s", testHost, strings.Repeat("x", 1000))
	req := a.nextReq(t)
	time.Sleep(100 * time.Millisecond)
	c.Close()
	got := a.expectCancel(t, req.ReqID, 3*time.Second)
	if got.Reason != "request_body_failed" {
		t.Fatalf("iptal nedeni: %q", got.Reason)
	}
	dc.wait(t, 1, 3*time.Second)
}

// O11: ilerlemesiz (slowloris) govde kayan deadline'da kesilmeli (408).
func TestSlowRequestBodyIsCut(t *testing.T) {
	oldD, oldMin := requestBodyIdleTimeout, requestBodyMinProgress
	requestBodyIdleTimeout, requestBodyMinProgress = 400*time.Millisecond, 1024
	t.Cleanup(func() { requestBodyIdleTimeout, requestBodyMinProgress = oldD, oldMin })

	a, dc, _ := streamEnv(t)
	srv := httptest.NewServer(dc)
	defer srv.Close()

	c, err := net.Dial("tcp", strings.TrimPrefix(srv.URL, "http://"))
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	fmt.Fprintf(c, "POST /up HTTP/1.1\r\nHost: %s\r\nContent-Length: 1000\r\n\r\n", testHost)
	start := time.Now()
	stop := make(chan struct{})
	defer close(stop)
	go func() { // 1 bayt / 50 ms: ilerleme var ama esigin cok altinda
		for {
			select {
			case <-stop:
				return
			case <-time.After(50 * time.Millisecond):
				if _, err := c.Write([]byte("x")); err != nil {
					return
				}
			}
		}
	}()
	req := a.nextReq(t)
	_ = c.SetReadDeadline(time.Now().Add(5 * time.Second))
	resp, err := http.ReadResponse(bufio.NewReader(c), nil)
	if err != nil {
		t.Fatalf("yanit okunamadi: %v", err)
	}
	if resp.StatusCode != http.StatusRequestTimeout {
		t.Fatalf("408 bekleniyordu: %d", resp.StatusCode)
	}
	if el := time.Since(start); el > 3*time.Second {
		t.Fatalf("kesme gecikti: %v", el)
	}
	a.expectCancel(t, req.ReqID, 2*time.Second)
	dc.wait(t, 1, 2*time.Second)
}

// O11 karsi-test: esigi asan (yavas ama mesru) yukleme kesilmemeli; govde
// bittikten sonra okuma deadline'i kaldirilmali (aksi halde uzun yanit kesilirdi).
func TestSteadyUploadAndLongResponseSurvive(t *testing.T) {
	oldD, oldMin := requestBodyIdleTimeout, requestBodyMinProgress
	requestBodyIdleTimeout, requestBodyMinProgress = 300*time.Millisecond, 1024
	t.Cleanup(func() { requestBodyIdleTimeout, requestBodyMinProgress = oldD, oldMin })

	a, dc, _ := streamEnv(t)
	srv := httptest.NewServer(dc)
	defer srv.Close()

	go func() {
		req := a.nextReq(t)
		// Govdeyi (sahte ajan) yok sayip yanit ver; yanit deadline'dan uzun surer.
		a.head(t, req.ReqID, map[string][]string{"Content-Type": {"text/event-stream"}})
		for i := 0; i < 4; i++ {
			time.Sleep(250 * time.Millisecond)
			a.body(t, req.ReqID, []byte("data: x\n\n"), false)
		}
		a.body(t, req.ReqID, nil, true)
	}()
	pr, pw := io.Pipe()
	go func() {
		chunk := []byte(strings.Repeat("y", 2048))
		for i := 0; i < 5; i++ { // 2 KB / 100 ms: esigin ustunde
			if _, err := pw.Write(chunk); err != nil {
				return
			}
			time.Sleep(100 * time.Millisecond)
		}
		pw.Close()
	}()
	r := visitorReq(t, context.Background(), srv.URL+"/up", "POST", pr)
	r.ContentLength = 5 * 2048
	resp, err := http.DefaultClient.Do(r)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(resp.Body)
	if err != nil || resp.StatusCode != 200 || strings.Count(string(b), "data: x") != 4 {
		t.Fatalf("durum %d govde %q hata %v", resp.StatusCode, b, err)
	}
	dc.wait(t, 1, 2*time.Second)
}

// A1 #9: Content-Length tavani asiyorsa govde ajana akmadan hemen 413.
func TestOversizedContentLengthRejectedEarly(t *testing.T) {
	a, dc, _ := streamEnv(t)
	srv := httptest.NewServer(dc)
	defer srv.Close()

	c, err := net.Dial("tcp", strings.TrimPrefix(srv.URL, "http://"))
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	fmt.Fprintf(c, "POST /up HTTP/1.1\r\nHost: %s\r\nContent-Length: %d\r\n\r\nabc", testHost, protocol.MaxBodyBytes+1)
	_ = c.SetReadDeadline(time.Now().Add(3 * time.Second))
	resp, err := http.ReadResponse(bufio.NewReader(c), nil)
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusRequestEntityTooLarge {
		t.Fatalf("413 bekleniyordu: %d", resp.StatusCode)
	}
	select {
	case r := <-a.reqs:
		t.Fatalf("istek ajana gitmemeliydi: %+v", r)
	case <-time.After(200 * time.Millisecond):
	}
}

// blockingWriter: SetWriteDeadline destekli, Write'i deadline dolana kadar
// bloklayan ResponseWriter (hic okumayan ziyaretci).
type blockingWriter struct {
	hdr      http.Header
	deadline atomic.Pointer[time.Time]
	status   int
}

func (b *blockingWriter) Header() http.Header { return b.hdr }
func (b *blockingWriter) WriteHeader(c int)   { b.status = c }
func (b *blockingWriter) Flush()              {}
func (b *blockingWriter) SetWriteDeadline(t time.Time) error {
	b.deadline.Store(&t)
	return nil
}
func (b *blockingWriter) Write(p []byte) (int, error) {
	for {
		d := b.deadline.Load()
		if d != nil && !d.IsZero() && time.Now().After(*d) {
			return 0, os.ErrDeadlineExceeded
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// Yazma tarafi: HIC okumayan ziyaretci stall esiginde kesilir, ajana iptal gider.
func TestStalledVisitorWriteIsCut(t *testing.T) {
	old := responseWriteStallTimeout
	responseWriteStallTimeout = 300 * time.Millisecond
	t.Cleanup(func() { responseWriteStallTimeout = old })

	a, _, h := streamEnv(t)
	go func() {
		req := a.nextReq(t)
		a.head(t, req.ReqID, nil)
		a.body(t, req.ReqID, []byte("veri"), false)
	}()
	w := &blockingWriter{hdr: http.Header{}}
	r := httptest.NewRequest("GET", "http://"+testHost+"/x", nil)
	done := make(chan struct{})
	go func() { defer close(done); h.ServeHTTP(w, r) }()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("tikanan yazim kesilmedi")
	}
	a.expectCancel(t, 1, 2*time.Second)
}

func TestCopyResponseBodySeparatesSides(t *testing.T) {
	re := errors.New("okuma")
	if r, w := copyResponseBody(io.Discard, io.MultiReader(strings.NewReader("ab"), &failReader{re})); !errors.Is(r, re) || w != nil {
		t.Fatalf("okuma hatasi: r=%v w=%v", r, w)
	}
	we := errors.New("yazma")
	if r, w := copyResponseBody(&errWriter{we}, strings.NewReader("ab")); r != nil || !errors.Is(w, we) {
		t.Fatalf("yazma hatasi: r=%v w=%v", r, w)
	}
	var sb strings.Builder
	if r, w := copyResponseBody(&sb, strings.NewReader("tamam")); r != nil || w != nil || sb.String() != "tamam" {
		t.Fatalf("temiz kopya: %v %v %q", r, w, sb.String())
	}
}

type failReader struct{ err error }

func (e *failReader) Read([]byte) (int, error) { return 0, e.err }

type errWriter struct{ err error }

func (e *errWriter) Write([]byte) (int, error) { return 0, e.err }
