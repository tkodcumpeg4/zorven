package agent

import (
	"bufio"
	"context"
	"encoding/json"
	"net"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/coder/websocket"

	"github.com/tkodcumpeg4/zorven/shared/protocol"
)

// readUntil, sunucu tarafindan tip eslesene kadar mesaj okur (govde cercevelerini atlar).
func readUntil(t *testing.T, ctx context.Context, srv *websocket.Conn, want protocol.MessageType) []byte {
	t.Helper()
	for {
		typ, data, err := srv.Read(ctx)
		if err != nil {
			t.Fatalf("%s beklenirken okuma hatasi: %v", want, err)
		}
		if typ != websocket.MessageText {
			continue
		}
		if pt, _ := protocol.PeekType(data); pt == want {
			return data
		}
	}
}

// Y4: hedef Content-Length bildirip govdeyi yarida keserse ajan basliktan
// SONRA http_error yollamali; yoksa sunucu govdenin devamini sonsuza kadar bekler.
func TestTruncatedLocalBodySendsHTTPError(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	go func() {
		c, err := ln.Accept()
		if err != nil {
			return
		}
		br := bufio.NewReader(c)
		_, _ = http.ReadRequest(br)
		_, _ = c.Write([]byte("HTTP/1.1 200 OK\r\nContent-Length: 1000\r\n\r\npartial"))
		c.Close()
	}()

	cs, srv, ctx := probePair(t)
	cs.SetTargets([]protocol.TunnelSpec{{ID: "t", Target: "http://" + ln.Addr().String()}})
	cs.handleRequest(context.Background(), protocol.HTTPRequest{Type: protocol.TypeHTTPRequest, ReqID: 3, TunnelID: "t", Method: "GET", Path: "/"})

	readUntil(t, ctx, srv, protocol.TypeHTTPResponse)
	var he protocol.HTTPError
	if err := json.Unmarshal(readUntil(t, ctx, srv, protocol.TypeHTTPError), &he); err != nil {
		t.Fatal(err)
	}
	if he.ReqID != 3 || he.Code != protocol.CodeLocalUnreachable {
		t.Fatalf("http_error: %+v", he)
	}
}

// streamingTarget, kapanana kadar veri basan ve acik baglanti sayisini tutan yerel hedef.
func streamingTarget(t *testing.T, readBody bool) (addr string, open *atomic.Int32, gotReq chan struct{}) {
	t.Helper()
	open = &atomic.Int32{}
	gotReq = make(chan struct{}, 4)
	srv := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		open.Add(1)
		defer open.Add(-1)
		gotReq <- struct{}{}
		if readBody {
			// Yukleme: govdeyi sonuna kadar bekler (yarim govde = asili kalir).
			buf := make([]byte, 4096)
			for {
				if _, err := r.Body.Read(buf); err != nil {
					return
				}
			}
		}
		chunk := make([]byte, 32<<10)
		for {
			if _, err := w.Write(chunk); err != nil {
				return
			}
			w.(http.Flusher).Flush()
			select {
			case <-r.Context().Done():
				return
			default:
			}
		}
	})}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	go srv.Serve(ln)
	t.Cleanup(func() { srv.Close() })
	return ln.Addr().String(), open, gotReq
}

func waitZero(t *testing.T, what string, n *atomic.Int32, d time.Duration) {
	t.Helper()
	deadline := time.Now().Add(d)
	for time.Now().Before(deadline) {
		if n.Load() == 0 {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("%s serbest kalmadi (acik=%d)", what, n.Load())
}

func inflightCount(cs *clientSession) int {
	cs.mu.Lock()
	defer cs.mu.Unlock()
	return len(cs.inflight)
}

// Y5: kredi beklemesindeki (sunucu window_update yollamiyor) akis, sunucu iptal
// edince cozulmeli; yerel istek ve hedef baglantisi birakilmali.
func TestCancelReleasesCreditWaitAndTarget(t *testing.T) {
	addr, open, gotReq := streamingTarget(t, false)
	cs, srv, ctx := probePair(t)
	cs.flowControl = true
	srv.SetReadLimit(protocol.WSReadLimit)
	cs.SetTargets([]protocol.TunnelSpec{{ID: "t", Target: "http://" + addr}})
	cs.handleRequest(context.Background(), protocol.HTTPRequest{Type: protocol.TypeHTTPRequest, ReqID: 5, TunnelID: "t", Method: "GET", Path: "/big"})
	<-gotReq
	readUntil(t, ctx, srv, protocol.TypeHTTPResponse)
	// Pencere (2 MB) dolana kadar govde cercevelerini oku: ajan artik kredi bekliyor.
	got := 0
	for got < protocol.InitialWindowBytes {
		typ, data, err := srv.Read(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if typ == websocket.MessageBinary {
			f, _ := protocol.DecodeBodyFrame(data)
			got += len(f.Payload)
		}
	}
	time.Sleep(100 * time.Millisecond)
	if inflightCount(cs) != 1 {
		t.Fatalf("istek ucusta olmali: %d", inflightCount(cs))
	}
	cs.cancelRequest(5, "client_disconnected")
	waitZero(t, "hedef baglantisi", open, 3*time.Second)
	deadline := time.Now().Add(3 * time.Second)
	for inflightCount(cs) != 0 && time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
	}
	if n := inflightCount(cs); n != 0 {
		t.Fatalf("iptal sonrasi ucusta istek kaldi: %d", n)
	}
}

// O8: yarim yuklemede sunucu iptal edince yazma kuyrugu ve hedef baglantisi
// serbest kalmali (eskiden govde EOF'u hic gelmedigi icin suresiz asili).
func TestCancelDuringUploadReleasesTarget(t *testing.T) {
	addr, open, gotReq := streamingTarget(t, true)
	cs, _, _ := probePair(t)
	cs.SetTargets([]protocol.TunnelSpec{{ID: "t", Target: "http://" + addr}})
	cs.handleRequest(context.Background(), protocol.HTTPRequest{Type: protocol.TypeHTTPRequest, ReqID: 6, TunnelID: "t",
		Method: "POST", Path: "/up", HasBody: true, ContentLength: 1 << 20})
	cs.routeBody(protocol.BodyFrame{FrameType: protocol.FrameRequestBody, ReqID: 6, Payload: []byte(strings.Repeat("x", 1000))})
	select {
	case <-gotReq:
	case <-time.After(3 * time.Second):
		t.Fatal("hedef istegi almadi")
	}
	cs.cancelRequest(6, "request_body_failed")
	waitZero(t, "hedef baglantisi", open, 3*time.Second)
	deadline := time.Now().Add(3 * time.Second)
	for inflightCount(cs) != 0 && time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
	}
	if n := inflightCount(cs); n != 0 {
		t.Fatalf("iptal sonrasi ucusta istek kaldi: %d", n)
	}
}

// Sunucu iptal ettiyse ajan ona http_error YOLLAMAMALI (kayit zaten silindi).
func TestServerCancelDoesNotEchoHTTPError(t *testing.T) {
	addr, _, gotReq := streamingTarget(t, false)
	cs, srv, ctx := probePair(t)
	cs.SetTargets([]protocol.TunnelSpec{{ID: "t", Target: "http://" + addr}})
	cs.handleRequest(context.Background(), protocol.HTTPRequest{Type: protocol.TypeHTTPRequest, ReqID: 8, TunnelID: "t", Method: "GET", Path: "/"})
	<-gotReq
	readUntil(t, ctx, srv, protocol.TypeHTTPResponse)
	cs.cancelRequest(8, "client_disconnected")
	rctx, cancel := context.WithTimeout(ctx, 700*time.Millisecond)
	defer cancel()
	for {
		typ, data, err := srv.Read(rctx)
		if err != nil {
			return // zaman asimi: http_error gelmedi
		}
		if typ == websocket.MessageText {
			if pt, _ := protocol.PeekType(data); pt == protocol.TypeHTTPError {
				t.Fatal("sunucu iptalinden sonra http_error yollandi")
			}
		}
	}
}
