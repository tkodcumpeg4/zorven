package rawproxy

import (
	"context"
	"errors"
	"io"
	"net"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/tkodcumpeg4/zorven/server/tunnel"
	"github.com/tkodcumpeg4/zorven/shared/protocol"
)

// fakeStream, ajan tarafini taklit eder: ziyaretciden gelen baytlari upper'a
// verir; echo ise aynen geri yollar.
type fakeStream struct {
	accept    chan protocol.StreamAck
	fromLocal chan []byte
	closed    chan struct{}
	once      sync.Once
	echo      bool
	got       atomic.Int64
}

func newFakeStream(code string, echo bool) *fakeStream {
	f := &fakeStream{accept: make(chan protocol.StreamAck, 1), fromLocal: make(chan []byte, 64),
		closed: make(chan struct{}), echo: echo}
	f.accept <- protocol.StreamAck{Code: code}
	return f
}

func (f *fakeStream) Accept() <-chan protocol.StreamAck { return f.accept }
func (f *fakeStream) FromLocal() <-chan []byte          { return f.fromLocal }
func (f *fakeStream) Closed() <-chan struct{}           { return f.closed }
func (f *fakeStream) Close(string)                      { f.once.Do(func() { close(f.closed) }) }
func (f *fakeStream) Send(_ context.Context, d []byte) error {
	f.got.Add(int64(len(d)))
	if f.echo {
		f.fromLocal <- append([]byte(nil), d...)
	}
	return nil
}

// rawTestMgr, sahte akisla gercek TCP dinleyicisi acan yonetici doner.
func rawTestMgr(t *testing.T, open func() (bridgeStream, bool, error)) (*Manager, string) {
	t.Helper()
	p := freePort(t)
	ti := TunnelInfo{TunnelID: "tun", TenantID: "ten", ClientID: "cli", Proto: "tcp", Port: p, BindAddr: "127.0.0.1"}
	m := New(tunnel.NewHub(), nil, quietLog(), func(context.Context) ([]TunnelInfo, error) { return []TunnelInfo{ti}, nil })
	m.openStream = func(context.Context, TunnelInfo, string) (bridgeStream, bool, error) { return open() }
	m.Start(context.Background())
	t.Cleanup(m.closeAll)
	return m, net.JoinHostPort("127.0.0.1", strconv.Itoa(p))
}

func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	for i := 0; i < 200; i++ {
		if cond() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("zaman asimi: %s", what)
}

func TestTCPStatsAcceptBytesDuration(t *testing.T) {
	m, addr := rawTestMgr(t, func() (bridgeStream, bool, error) { return newFakeStream("", true), false, nil })

	c, err := net.Dial("tcp", addr)
	if err != nil {
		t.Fatal(err)
	}
	payload := make([]byte, 5000)
	if _, err := c.Write(payload); err != nil {
		t.Fatal(err)
	}
	buf := make([]byte, 5000)
	if _, err := io.ReadFull(c, buf); err != nil {
		t.Fatal(err)
	}
	live, ok := m.TCPStats("tun")
	if !ok || live.Active != 1 || live.ConnsTotal != 1 {
		t.Fatalf("canli (aktif): %+v", live)
	}
	if live.BytesIn != 5000 || live.BytesOut != 5000 {
		t.Fatalf("bayt sayaclari: %+v", live)
	}
	time.Sleep(30 * time.Millisecond)
	c.Close()
	waitFor(t, "baglanti kapanisi", func() bool { l, _ := m.TCPStats("tun"); return l.Active == 0 })

	live, _ = m.TCPStats("tun")
	if live.DurMaxMs < 30 || live.DurAvgMs < 30 {
		t.Fatalf("sure: %+v", live)
	}
	if live.Port == 0 || live.Rejected != 0 {
		t.Fatalf("port/red: %+v", live)
	}

	rows := m.collectTCPMinutes(time.Unix(60, 0).UTC())
	if len(rows) != 1 {
		t.Fatalf("satir: %+v", rows)
	}
	r := rows[0]
	if r.ConnsNew != 1 || r.ConnsPeak != 1 || r.BytesIn != 5000 || r.BytesOut != 5000 || r.DurCount != 1 || r.DurMaxMs < 30 {
		t.Fatalf("dakika: %+v", r)
	}
	// Sessiz ikinci dakika yazilmaz; tepe artik aktife (0) inmis olmali.
	if rows := m.collectTCPMinutes(time.Unix(120, 0).UTC()); len(rows) != 0 {
		t.Fatalf("sessiz dakika: %+v", rows)
	}
}

func TestTCPStatsPeakConcurrent(t *testing.T) {
	m, addr := rawTestMgr(t, func() (bridgeStream, bool, error) { return newFakeStream("", false), false, nil })
	var conns []net.Conn
	for i := 0; i < 3; i++ {
		c, err := net.Dial("tcp", addr)
		if err != nil {
			t.Fatal(err)
		}
		conns = append(conns, c)
	}
	waitFor(t, "3 aktif", func() bool { l, _ := m.TCPStats("tun"); return l.Active == 3 })
	for _, c := range conns {
		c.Close()
	}
	waitFor(t, "0 aktif", func() bool { l, _ := m.TCPStats("tun"); return l.Active == 0 })
	rows := m.collectTCPMinutes(time.Unix(60, 0).UTC())
	if len(rows) != 1 || rows[0].ConnsPeak != 3 || rows[0].ConnsNew != 3 {
		t.Fatalf("tepe: %+v", rows)
	}
}

func TestTCPStatsRejectReasons(t *testing.T) {
	// Yerel hedefe baglanilamadi.
	m, addr := rawTestMgr(t, func() (bridgeStream, bool, error) { return newFakeStream("dial_failed", false), false, nil })
	dialAndWait(t, addr)
	waitFor(t, "dial reddi", func() bool { l, _ := m.TCPStats("tun"); return l.RejectedBy[TCPRejectDial] == 1 })

	// Akis acilamadi.
	m, addr = rawTestMgr(t, func() (bridgeStream, bool, error) { return nil, false, errors.New("x") })
	dialAndWait(t, addr)
	waitFor(t, "akis reddi", func() bool { l, _ := m.TCPStats("tun"); return l.RejectedBy[TCPRejectDial] == 1 })

	// Istemci cevrimdisi.
	m, addr = rawTestMgr(t, func() (bridgeStream, bool, error) { return nil, true, nil })
	dialAndWait(t, addr)
	waitFor(t, "offline reddi", func() bool { l, _ := m.TCPStats("tun"); return l.RejectedBy[TCPRejectOffline] == 1 })

	// Kapi etkin, grant yok.
	m, addr = rawTestMgr(t, func() (bridgeStream, bool, error) { return newFakeStream("", false), false, nil })
	gate := &fakeGate{enabled: map[string]bool{"tun": true}}
	m.Door = gate
	dialAndWait(t, addr)
	waitFor(t, "kapi reddi", func() bool { l, _ := m.TCPStats("tun"); return l.RejectedBy[TCPRejectDoor] == 1 })
	live, _ := m.TCPStats("tun")
	if live.Rejected != 1 || live.ConnsTotal != 0 {
		t.Fatalf("toplam: %+v", live)
	}
	rows := m.collectTCPMinutes(time.Unix(60, 0).UTC())
	if len(rows) != 1 || rows[0].Rejected != 1 || rows[0].RejectedDoor != 1 || rows[0].ConnsNew != 0 {
		t.Fatalf("dakika: %+v", rows)
	}
}

// IP izin listesi reddi: dogrudan bridgeTCP, gercek soket cifti uzerinden.
func TestTCPStatsIPDenied(t *testing.T) {
	m := newDoorMgr(t, nil, "203.0.113.0/24") // 127.0.0.1 listede degil
	m.openStream = func(context.Context, TunnelInfo, string) (bridgeStream, bool, error) {
		return newFakeStream("", false), false, nil
	}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Skip(err)
	}
	defer ln.Close()
	go func() {
		c, _ := net.Dial("tcp", ln.Addr().String())
		if c != nil {
			defer c.Close()
			io.Copy(io.Discard, c)
		}
	}()
	srv, err := ln.Accept()
	if err != nil {
		t.Fatal(err)
	}
	m.HandleSNIConn(srv, ti1)
	live, ok := m.TCPStats("tun")
	if ok && live.RejectedBy[TCPRejectIP] != 1 {
		t.Fatalf("ip reddi: %+v", live)
	}
	if !ok {
		// Dinleyici olmayan SNI yolunda da sayac olusur.
		v, has := m.tcpStats.Load("tun")
		if !has || v.(*tcpStat).rejIP.Load() != 1 {
			t.Fatal("ip reddi sayilmadi")
		}
	}
}

func dialAndWait(t *testing.T, addr string) {
	t.Helper()
	c, err := net.Dial("tcp", addr)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	_ = c.SetReadDeadline(time.Now().Add(2 * time.Second))
	_, _ = c.Read(make([]byte, 1)) // sunucu kapatinca EOF
}

func TestTCPStatsUnknownTunnel(t *testing.T) {
	m := New(nil, nil, quietLog(), nil)
	if _, ok := m.TCPStats("yok"); ok {
		t.Fatal("bilinmeyen tunel icin ok=false beklenir")
	}
}
