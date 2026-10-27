package rawproxy

import (
	"context"
	"io"
	"net"
	"runtime"
	"sync"
	"testing"
	"time"

	"golang.org/x/time/rate"

	"github.com/tkodcumpeg4/zorven/server/bandwidth"
	"github.com/tkodcumpeg4/zorven/server/tunnel"
)

// fakeUsage, bandwidth.UsageSource'un sahte uygulamasi: durumu sabit, kayitlari toplar.
type fakeUsage struct {
	mu      sync.Mutex
	state   *bandwidth.TenantRuntimeState
	in, out map[bandwidth.UsageType]int64
}

func newFakeUsage(st *bandwidth.TenantRuntimeState) *fakeUsage {
	return &fakeUsage{state: st, in: map[bandwidth.UsageType]int64{}, out: map[bandwidth.UsageType]int64{}}
}

func (f *fakeUsage) GetOrCreateState(context.Context, string) (*bandwidth.TenantRuntimeState, error) {
	return f.state, nil
}

func (f *fakeUsage) Record(_ context.Context, e bandwidth.UsageEvent) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.in[e.Type] += e.BytesIn
	f.out[e.Type] += e.BytesOut
	return nil
}

func (f *fakeUsage) get(t bandwidth.UsageType) (int64, int64) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.in[t], f.out[t]
}

func unlimitedState() *bandwidth.TenantRuntimeState {
	return bandwidth.NewTenantRuntimeState("ten", "enterprise", 0, 0, 0, 0)
}

// limitedState, saniyede bytesPerSec bayt (burst = 1 sn) izin veren durum.
func limitedState(bytesPerSec int) *bandwidth.TenantRuntimeState {
	s := bandwidth.NewTenantRuntimeState("ten", "free", 0, 0, 10, 1)
	s.GetLimiter().SetLimit(rate.Limit(bytesPerSec))
	s.GetLimiter().SetBurst(bytesPerSec)
	return s
}

func TestTCPRecordsBothDirectionsOnClose(t *testing.T) {
	fu := newFakeUsage(unlimitedState())
	_, addr := rawTestMgr(t, func() (bridgeStream, bool, error) { return newFakeStream("", true), false, nil }, func(m *Manager) {
		m.Usage = fu
	})

	c, err := net.Dial("tcp", addr)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := c.Write(make([]byte, 5000)); err != nil {
		t.Fatal(err)
	}
	if _, err := io.ReadFull(c, make([]byte, 5000)); err != nil {
		t.Fatal(err)
	}
	c.Close()
	waitFor(t, "kapanista kayit", func() bool {
		in, out := fu.get(bandwidth.UsageTypeTCP)
		return in == 5000 && out == 5000
	})
}

func TestTCPThrottleCapsThroughput(t *testing.T) {
	// 20 KB/s, burst 20 KB: 60 KB aktarim en az ~2 sn surmeli.
	fu := newFakeUsage(limitedState(20 * 1024))
	m, addr := rawTestMgr(t, func() (bridgeStream, bool, error) { return newFakeStream("", false), false, nil }, func(m *Manager) {
		m.Usage = fu
	})
	c, err := net.Dial("tcp", addr)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	start := time.Now()
	if _, err := c.Write(make([]byte, 60*1024)); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 400; i++ {
		if l, _ := m.TCPStats("tun"); l.BytesIn >= 60*1024 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if l, _ := m.TCPStats("tun"); l.BytesIn < 60*1024 {
		t.Fatalf("aktarim tamamlanmadi: %d", l.BytesIn)
	}
	if el := time.Since(start); el < 1500*time.Millisecond {
		t.Fatalf("kisitlama yok: %v", el)
	}
}

func TestTCPUnlimitedNoWait(t *testing.T) {
	fu := newFakeUsage(unlimitedState())
	m, addr := rawTestMgr(t, func() (bridgeStream, bool, error) { return newFakeStream("", false), false, nil }, func(m *Manager) {
		m.Usage = fu
	})
	c, err := net.Dial("tcp", addr)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	start := time.Now()
	c.Write(make([]byte, 2*1024*1024))
	waitFor(t, "akis", func() bool { l, _ := m.TCPStats("tun"); return l.BytesIn >= 2*1024*1024 })
	if el := time.Since(start); el > time.Second {
		t.Fatalf("sinirsiz planda yavasladi: %v", el)
	}
}

func TestTCPNoGoroutineLeak(t *testing.T) {
	fu := newFakeUsage(unlimitedState())
	_, addr := rawTestMgr(t, func() (bridgeStream, bool, error) { return newFakeStream("", true), false, nil }, func(m *Manager) {
		m.Usage = fu
	})
	before := runtime.NumGoroutine()
	for i := 0; i < 20; i++ {
		c, err := net.Dial("tcp", addr)
		if err != nil {
			t.Fatal(err)
		}
		c.Write([]byte("x"))
		io.ReadFull(c, make([]byte, 1))
		c.Close()
	}
	waitFor(t, "goroutine'ler", func() bool { return runtime.NumGoroutine() <= before+2 })
}

// udpHarness: gercek UDP dinleyici + onceden kurulmus sahte-akisli flow.
func udpHarness(t *testing.T, fu *fakeUsage) (*udpListener, *net.UDPConn, *fakeStream) {
	t.Helper()
	m := New(tunnel.NewHub(), nil, quietLog(), nil)
	m.Usage = fu
	ul, err := m.newUDPListener("127.0.0.1", 0, TunnelInfo{TunnelID: "u", TenantID: "ten", Proto: "udp"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(ul.close)
	cli, err := net.DialUDP("udp", nil, ul.conn.LocalAddr().(*net.UDPAddr))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { cli.Close() })
	src := cli.LocalAddr().(*net.UDPAddr)
	fs := newFakeStream("", false)
	_, cancel := context.WithCancel(context.Background())
	f := &udpFlow{stream: fs, cancel: cancel, lastSeen: time.Now().UnixNano()}
	key := src.String()
	ul.mu.Lock()
	ul.flows[key] = f
	ul.mu.Unlock()
	go ul.pumpFromLocal(f, src, key)
	return ul, cli, fs
}

func TestUDPRecordsBothDirections(t *testing.T) {
	fu := newFakeUsage(unlimitedState())
	ul, cli, fs := udpHarness(t, fu)
	for i := 0; i < 10; i++ {
		cli.Write(make([]byte, 100))
	}
	waitFor(t, "gelen datagramlar", func() bool { return fs.got.Load() == 1000 })
	for i := 0; i < 5; i++ {
		fs.fromLocal <- make([]byte, 200)
	}
	buf := make([]byte, 2000)
	cli.SetReadDeadline(time.Now().Add(2 * time.Second))
	for i := 0; i < 5; i++ {
		if _, err := cli.Read(buf); err != nil {
			t.Fatal(err)
		}
	}
	ul.close() // flush
	in, out := fu.get(bandwidth.UsageTypeUDP)
	if in != 1000 || out != 1000 {
		t.Fatalf("udp kayit in=%d out=%d", in, out)
	}
}

func TestUDPThrottleDropsWhenExhausted(t *testing.T) {
	// 1000 B/s, burst 1000: 10x500 B -> ilk 2 gecer, kalanlar dusulur.
	fu := newFakeUsage(limitedState(1000))
	ul, cli, fs := udpHarness(t, fu)
	for i := 0; i < 10; i++ {
		cli.Write(make([]byte, 500))
	}
	waitFor(t, "dusen datagramlar", func() bool { return ul.stats.droppedRate.Load() >= 7 })
	if got := fs.got.Load(); got > 1500 {
		t.Fatalf("limit asildi: %d bayt iletildi", got)
	}
	ul.close()
	if in, _ := fu.get(bandwidth.UsageTypeUDP); in != fs.got.Load() {
		t.Fatalf("yalniz iletilen sayilmali: kayit=%d iletilen=%d", in, fs.got.Load())
	}
}

func TestUDPUnlimitedNoDrops(t *testing.T) {
	fu := newFakeUsage(unlimitedState())
	ul, cli, fs := udpHarness(t, fu)
	for i := 0; i < 50; i++ {
		cli.Write(make([]byte, 1000))
	}
	waitFor(t, "iletim", func() bool { return fs.got.Load() == 50000 })
	if ul.stats.droppedRate.Load() != 0 {
		t.Fatal("sinirsiz planda dusme olmamali")
	}
}
