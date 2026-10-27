package netctl

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/tkodcumpeg4/zorven/shared/protocol"
)

func dnsHas(st *protocol.NetState, name, ip string) bool {
	if st.DNS == nil {
		return false
	}
	for _, r := range st.DNS.Records {
		if r.Name == name && r.IP == ip {
			return true
		}
	}
	return false
}

// Z5: Hooks.TunRules ile gelen politika turevi kural ACL + es + ozel kaynak DNS
// kaydi uretir; kosul bozulunca (kanca kuraldan vazgecer) Recompute ile hepsi duser.
func TestTunHookRulesACLDNSAndRevoke(t *testing.T) {
	h := newHarness(t)
	var on atomic.Bool
	on.Store(true)
	var gotClients atomic.Value
	h.m.Hooks.TunRules = func(tenantID string, clients []string) []TunRule {
		gotClients.Store(clients)
		if !on.Load() {
			return nil
		}
		return []TunRule{{
			ID: "pol:p1:res:db.internal", Name: "app->db", Proto: "tcp", Ports: "5432",
			Origin: "policy:p1", SrcDevices: []string{"lap"}, DstDevices: []string{"nas"}, Resource: "db.internal",
		}}
	}
	nas := h.attach(tunDev("t1", "nas", "NAS Box"))
	lap := h.attach(tunDev("t1", "lap", "laptop"))
	pc := h.attach(tunDev("t1", "pc", "pc"))
	setMode(t, nas, true)
	setMode(t, lap, true)
	setMode(t, pc, true)
	nas.drain()
	lap.drain()
	pc.drain()
	if cl, _ := gotClients.Load().([]string); len(cl) != 3 {
		t.Fatalf("kanca Mod B cihaz listesini almali: %v", cl)
	}
	if !hasRule(nas.state.ACL, "tcp", "5432", lap.reg.IP) {
		t.Fatalf("nas ACL'inde lap->5432 olmali: %+v", nas.state.ACL)
	}
	if hasRule(nas.state.ACL, "tcp", "5432", pc.reg.IP) {
		t.Fatal("kaynak olmayan cihaz ACL'e girmemeli")
	}
	if !nas.hasPeer(lap.pub) || !lap.hasPeer(nas.pub) || pc.hasPeer(nas.pub) || nas.hasPeer(pc.pub) {
		t.Fatal("yalniz kaynak<->hedef esleri gorunmeli")
	}
	if !dnsHas(lap.state, "db.internal", nas.reg.IP) {
		t.Fatalf("kaynak cihaz db.internal kaydini almali: %+v", lap.state.DNS)
	}
	if pc.state.DNS != nil && len(pc.state.DNS.Records) != 0 {
		t.Fatalf("yetkisiz cihaz kaynak adini gormemeli: %+v", pc.state.DNS)
	}
	if nas.state.DNS != nil && len(nas.state.DNS.Records) != 0 {
		t.Fatalf("hedef kendi kaynagini kayit olarak almamali: %+v", nas.state.DNS)
	}

	// Kosul bozuldu (posture/zaman): kanca kural vermez, Recompute peer'i ve ACL'i kaldirir.
	on.Store(false)
	h.m.Recompute("t1")
	nas.drain()
	lap.drain()
	if len(nas.state.ACL) != 0 || nas.hasPeer(lap.pub) || lap.hasPeer(nas.pub) {
		t.Fatalf("kosul bozulunca ACL/es kalmamali: acl=%+v", nas.state.ACL)
	}
	if dnsHas(lap.state, "db.internal", nas.reg.IP) {
		t.Fatal("kosul bozulunca DNS kaydi kalkmali")
	}
	// Kosul duzelince geri gelir.
	on.Store(true)
	h.m.Recompute("t1")
	nas.drain()
	lap.drain()
	if !hasRule(nas.state.ACL, "tcp", "5432", lap.reg.IP) || !dnsHas(lap.state, "db.internal", nas.reg.IP) {
		t.Fatal("kosul duzelince erisim geri gelmeli")
	}
}

func TestTunHookDNSOnlyAndOriginless(t *testing.T) {
	h := newHarness(t)
	h.m.Hooks.TunRules = func(string, []string) []TunRule {
		return []TunRule{
			// Origin'siz kural yok sayilir (elle kural taklidi yapamaz).
			{ID: "x", Src: Grantee{Type: GranteeGroup, ID: "everyone"}, Dst: "*", Proto: "any"},
			{ID: "pol:d:dns:db.internal", Origin: "policy:d", SrcDevices: []string{"lap"}, DstDevices: []string{"nas"},
				Resource: "db.internal", DNSOnly: true},
		}
	}
	nas := h.attach(tunDev("t1", "nas", "NAS Box"))
	lap := h.attach(tunDev("t1", "lap", "laptop"))
	setMode(t, nas, true)
	setMode(t, lap, true)
	nas.drain()
	lap.drain()
	if len(nas.state.ACL) != 0 || nas.hasPeer(lap.pub) {
		t.Fatalf("origin'siz kanca kurali ve DNSOnly ACL/es uretmemeli: %+v", nas.state.ACL)
	}
	if !dnsHas(lap.state, "db.internal", nas.reg.IP) {
		t.Fatalf("DNSOnly kural kaynak cihaza ad kaydi vermeli: %+v", lap.state.DNS)
	}
	eff, err := h.m.EffectiveTunRules(context.Background(), "t1")
	if err != nil || len(eff) != 1 || eff[0].ID != "pol:d:dns:db.internal" {
		t.Fatalf("etkin kurallar: %+v %v", eff, err)
	}
}

// Elle kurallar ve politika turevi kurallar birlikte calisir; kanca nil ise
// eski davranis (acik cekirdek).
func TestTunNilHookKeepsManualRules(t *testing.T) {
	h := newHarness(t)
	nas := h.attach(tunDev("t1", "nas", "NAS Box"))
	lap := h.attach(tunDev("t1", "lap", "laptop"))
	setMode(t, nas, true)
	setMode(t, lap, true)
	if _, err := h.m.CreateTunRule(context.Background(), "t1", TunRuleInput{
		Src: Grantee{Type: GranteeDevice, ID: "lap"}, Dst: "nas", Proto: "tcp", Ports: "22",
	}, "t"); err != nil {
		t.Fatal(err)
	}
	nas.drain()
	if !hasRule(nas.state.ACL, "tcp", "22", lap.reg.IP) {
		t.Fatal("kanca nil iken elle kural calismali")
	}
}

func alogDev(t, c, name string) Identity {
	id := tunDev(t, c, name)
	id.ALog = true
	return id
}

func sendALog(t *testing.T, f *fake, entries ...protocol.NetAccessEntry) {
	t.Helper()
	b, _ := protocol.Marshal(protocol.NetAccessLog{Type: protocol.TypeNetAccessLog, WindowSec: 30, Entries: entries}, protocol.TypeNetAccessLog)
	if err := f.nc.HandleControl(context.Background(), protocol.TypeNetAccessLog, b); err != nil {
		t.Fatal(err)
	}
}

func TestAccessLogStoredEnrichedAndRateLimited(t *testing.T) {
	h := newHarness(t)
	now := time.Date(2026, 10, 26, 12, 0, 0, 0, time.UTC)
	h.m.Now = func() time.Time { return now }
	var audits atomic.Int32
	h.m.Hooks.Audit = func(_, _, action, _, _ string) {
		if action == "net.access.deny" {
			audits.Add(1)
		}
	}
	h.m.Hooks.TunRules = func(string, []string) []TunRule {
		return []TunRule{{ID: "pol:p1:res:db.internal", Proto: "tcp", Ports: "5432", Origin: "policy:p1",
			SrcDevices: []string{"lap"}, DstDevices: []string{"nas"}, Resource: "db.internal"}}
	}
	nas := h.attach(alogDev("t1", "nas", "NAS Box"))
	lap := h.attach(alogDev("t1", "lap", "laptop"))
	if !nas.reg.ALog {
		t.Fatal("alog pazarlanmali")
	}
	setMode(t, nas, true)
	setMode(t, lap, true)
	good := protocol.NetAccessEntry{SrcIP: lap.reg.IP, Proto: "tcp", Port: 5432, Decision: "allow", Rule: "pol:p1:res:db.internal", Flows: 2, Packets: 40, Bytes: 9000}
	deny := protocol.NetAccessEntry{SrcIP: lap.reg.IP, Proto: "tcp", Port: 22, Decision: "deny", Flows: 1, Packets: 3}
	bad := []protocol.NetAccessEntry{
		{SrcIP: "not-ip", Proto: "tcp", Decision: "allow"},
		{SrcIP: lap.reg.IP, Proto: "gre", Decision: "allow"},
		{SrcIP: lap.reg.IP, Proto: "tcp", Decision: "maybe"},
		{SrcIP: lap.reg.IP, Proto: "tcp", Port: 70000, Decision: "allow"},
		{SrcIP: lap.reg.IP, Proto: "tcp", Decision: "allow", Packets: -1},
	}
	sendALog(t, nas, append([]protocol.NetAccessEntry{good, deny}, bad...)...)
	recs, _ := h.m.AccessLog(context.Background(), "t1", AccessQuery{})
	if len(recs) != 2 {
		t.Fatalf("yalniz gecerli girdiler saklanmali: %+v", recs)
	}
	var allow AccessRecord
	for _, r := range recs {
		if r.Decision == "allow" {
			allow = r
		}
	}
	if allow.SrcName != "laptop" || allow.DstName != "NAS Box" && allow.DstName != "nas-box" || allow.Resource != "db.internal" ||
		allow.Bytes != 9000 || allow.DstClientID != "nas" || allow.SrcClientID != "lap" {
		t.Fatalf("zenginlestirme: %+v", allow)
	}
	if audits.Load() != 1 {
		t.Fatalf("ret ozeti denetime yazilmali: %d", audits.Load())
	}
	// Hiz siniri: 10 sn icinde ikinci mesaj atilir.
	now = now.Add(5 * time.Second)
	sendALog(t, nas, good)
	if recs, _ = h.m.AccessLog(context.Background(), "t1", AccessQuery{}); len(recs) != 2 {
		t.Fatalf("hiz siniri asildi: %d", len(recs))
	}
	now = now.Add(6 * time.Second)
	sendALog(t, nas, good)
	if recs, _ = h.m.AccessLog(context.Background(), "t1", AccessQuery{}); len(recs) != 3 {
		t.Fatalf("aralik gecince kabul edilmeli: %d", len(recs))
	}
	// Filtreler.
	only, _ := h.m.AccessLog(context.Background(), "t1", AccessQuery{Decision: "deny"})
	if len(only) != 1 || only[0].Port != 22 {
		t.Fatalf("deny filtresi: %+v", only)
	}
	res, _ := h.m.AccessLog(context.Background(), "t1", AccessQuery{Resource: "db.internal"})
	if len(res) != 2 {
		t.Fatalf("resource filtresi: %+v", res)
	}
}

func TestAccessLogIgnoredWithoutNegotiation(t *testing.T) {
	h := newHarness(t)
	nas := h.attach(tunDev("t1", "nas", "NAS Box")) // ALog pazarlanmadi
	setMode(t, nas, true)
	sendALog(t, nas, protocol.NetAccessEntry{SrcIP: "100.64.0.9", Proto: "tcp", Decision: "deny", Packets: 1})
	if recs, _ := h.m.AccessLog(context.Background(), "t1", AccessQuery{}); len(recs) != 0 {
		t.Fatal("pazarlanmayan dugumden gunluk kabul edilmemeli")
	}
	// Mod B'de olmayan (Mod A) dugum de gunluk gonderemez.
	a := h.attach(alogDev("t1", "lap", "laptop"))
	sendALog(t, a, protocol.NetAccessEntry{SrcIP: "100.64.0.9", Proto: "tcp", Decision: "deny", Packets: 1})
	if recs, _ := h.m.AccessLog(context.Background(), "t1", AccessQuery{}); len(recs) != 0 {
		t.Fatal("Mod B'de olmayan dugumden gunluk kabul edilmemeli")
	}
}

func TestAccessLimiterPerTenant(t *testing.T) {
	var l accessLimiter
	now := time.Now()
	if n := l.take("t1", accessTenantPerMin-10, now); n != accessTenantPerMin-10 {
		t.Fatalf("n=%d", n)
	}
	if n := l.take("t1", 50, now); n != 10 {
		t.Fatalf("kalan kota 10 olmali: %d", n)
	}
	if n := l.take("t1", 5, now); n != 0 {
		t.Fatalf("kota dolu: %d", n)
	}
	if n := l.take("t2", 5, now); n != 5 {
		t.Fatal("kiracilar bagimsiz")
	}
	if n := l.take("t1", 5, now.Add(time.Minute)); n != 5 {
		t.Fatal("pencere yenilenmeli")
	}
}
