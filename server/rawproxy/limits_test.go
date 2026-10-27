package rawproxy

import (
	"context"
	"io"
	"net"
	"testing"
	"time"
)

func testLimits(perTunnel, perIP int, idle time.Duration) *connLimits {
	return &connLimits{max: 0, perTunnel: perTunnel, perIP: perIP, idle: idle, byTunnel: map[string]int{}, byIP: map[string]int{}}
}

func TestConnLimitsAcquireRelease(t *testing.T) {
	l := testLimits(3, 2, 0)
	r1, ok1 := l.acquire("t", "1.1.1.1")
	_, ok2 := l.acquire("t", "1.1.1.1")
	if _, ok := l.acquire("t", "1.1.1.1"); !ok1 || !ok2 || ok {
		t.Fatal("IP basina 2 siniri")
	}
	if _, ok := l.acquire("t", "2.2.2.2"); !ok {
		t.Fatal("baska IP kabul edilmeli")
	}
	if _, ok := l.acquire("t", "3.3.3.3"); ok {
		t.Fatal("tunel basina 3 siniri")
	}
	r1()
	r1() // ikinci cagri etkisiz
	if _, ok := l.acquire("t", "3.3.3.3"); !ok {
		t.Fatal("birakilan yer yeniden kullanilmali")
	}
	if l.total != 3 {
		t.Fatalf("toplam %d", l.total)
	}
}

func TestRawTCPPerIPLimitRejects(t *testing.T) {
	m, addr := rawTestMgr(t, func() (bridgeStream, bool, error) { return newFakeStream("", false), false, nil }, func(m *Manager) {
		m.limits = testLimits(0, 2, 0)
	})
	var keep []net.Conn
	for i := 0; i < 2; i++ {
		c, err := net.Dial("tcp", addr)
		if err != nil {
			t.Fatal(err)
		}
		keep = append(keep, c)
	}
	waitFor(t, "2 aktif", func() bool { l, _ := m.TCPStats("tun"); return l.Active == 2 })
	c3, err := net.Dial("tcp", addr)
	if err != nil {
		t.Fatal(err)
	}
	_ = c3.SetReadDeadline(time.Now().Add(2 * time.Second))
	if _, err := c3.Read(make([]byte, 1)); err != io.EOF {
		t.Fatalf("ucuncu baglanti kapatilmali: %v", err)
	}
	for _, c := range keep {
		c.Close()
	}
}

func TestRawTCPIdleTimeoutCloses(t *testing.T) {
	_, addr := rawTestMgr(t, func() (bridgeStream, bool, error) { return newFakeStream("", true), false, nil }, func(m *Manager) {
		m.limits = testLimits(0, 0, 1500*time.Millisecond)
	})
	c, err := net.Dial("tcp", addr)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	// Etkinlik suresi uzatir: 1 sn sonra veri gonder, baglanti acik kalmali.
	time.Sleep(time.Second)
	if _, err := c.Write([]byte("x")); err != nil {
		t.Fatal(err)
	}
	buf := make([]byte, 1)
	if _, err := io.ReadFull(c, buf); err != nil {
		t.Fatalf("etkin baglanti kapanmamali: %v", err)
	}
	_ = c.SetReadDeadline(time.Now().Add(5 * time.Second))
	start := time.Now()
	if _, err := c.Read(buf); err == nil {
		t.Fatal("bosta baglanti kapanmali")
	}
	if d := time.Since(start); d > 4500*time.Millisecond {
		t.Fatalf("bosta kapanis gec: %v", d)
	}
	_ = context.Background()
}
