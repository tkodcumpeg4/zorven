package rawproxy

import (
	"context"
	"io"
	"net"
	"testing"
	"time"

	"github.com/tkodcumpeg4/zorven/server/tunsec"
)

type geoMap map[string]string

func (g geoMap) Country(ip string) (string, bool) {
	c, ok := g[ip]
	return c, ok
}

func secOf(t *testing.T, cfg tunsec.Config) map[string]*tunsec.Compiled {
	t.Helper()
	c := tunsec.Compile(cfg)
	if c == nil {
		t.Fatal("ayar derlenemedi")
	}
	return map[string]*tunsec.Compiled{"tun": c}
}

func TestAdmitGeo(t *testing.T) {
	m := newDoorMgr(t, nil)
	m.Geo = geoMap{"203.0.113.9": "AU", "198.51.100.4": "TR"}
	ctx := context.Background()

	// Ayar yok: herkes gecer.
	if ok, _ := m.admit(ctx, ti1, "203.0.113.9"); !ok {
		t.Fatal("ayarsiz tunel acik olmali")
	}
	m.SetSecurity(secOf(t, tunsec.Config{Geo: tunsec.GeoConfig{Mode: "allow", Countries: []string{"TR"}}}))
	if ok, _ := m.admit(ctx, ti1, "203.0.113.9"); ok {
		t.Fatal("AU allow listesinde degil, reddedilmeli")
	}
	if ok, _ := m.admit(ctx, ti1, "198.51.100.4"); !ok {
		t.Fatal("TR gecmeli")
	}
	// Bilinmeyen: vars. allow
	if ok, _ := m.admit(ctx, ti1, "192.0.2.1"); !ok {
		t.Fatal("unknown_is=allow gecmeli")
	}
	m.SetSecurity(secOf(t, tunsec.Config{Geo: tunsec.GeoConfig{Mode: "allow", Countries: []string{"TR"}, UnknownIs: "deny"}}))
	if ok, _ := m.admit(ctx, ti1, "192.0.2.1"); ok {
		t.Fatal("unknown_is=deny reddetmeli")
	}
	m.SetSecurity(secOf(t, tunsec.Config{Geo: tunsec.GeoConfig{Mode: "deny", Countries: []string{"AU"}}}))
	if ok, _ := m.admit(ctx, ti1, "203.0.113.9"); ok {
		t.Fatal("AU deny listesinde")
	}
	// Baska tunelin ayari etkilemez.
	other := ti1
	other.TunnelID = "baska"
	if ok, _ := m.admit(ctx, other, "203.0.113.9"); !ok {
		t.Fatal("baska tunel etkilenmemeli")
	}
	// Geo, kapi grant'inden ONCE uygulanir.
	g := &fakeGate{enabled: map[string]bool{"tun": true}, grants: map[string]time.Time{"tun|203.0.113.9": time.Now().Add(time.Hour)}}
	m.Door = g
	if ok, _ := m.admit(ctx, ti1, "203.0.113.9"); ok {
		t.Fatal("grant'li olsa da yasakli ulke girememeli")
	}
}

func TestTCPGeoRejectedOnListener(t *testing.T) {
	m, addr := rawTestMgr(t, func() (bridgeStream, bool, error) { return newFakeStream("", true), false, nil }, func(m *Manager) {
		m.Geo = geoMap{"127.0.0.1": "AU"}
	})
	m.SetSecurity(secOf(t, tunsec.Config{Geo: tunsec.GeoConfig{Mode: "deny", Countries: []string{"AU"}}}))

	c, err := net.Dial("tcp", addr)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	_ = c.SetReadDeadline(time.Now().Add(2 * time.Second))
	_, _ = c.Write([]byte("merhaba"))
	buf := make([]byte, 16)
	if n, err := c.Read(buf); err == nil || n != 0 {
		t.Fatalf("yasakli ulkeden baglanti kesilmeliydi: n=%d err=%v", n, err)
	}
	if m.LiveFlows("") != 0 {
		t.Fatal("reddedilen baglanti kayda girmemeli")
	}
}

// Z2: acik TCP akisi, tunel Geo ayari degisince yeniden degerlendirmede kesilir.
func TestReevaluateClosesOpenTCPFlow(t *testing.T) {
	m, addr := rawTestMgr(t, func() (bridgeStream, bool, error) { return newFakeStream("", true), false, nil }, func(m *Manager) {
		m.Geo = geoMap{"127.0.0.1": "TR"}
	})

	c, err := net.Dial("tcp", addr)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	_, _ = c.Write([]byte("ping"))
	buf := make([]byte, 4)
	_ = c.SetReadDeadline(time.Now().Add(2 * time.Second))
	if _, err := io.ReadFull(c, buf); err != nil {
		t.Fatalf("akis calismali: %v", err)
	}
	waitFor(t, "akis kaydi", func() bool { return m.LiveFlows("ten") == 1 })

	// Degisiklik yokken yeniden degerlendirme kesmez.
	if n := m.Reevaluate("ten"); n != 0 {
		t.Fatalf("ayar degismedi, kesilmemeli: %d", n)
	}
	// Baska kiraci etkilenmez.
	m.SetSecurity(secOf(t, tunsec.Config{Geo: tunsec.GeoConfig{Mode: "deny", Countries: []string{"TR"}}}))
	if n := m.Reevaluate("baska-kiraci"); n != 0 {
		t.Fatalf("baska kiraci: %d", n)
	}
	if n := m.Reevaluate("ten"); n != 1 {
		t.Fatalf("1 akis kesilmeliydi: %d", n)
	}
	_ = c.SetReadDeadline(time.Now().Add(2 * time.Second))
	if _, err := c.Read(buf); err == nil {
		t.Fatal("kesilen baglantidan okunamamali")
	}
	waitFor(t, "kayit temizligi", func() bool { return m.LiveFlows("") == 0 })
}

// Z2: kapi grant'i iptal edildiyse (admit artik ret) acik akis kesilir.
func TestReevaluateDoorGrantGone(t *testing.T) {
	g := &fakeGate{enabled: map[string]bool{"tun": true}, grants: map[string]time.Time{"tun|127.0.0.1": time.Now().Add(time.Hour)}}
	m, addr := rawTestMgr(t, func() (bridgeStream, bool, error) { return newFakeStream("", true), false, nil }, func(m *Manager) {
		m.Door = g
	})
	c, err := net.Dial("tcp", addr)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	_, _ = c.Write([]byte("x"))
	_ = c.SetReadDeadline(time.Now().Add(2 * time.Second))
	buf := make([]byte, 1)
	if _, err := io.ReadFull(c, buf); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "akis kaydi", func() bool { return m.LiveFlows("ten") == 1 })
	blockedBefore := g.blocked.Load()
	delete(g.grants, "tun|127.0.0.1")
	if n := m.Reevaluate("ten"); n != 1 {
		t.Fatalf("grant'i kalkan akis kesilmeliydi: %d", n)
	}
	if g.blocked.Load() != blockedBefore {
		t.Fatal("yeniden degerlendirme engelleme sayacini sisirmemeli")
	}
}

// Z2: dinleyicisi kaldirilan rezerve-port tunelinin acik akislari kesilir.
func TestReevaluateTunnelRemoved(t *testing.T) {
	m, addr := rawTestMgr(t, func() (bridgeStream, bool, error) { return newFakeStream("", true), false, nil })
	c, err := net.Dial("tcp", addr)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	_, _ = c.Write([]byte("x"))
	_ = c.SetReadDeadline(time.Now().Add(2 * time.Second))
	buf := make([]byte, 1)
	if _, err := io.ReadFull(c, buf); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "akis kaydi", func() bool { return m.LiveFlows("ten") == 1 })
	m.List = func(context.Context) ([]TunnelInfo, error) { return nil, nil }
	m.Reload()
	if n := m.Reevaluate("ten"); n != 1 {
		t.Fatalf("kaldirilan tunelin akisi kesilmeliydi: %d", n)
	}
}

func BenchmarkAdmitGeo(b *testing.B) {
	m := New(nil, nil, quietLog(), nil)
	m.Geo = geoMap{"198.51.100.4": "TR"}
	c := tunsec.Compile(tunsec.Config{Geo: tunsec.GeoConfig{Mode: "allow", Countries: []string{"TR", "DE"}}})
	m.SetSecurity(map[string]*tunsec.Compiled{"tun": c})
	ctx := context.Background()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		if ok, _ := m.admit(ctx, ti1, "198.51.100.4"); !ok {
			b.Fatal("gecmeliydi")
		}
	}
}
