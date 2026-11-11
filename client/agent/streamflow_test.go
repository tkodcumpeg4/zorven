package agent

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/coder/websocket"

	"github.com/tkodcumpeg4/zorven/shared/protocol"
)

// flowHarness: sahte sunucu (WS) + gercek clientSession. Sunucu tarafi testin
// elinde; ajana gelen mesajlari gercek okuma dongusu gibi dagitan goroutine var.
type flowHarness struct {
	t     *testing.T
	cs    *clientSession
	srvC  chan *websocket.Conn
	srv   *websocket.Conn
	msgs  chan []byte // ajandan sunucuya metin mesajlari
	bin   chan protocol.BodyFrame
	wmu   sync.Mutex
	close func()
}

func newFlowHarness(t *testing.T, target string) *flowHarness {
	t.Helper()
	h := &flowHarness{t: t, srvC: make(chan *websocket.Conn, 1), msgs: make(chan []byte, 1024), bin: make(chan protocol.BodyFrame, 4096)}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c, err := websocket.Accept(w, r, nil)
		if err != nil {
			return
		}
		c.SetReadLimit(1 << 20)
		h.srvC <- c
		for {
			typ, data, err := c.Read(context.Background())
			if err != nil {
				return
			}
			if typ == websocket.MessageBinary {
				f, err := protocol.DecodeBodyFrame(data)
				if err == nil {
					h.bin <- f
				}
				continue
			}
			h.msgs <- data
		}
	}))
	ctx, cancel := context.WithCancel(context.Background())
	cc, _, err := websocket.Dial(ctx, "ws"+srv.URL[len("http"):], nil)
	if err != nil {
		t.Fatal(err)
	}
	cc.SetReadLimit(1 << 20)
	h.srv = <-h.srvC
	h.cs = &clientSession{
		conn:          cc,
		log:           slog.New(slog.NewTextHandler(io.Discard, nil)),
		targets:       map[string]string{"t1": target},
		streamConns:   map[uint64]net.Conn{},
		streamTunnels: map[uint64]string{},
		wsConns:       map[uint64]net.Conn{},
		windows:       newWindowTable(),
	}
	// Ajanin okuma dongusu (conn.go) yerine: yalniz bu testin mesajlari.
	go func() {
		for {
			typ, data, err := cc.Read(ctx)
			if err != nil {
				return
			}
			if typ == websocket.MessageBinary {
				if f, err := protocol.DecodeBodyFrame(data); err == nil {
					h.cs.routeStreamData(f.FrameType, f.ReqID, f.Payload)
				}
				continue
			}
			switch pt, _ := protocol.PeekType(data); pt {
			case protocol.TypeWindowUpdate:
				var wu protocol.WindowUpdate
				_ = json.Unmarshal(data, &wu)
				h.cs.windows.add(wu.ReqID, wu.Increment)
			case protocol.TypeStreamClose:
				var m protocol.StreamClose
				_ = json.Unmarshal(data, &m)
				h.cs.handleStreamClose(m.ReqID)
			}
		}
	}()
	h.close = func() { cancel(); cc.CloseNow(); srv.Close() }
	t.Cleanup(h.close)
	return h
}

func (h *flowHarness) send(typ websocket.MessageType, b []byte) {
	h.wmu.Lock()
	defer h.wmu.Unlock()
	if err := h.srv.Write(context.Background(), typ, b); err != nil {
		h.t.Fatal(err)
	}
}

func (h *flowHarness) ctl(v any, ty protocol.MessageType) {
	b, _ := protocol.Marshal(v, ty)
	h.send(websocket.MessageText, b)
}

func (h *flowHarness) expect(want protocol.MessageType) []byte {
	h.t.Helper()
	for {
		select {
		case m := <-h.msgs:
			if pt, _ := protocol.PeekType(m); pt == want {
				return m
			}
		case <-time.After(5 * time.Second):
			h.t.Fatalf("%s bekleniyordu", want)
		}
	}
}

// Kredi + yari kapama: ziyaretci 1 MB yollayip yazmayi kapatir; hedef EOF'u
// gorup 3 MB yanit yazar ve kapatir. Ajan: hedefe CloseWrite yapmali, sunucu
// penceresini asmadan yaniti gondermeli, sonunda bos EOF cercevesi yollamali
// (StreamClose DEGIL) ve sunucunun WindowUpdate'lerini kullanmali.
func TestAgentStreamCreditHalfClose(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	const reqBytes, respBytes = 1 << 20, 3 << 20
	gotReq := make(chan int, 1)
	go func() {
		c, err := ln.Accept()
		if err != nil {
			return
		}
		defer c.Close()
		n, _ := io.Copy(io.Discard, c) // ajan CloseWrite yapinca biter
		gotReq <- int(n)
		_, _ = c.Write(make([]byte, respBytes))
		_ = c.(*net.TCPConn).CloseWrite()
		_, _ = io.Copy(io.Discard, c)
	}()

	h := newFlowHarness(t, ln.Addr().String())
	go h.cs.handleStreamOpen(protocol.StreamOpen{Type: protocol.TypeStreamOpen, ReqID: 7, TunnelID: "t1", Proto: "tcp",
		Caps: []string{protocol.StreamCapCredit, protocol.StreamCapHalfClose, "gelecekte"}})
	var ack protocol.StreamAck
	_ = json.Unmarshal(h.expect(protocol.TypeStreamAck), &ack)
	if ack.Code != "" || !protocol.HasCap(ack.Caps, protocol.StreamCapCredit) || !protocol.HasCap(ack.Caps, protocol.StreamCapHalfClose) || protocol.HasCap(ack.Caps, "gelecekte") {
		t.Fatalf("ack: %+v", ack)
	}
	chunk := make([]byte, 32<<10)
	for i := 0; i < reqBytes/len(chunk); i++ {
		h.send(websocket.MessageBinary, protocol.BodyFrame{FrameType: protocol.FrameStreamData, ReqID: 7, Payload: chunk}.Encode())
	}
	h.send(websocket.MessageBinary, protocol.BodyFrame{FrameType: protocol.FrameStreamData, ReqID: 7, EOF: true}.Encode())

	select {
	case n := <-gotReq:
		if n != reqBytes {
			t.Fatalf("hedef %d/%d bayt aldi", n, reqBytes)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("hedef EOF gormedi (CloseWrite yapilmadi)")
	}
	// Ajan sunucuya yazdigi icin kredi iade etmeli.
	var wu protocol.WindowUpdate
	_ = json.Unmarshal(h.expect(protocol.TypeWindowUpdate), &wu)
	if wu.ReqID != 7 || wu.Increment < protocol.InitialWindowBytes/2 {
		t.Fatalf("WindowUpdate: %+v", wu)
	}

	// Sunucu rolu: gelen yaniti say, pencereyi tuket ve parca parca iade et.
	var got, unacked int
	for {
		select {
		case f := <-h.bin:
			if len(f.Payload) == 0 {
				if !f.EOF {
					t.Fatal("bos cerceve EOF bayragi tasimali")
				}
				if got != respBytes {
					t.Fatalf("yanit %d/%d", got, respBytes)
				}
				goto done
			}
			got += len(f.Payload)
			unacked += len(f.Payload) + protocol.StreamFrameCost
			if unacked > protocol.InitialWindowBytes {
				t.Fatalf("ajan pencereyi asti: %d", unacked)
			}
			if unacked >= protocol.InitialWindowBytes/2 {
				h.ctl(protocol.WindowUpdate{Type: protocol.TypeWindowUpdate, ReqID: 7, Increment: unacked}, protocol.TypeWindowUpdate)
				unacked = 0
			}
		case m := <-h.msgs:
			if pt, _ := protocol.PeekType(m); pt == protocol.TypeStreamClose {
				t.Fatal("yari kapamada ajan StreamClose gondermemeli")
			}
		case <-time.After(5 * time.Second):
			t.Fatalf("yanit bekleniyor: %d/%d", got, respBytes)
		}
	}
done:
	// Iki yon bitti: sunucu StreamClose gonderir, ajan akisi kapatir.
	h.ctl(protocol.StreamClose{Type: protocol.TypeStreamClose, ReqID: 7}, protocol.TypeStreamClose)
	deadline := time.Now().Add(5 * time.Second)
	for {
		h.cs.streamMu.Lock()
		_, open := h.cs.streamConns[7]
		h.cs.streamMu.Unlock()
		if !open {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("akis kapanmadi")
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// Eski sunucu (Caps yok): ajan yetenek yankilamaz ve eski davranisi surdurur
// (hedef kapaninca StreamClose; kredi beklemez).
func TestAgentStreamLegacyServer(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	const size = 5 << 20
	go func() {
		c, err := ln.Accept()
		if err != nil {
			return
		}
		_, _ = c.Write(bytes.Repeat([]byte{1}, size))
		c.Close()
	}()
	h := newFlowHarness(t, ln.Addr().String())
	go h.cs.handleStreamOpen(protocol.StreamOpen{Type: protocol.TypeStreamOpen, ReqID: 9, TunnelID: "t1", Proto: "tcp"})
	var ack protocol.StreamAck
	_ = json.Unmarshal(h.expect(protocol.TypeStreamAck), &ack)
	if len(ack.Caps) != 0 {
		t.Fatalf("eski sunucuya yetenek yankilanmamali: %v", ack.Caps)
	}
	got := 0
	for got < size {
		select {
		case f := <-h.bin:
			got += len(f.Payload) // kredi iadesi olmadan pencereden fazlasi gelmeli
		case <-time.After(5 * time.Second):
			t.Fatalf("eski modda veri kredisiz akmali: %d/%d", got, size)
		}
	}
	h.expect(protocol.TypeStreamClose)
}
