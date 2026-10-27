package netctl

import (
	"context"
	"net/netip"
	"testing"

	"github.com/tkodcumpeg4/zorven/shared/protocol"
)

func tunDev(t, c, name string) Identity {
	id := dev(t, c, name)
	id.Tun = true
	return id
}

func setMode(t *testing.T, f *fake, on bool) {
	t.Helper()
	b, _ := protocol.Marshal(protocol.NetMode{Type: protocol.TypeNetMode, TUN: on}, protocol.TypeNetMode)
	if err := f.nc.HandleControl(context.Background(), protocol.TypeNetMode, b); err != nil {
		t.Fatal(err)
	}
}

func hasRule(acl []protocol.NetACLRule, proto, ports, src string) bool {
	for _, r := range acl {
		if r.Proto != proto || r.Ports != ports {
			continue
		}
		for _, s := range r.Src {
			if s == src {
				return true
			}
		}
	}
	return false
}

func TestTunRulesPeersACLAndDNS(t *testing.T) {
	h := newHarness(t)
	nas := h.attach(tunDev("t1", "nas", "NAS Box"))
	lap := h.attach(tunDev("t1", "lap", "laptop"))
	if !nas.reg.Tun || nas.reg.Pool != DefaultPool.String() {
		t.Fatalf("kayit yaniti Tun/Pool eksik: %+v", nas.reg)
	}
	// Mod B'ye gecilmeden kural hicbir sey acmaz.
	if _, err := h.m.CreateTunRule(context.Background(), "t1", TunRuleInput{
		Src: Grantee{Type: GranteeDevice, ID: "lap"}, Dst: "nas", Proto: "tcp", Ports: "22",
	}, "test"); err != nil {
		t.Fatal(err)
	}
	nas.drain()
	lap.drain()
	if nas.hasPeer(lap.pub) || lap.hasPeer(nas.pub) {
		t.Fatal("Mod A dugumlerine kural tabanli es verilmemeli")
	}
	setMode(t, nas, true)
	setMode(t, lap, true)
	nas.drain()
	lap.drain()
	if !nas.hasPeer(lap.pub) || !lap.hasPeer(nas.pub) {
		t.Fatal("iki Mod B dugumu birbirini gormeli")
	}
	if !hasRule(nas.state.ACL, "tcp", "22", lap.reg.IP) {
		t.Fatalf("nas ACL'inde lap->22 olmali: %+v", nas.state.ACL)
	}
	if len(lap.state.ACL) != 0 {
		t.Fatalf("lap'a gelen kural yok (default deny): %+v", lap.state.ACL)
	}
	if nas.state.DNS == nil || nas.state.DNS.Zone != TunZone || nas.state.DNS.Resolver != "100.64.0.1" {
		t.Fatalf("dns: %+v", nas.state.DNS)
	}
	if !nas.state.Peers[lap.pub].Tun {
		t.Fatal("es Tun bayragi")
	}
	// Baska kiracidaki Mod B dugumu hicbir sey gormez.
	other := h.attach(tunDev("t2", "nas2", "NAS Box"))
	setMode(t, other, true)
	other.drain()
	if len(other.state.Peers) != 0 || len(other.state.ACL) != 0 {
		t.Fatal("kiracilar arasi sizinti")
	}
	// Kural silinince es ve ACL duser.
	rules, _ := h.m.TunRules(context.Background(), "t1")
	if len(rules) != 1 {
		t.Fatalf("kural sayisi %d", len(rules))
	}
	if err := h.m.DeleteTunRule(context.Background(), "t1", rules[0].ID, "test"); err != nil {
		t.Fatal(err)
	}
	nas.drain()
	lap.drain()
	if nas.hasPeer(lap.pub) || len(nas.state.ACL) != 0 {
		t.Fatalf("kural silinince es/ACL kalmamali: %+v", nas.state.ACL)
	}
	// Kural geri gelir, sonra Mod A'ya donunce ACL ve kural tabanli es duser.
	if _, err := h.m.CreateTunRule(context.Background(), "t1", TunRuleInput{
		Src: Grantee{Type: GranteeGroup, ID: "everyone"}, Dst: "*", Proto: "icmp",
	}, "test"); err != nil {
		t.Fatal(err)
	}
	nas.drain()
	if !hasRule(nas.state.ACL, "icmp", "", lap.reg.IP) || !nas.hasPeer(lap.pub) {
		t.Fatalf("icmp kurali: %+v", nas.state.ACL)
	}
	setMode(t, nas, false)
	nas.drain()
	lap.drain()
	if len(nas.state.ACL) != 0 || nas.hasPeer(lap.pub) || lap.hasPeer(nas.pub) {
		t.Fatalf("Mod A'ya donunce ACL/es kalmamali: acl=%+v", nas.state.ACL)
	}
}

func TestTunModeIgnoredWithoutFeature(t *testing.T) {
	h := newHarness(t)
	a := h.attach(dev("t1", "nas", "NAS Box")) // Tun pazarlanmadi
	if a.reg.Tun {
		t.Fatal("wg_tun pazarlanmadiysa Tun=false olmali")
	}
	setMode(t, a, true)
	a.drain()
	if a.state.DNS != nil || len(a.state.ACL) != 0 {
		t.Fatal("pazarlanmayan dugume Mod B bilgisi gonderilmemeli")
	}
}

func TestTunShareDerivedACL(t *testing.T) {
	h := newHarness(t)
	nas := h.attach(tunDev("t1", "nas", "NAS Box"))
	lap := h.attach(dev("t1", "lap", "laptop"))
	mkShare(t, h, "t1", "nas", 5432, Grantee{Type: GranteeGroup, ID: "everyone"})
	setMode(t, nas, true)
	nas.drain()
	lap.drain()
	if !hasRule(nas.state.ACL, "tcp", "5432", lap.reg.IP) {
		t.Fatalf("paylasimdan ACL turetilmeli: %+v", nas.state.ACL)
	}
}

func TestTunRuleValidation(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	bad := []TunRuleInput{
		{Src: Grantee{Type: GranteeUser, ID: "alice"}, Dst: "*", Proto: "tcp"},
		{Src: Grantee{Type: GranteeGroup, ID: "role:admin"}, Dst: "*", Proto: "tcp"},
		{Src: Grantee{Type: GranteeDevice, ID: "lap"}, Dst: "*", Proto: "sctp"},
		{Src: Grantee{Type: GranteeDevice, ID: "lap"}, Dst: "*", Proto: "tcp", Ports: "70000"},
		{Src: Grantee{Type: GranteeDevice, ID: "lap"}, Dst: "*", Proto: "icmp", Ports: "22"},
		{Src: Grantee{Type: GranteeDevice, ID: "yok"}, Dst: "*", Proto: "tcp"},
		{Src: Grantee{Type: GranteeDevice, ID: "lap"}, Dst: "yok", Proto: "tcp"},
	}
	for i, in := range bad {
		if _, err := h.m.CreateTunRule(ctx, "t1", in, "t"); err == nil {
			t.Errorf("%d: gecersiz kural kabul edildi: %+v", i, in)
		}
	}
	if _, err := h.m.CreateTunRule(ctx, "t1", TunRuleInput{Src: Grantee{Type: GranteeGroup, ID: "everyone"}, Proto: "tcp", Ports: "1000-2000"}, "t"); err != nil {
		t.Fatal(err)
	}
}

func TestSetNetwork(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	if err := h.m.SetNetwork(ctx, "t1", netip.MustParsePrefix("8.8.0.0/16"), "t"); err == nil {
		t.Fatal("genel aralik reddedilmeli")
	}
	if err := h.m.SetNetwork(ctx, "t1", netip.MustParsePrefix("10.99.0.0/16"), "t"); err != nil {
		t.Fatal(err)
	}
	a := h.attach(tunDev("t1", "nas", "NAS Box"))
	if a.reg.Pool != "10.99.0.0/16" || !netip.MustParsePrefix("10.99.0.0/16").Contains(netip.MustParseAddr(a.reg.IP)) {
		t.Fatalf("kiraci havuzu uygulanmadi: %+v", a.reg)
	}
	if err := h.m.SetNetwork(ctx, "t1", netip.MustParsePrefix("10.98.0.0/16"), "t"); err == nil {
		t.Fatal("dugum varken havuz degismemeli")
	}
	// Yeniden baslatma: havuz depodan yuklenir.
	m2 := New(h.st, h.m.dir, nil, nil)
	p, _ := m2.Network(ctx, "t1")
	if p.String() != "10.99.0.0/16" {
		t.Fatalf("havuz kalici degil: %s", p)
	}
	if ResolverIP(p).String() != "10.99.0.1" {
		t.Fatalf("resolver %s", ResolverIP(p))
	}
}
