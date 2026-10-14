package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/coder/websocket"

	"github.com/tkodcumpeg4/zorven/shared/protocol"
)

// Regresyon (T1, DP-7 "TCP 20 eszamanli 19/20"): ajan StreamAck'i akisi
// registerStream ile kaydetmeden ONCE gonderiyordu. Sunucu ack'i alir almaz
// ziyaretci verisini yollar; veri okuma dongusune kayittan once ulasirsa
// routeStreamData akisi bulamayip baytlari sessizce dusuruyordu (yankisi hic
// gelmeyen baglanti). Burada sunucu ack gelir gelmez veri yollar ve her
// akisin yankisinin geri geldigini dogrular.
func TestStreamAckSonrasiIlkVeriDusmez(t *testing.T) {
	echo, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer echo.Close()
	go func() {
		for {
			c, err := echo.Accept()
			if err != nil {
				return
			}
			go func() {
				defer c.Close()
				buf := make([]byte, 4096)
				for {
					n, err := c.Read(buf)
					if n > 0 {
						c.Write(buf[:n])
					}
					if err != nil {
						return
					}
				}
			}()
		}
	}()

	const n = 300
	var mu sync.Mutex
	got := map[uint64]bool{}
	done := make(chan struct{})

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c, err := websocket.Accept(w, r, nil)
		if err != nil {
			return
		}
		c.SetReadLimit(1 << 20)
		for {
			typ, data, err := c.Read(context.Background())
			if err != nil {
				return
			}
			if typ == websocket.MessageBinary {
				f, err := protocol.DecodeBodyFrame(data)
				if err != nil {
					continue
				}
				mu.Lock()
				got[f.ReqID] = true
				if len(got) == n {
					close(done)
				}
				mu.Unlock()
				continue
			}
			var h struct {
				Type  protocol.MessageType `json:"type"`
				ReqID uint64               `json:"req_id"`
			}
			if json.Unmarshal(data, &h) != nil || h.Type != protocol.TypeStreamAck {
				continue
			}
			// Ack gelir gelmez veri yolla (gercek sunucudaki davranis).
			c.Write(context.Background(), websocket.MessageBinary, protocol.BodyFrame{
				FrameType: protocol.FrameStreamData, ReqID: h.ReqID, Payload: []byte(fmt.Sprintf("hello-%d", h.ReqID)),
			}.Encode())
		}
	}))
	defer srv.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	cc, _, err := websocket.Dial(ctx, "ws"+srv.URL[len("http"):], nil)
	if err != nil {
		t.Fatal(err)
	}
	defer cc.CloseNow()
	cc.SetReadLimit(1 << 20)

	cs := &clientSession{
		conn:        cc,
		targets:     map[string]string{"t1": echo.Addr().String()},
		streamConns: map[uint64]net.Conn{},
	}
	// Gercek okuma dongusu gibi: ikili cerceveleri sirayla routeStreamData'ya ver.
	go func() {
		for {
			typ, data, err := cc.Read(ctx)
			if err != nil {
				return
			}
			if typ != websocket.MessageBinary {
				continue
			}
			f, err := protocol.DecodeBodyFrame(data)
			if err == nil && f.FrameType == protocol.FrameStreamData {
				cs.routeStreamData(f.FrameType, f.ReqID, f.Payload)
			}
		}
	}()

	for i := 1; i <= n; i++ {
		go cs.handleStreamOpen(protocol.StreamOpen{
			Type: protocol.TypeStreamOpen, ReqID: uint64(i), TunnelID: "t1", Proto: "tcp",
		})
	}
	select {
	case <-done:
	case <-time.After(15 * time.Second):
		mu.Lock()
		defer mu.Unlock()
		t.Fatalf("yanki gelen akis: %d/%d (ilk veri dusuyor)", len(got), n)
	}
}
