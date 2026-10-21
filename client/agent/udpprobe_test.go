package agent

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"

	"github.com/tkodcumpeg4/zorven/shared/protocol"
)

// probePair, istemci oturumu ile "sunucu" ucunu baglayan gercek bir WS cifti kurar.
func probePair(t *testing.T) (*clientSession, *websocket.Conn, context.Context) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	t.Cleanup(cancel)
	srvConn := make(chan *websocket.Conn, 1)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c, err := websocket.Accept(w, r, nil)
		if err != nil {
			return
		}
		srvConn <- c
		<-r.Context().Done()
	}))
	t.Cleanup(srv.Close)
	cli, _, err := websocket.Dial(ctx, "ws"+strings.TrimPrefix(srv.URL, "http"), nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { cli.CloseNow() })
	cs := newClientSession(cli, "http://127.0.0.1:1", slog.New(slog.NewTextHandler(io.Discard, nil)))
	cs.udpProbe = true
	return cs, <-srvConn, ctx
}

func TestHelloAdvertisesUDPProbe(t *testing.T) {
	a := &Agent{}
	found := false
	for _, f := range a.helloFeatures() {
		if f == protocol.FeatureUDPProbe {
			found = true
		}
	}
	if !found {
		t.Fatal("hello FeatureUDPProbe ilan etmeli")
	}
}

func TestEchoUDPProbeReturnsSameFields(t *testing.T) {
	cs, srv, ctx := probePair(t)
	cs.echoUDPProbe(ctx, protocol.UDPProbe{Type: protocol.TypeUDPProbe, TunnelID: "tun1", Seq: 42, TS: 123456789})
	_, data, err := srv.Read(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var e protocol.UDPProbeEcho
	if err := json.Unmarshal(data, &e); err != nil {
		t.Fatal(err)
	}
	if e.Type != protocol.TypeUDPProbeEcho || e.TunnelID != "tun1" || e.Seq != 42 || e.TS != 123456789 {
		t.Fatalf("yanki aynen donmeli: %+v", e)
	}
}

func TestDropCounterTakeResets(t *testing.T) {
	var c udpDropCounter
	if c.take() != nil {
		t.Fatal("bos sayac nil donmeli")
	}
	c.add("a", 2)
	c.add("a", 3)
	c.add("b", 1)
	c.add("", 9)
	c.add("c", 0)
	got := c.take()
	if len(got) != 2 || got["a"] != 5 || got["b"] != 1 {
		t.Fatalf("toplam yanlis: %v", got)
	}
	if c.take() != nil {
		t.Fatal("take sayaci sifirlamali")
	}
}

func TestFlushUDPAgentStats(t *testing.T) {
	cs, srv, ctx := probePair(t)
	cs.flushUDPAgentStats(ctx) // sifir: mesaj yok
	cs.udpDrops.add("tun1", 3)
	cs.flushUDPAgentStats(ctx)
	_, data, err := srv.Read(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var m protocol.UDPAgentStats
	if err := json.Unmarshal(data, &m); err != nil {
		t.Fatal(err)
	}
	if m.Type != protocol.TypeUDPAgentStats || m.Dropped["tun1"] != 3 {
		t.Fatalf("bildirim yanlis: %+v", m)
	}
	if cs.udpDrops.take() != nil {
		t.Fatal("bildirimden sonra sayac sifirlanmali")
	}
}

func TestUDPWriteFailureCountsDrop(t *testing.T) {
	cs, _, _ := probePair(t)
	a, b := net.Pipe()
	b.Close()
	cs.registerStream(7, a)
	cs.streamMu.Lock()
	cs.streamTunnels[7] = "tun1"
	cs.streamMu.Unlock()
	cs.routeStreamData(protocol.FrameDatagram, 7, []byte("x"))
	got := cs.udpDrops.take()
	if got["tun1"] != 1 {
		t.Fatalf("yerel yazma hatasi dusurme sayilmali: %v", got)
	}
	// TCP akisi (tunel kaydi yok) sayilmaz.
	c, d := net.Pipe()
	d.Close()
	cs.registerStream(8, c)
	cs.routeStreamData(protocol.FrameStreamData, 8, []byte("x"))
	if cs.udpDrops.take() != nil {
		t.Fatal("TCP akisi UDP sayacina girmemeli")
	}
}
