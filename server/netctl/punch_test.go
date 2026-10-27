package netctl

import (
	"context"
	"net/netip"
	"testing"
	"time"

	"github.com/tkodcumpeg4/zorven/shared/protocol"
	"github.com/tkodcumpeg4/zorven/shared/stun"
)

// pfake: punch destekli sahte dugum; aldigi net_punch mesajlarini toplar.
type pfake struct {
	*fake
	disco  string
	punchs []protocol.NetPunch
}

func (h *harness) attachPunch(id Identity, disco string) *pfake {
	h.m.Punch = true
	h.m.AllowLoopbackEndpoints = true
	h.m.Stun = []string{"stun.example:3478"}
	id.Punch = true
	f := &fake{t: h.t, ob: NewOutbox(), state: protocol.NewNetState(), pub: pubKey()}
	f.nc = h.m.Attach(id, f.ob)
	reg, _ := protocol.Marshal(protocol.NetRegister{
		Type: protocol.TypeNetRegister, WGPub: f.pub, Name: "acc", DiscoPub: disco,
	}, protocol.TypeNetRegister)
	if err := f.nc.HandleControl(context.Background(), protocol.TypeNetRegister, reg); err != nil {
		h.t.Fatalf("register: %v", err)
	}
	p := &pfake{fake: f, disco: disco}
	p.drainP()
	return p
}

func (p *pfake) drainP() {
	for {
		select {
		case b := <-p.ob.ctrl:
			tp, _ := protocol.PeekType(b)
			switch tp {
			case protocol.TypeNetRegistered:
				jsonUnmarshal(b, &p.reg)
			case protocol.TypeNetMap:
				var nm protocol.NetMap
				jsonUnmarshal(b, &nm)
				p.state.Apply(nm)
			case protocol.TypeNetPunch:
				var np protocol.NetPunch
				jsonUnmarshal(b, &np)
				p.punchs = append(p.punchs, np)
			}
		case b := <-p.ob.data:
			p.data = append(p.data, b)
		default:
			return
		}
	}
}

func (p *pfake) send(t protocol.MessageType, v any) {
	b, _ := protocol.Marshal(v, t)
	_ = p.nc.HandleControl(context.Background(), t, b)
	p.drainP()
}

func (p *pfake) endpoints(eps ...string) {
	p.send(protocol.TypeNetEndpoints, protocol.NetEndpoints{Type: protocol.TypeNetEndpoints, Endpoints: eps})
}

func TestPunchRegisteredFlagsAndStun(t *testing.T) {
	h := newHarness(t)
	h.m.StunPort = 3478
	a := h.attachPunch(dev("t1", "nas", "nas"), pubKey())
	if !a.reg.Punch || len(a.reg.Stun) != 1 || a.reg.StunPort != 3478 {
		t.Fatalf("punch alanlari: %+v", a.reg)
	}
	// Disco anahtari yoksa / bozuksa dogrudan yol yok (W1 davranisi).
	b := h.attachPunch(dev("t1", "lap", "lap"), "")
	if b.reg.Punch || len(b.reg.Stun) != 0 {
		t.Fatalf("disco anahtarsiz dugum punch almamali: %+v", b.reg)
	}
	c := h.attachPunch(dev("t1", "pc", "pc"), "kisa")
	if c.reg.Punch {
		t.Fatal("bozuk disco anahtari kabul edildi")
	}
	// Sunucuda kapaliysa hic punch yok.
	h2 := newHarness(t)
	f := h2.attach(dev("t1", "nas", "nas"))
	if f.reg.Punch {
		t.Fatal("Manager.Punch=false iken punch acildi")
	}
}

func TestPunchOnlyBetweenAuthorizedPeers(t *testing.T) {
	h := newHarness(t)
	nas := h.attachPunch(dev("t1", "nas", "nas"), pubKey())
	alice := h.attachPunch(sess("t1", "alice", "member"), pubKey())
	bob := h.attachPunch(sess("t1", "bob", "member"), pubKey())
	carol := h.attachPunch(sess("t2", "carol", "member"), pubKey())
	nas2 := h.attachPunch(dev("t2", "nas2", "nas2"), pubKey())
	mkShare(t, h, "t1", "nas", 5432, Grantee{GranteeUser, "alice"})
	for _, f := range []*pfake{nas, alice, bob, carol, nas2} {
		f.drainP()
	}

	// Paylasim yok sayilan dugumlerin adaylari kimseye gitmemeli.
	bob.endpoints("10.0.0.9:5000")
	carol.endpoints("10.1.1.1:7000")
	nas.drainP()
	nas2.endpoints("10.2.2.2:7000")
	nas.endpoints("10.0.0.1:4000", "203.0.113.5:4000")
	alice.drainP()
	for name, f := range map[string]*pfake{"bob": bob, "carol": carol, "nas2": nas2} {
		f.drainP()
		if len(f.punchs) != 0 {
			t.Fatalf("%s yetkisiz halde punch aldi: %+v", name, f.punchs)
		}
	}
	// nas adaylarini bildirdi ama alice henuz rapor vermedi: alice nas'in
	// adaylarini alir, nas'a bos aday gonderilmez.
	if len(alice.punchs) != 1 || len(nas.punchs) != 0 {
		t.Fatalf("aday olmadan punch: a=%d n=%d", len(alice.punchs), len(nas.punchs))
	}
	alice.endpoints("198.51.100.7:6000", "bozuk", "0.0.0.0:1", "224.0.0.1:5", "10.0.0.2:0")
	nas.drainP()
	if len(alice.punchs) != 2 || len(nas.punchs) != 1 {
		t.Fatalf("iki yana da punch beklenir: a=%d n=%d", len(alice.punchs), len(nas.punchs))
	}
	ap := alice.punchs[1]
	if ap.PeerWGPub != nas.pub || ap.PeerDiscoPub != nas.disco ||
		len(ap.Endpoints) != 2 || ap.Nonce == "" {
		t.Fatalf("alice'e giden punch yanlis: %+v", ap)
	}
	np := nas.punchs[0]
	if np.PeerWGPub != alice.pub || len(np.Endpoints) != 1 || np.Endpoints[0] != "198.51.100.7:6000" {
		t.Fatalf("gecersiz adaylar suzulmedi: %+v", np)
	}
	if np.Nonce != ap.Nonce {
		t.Fatal("iki yan ayni nonce'u almali")
	}
	// Baska kiraciya veya yetkisiz esler hicbir sey almamali.
	for name, f := range map[string]*pfake{"bob": bob, "carol": carol, "nas2": nas2} {
		f.drainP()
		if len(f.punchs) != 0 {
			t.Fatalf("%s sizinti: %+v", name, f.punchs)
		}
	}
	// Dogrudan net_punch_req ile yetkisiz ese istek yapilamaz.
	bob.send(protocol.TypeNetPunchReq, protocol.NetPunchReq{Type: protocol.TypeNetPunchReq, PeerWGPub: nas.pub})
	carol.send(protocol.TypeNetPunchReq, protocol.NetPunchReq{Type: protocol.TypeNetPunchReq, PeerWGPub: nas.pub})
	nas.drainP()
	if len(nas.punchs) != 1 || len(bob.punchs) != 0 || len(carol.punchs) != 0 {
		t.Fatal("yetkisiz punch_req adaylari sizdirdi")
	}
}

func TestPunchReqRateLimitAndRevoke(t *testing.T) {
	h := newHarness(t)
	now := time.Now()
	h.m.Now = func() time.Time { return now }
	nas := h.attachPunch(dev("t1", "nas", "nas"), pubKey())
	alice := h.attachPunch(sess("t1", "alice", "member"), pubKey())
	sh := mkShare(t, h, "t1", "nas", 22, Grantee{GranteeUser, "alice"})
	nas.drainP()
	alice.drainP()
	nas.endpoints("10.0.0.1:4000")
	alice.endpoints("10.0.0.2:4000")
	base := len(alice.punchs)
	req := func() {
		alice.send(protocol.TypeNetPunchReq, protocol.NetPunchReq{Type: protocol.TypeNetPunchReq, PeerWGPub: nas.pub})
		nas.drainP()
	}
	req()
	if len(alice.punchs) != base+1 {
		t.Fatalf("punch_req yanit vermedi: %d", len(alice.punchs)-base)
	}
	req() // hemen tekrar: hiz sinirina takilir
	if len(alice.punchs) != base+1 {
		t.Fatal("punch_req hiz siniri yok")
	}
	now = now.Add(5 * time.Second)
	req()
	if len(alice.punchs) != base+2 {
		t.Fatal("hiz siniri suresi dolunca tekrar calismali")
	}
	// Paylasim iptal -> artik punch yok.
	if err := h.m.DeleteShare(context.Background(), "t1", sh.ID, "t"); err != nil {
		t.Fatal(err)
	}
	alice.drainP()
	now = now.Add(5 * time.Second)
	req()
	if len(alice.punchs) != base+2 {
		t.Fatal("iptal edilen esle punch yapilmamali")
	}
}

func TestPathStatusInShares(t *testing.T) {
	h := newHarness(t)
	nas := h.attachPunch(dev("t1", "nas", "nas"), pubKey())
	alice := h.attachPunch(sess("t1", "alice", "member"), pubKey())
	bob := h.attachPunch(sess("t1", "bob", "member"), pubKey())
	mkShare(t, h, "t1", "nas", 5432, Grantee{GranteeUser, "alice"}, Grantee{GranteeUser, "bob"})
	for _, f := range []*pfake{nas, alice, bob} {
		f.drainP()
	}
	get := func() ShareInfo {
		l, err := h.m.Shares(context.Background(), "t1")
		if err != nil || len(l) != 1 {
			t.Fatalf("shares: %v %d", err, len(l))
		}
		return l[0]
	}
	if get().Path != "" {
		t.Fatal("rapor yokken yol bilinmemeli")
	}
	nas.send(protocol.TypeNetStatus, protocol.NetStatus{Type: protocol.TypeNetStatus, Paths: []protocol.NetPathStatus{
		{PeerWGPub: alice.pub, Path: "direct", RTTMs: 12},
		{PeerWGPub: bob.pub, Path: "relay"},
		{PeerWGPub: "x", Path: "bogus"},
	}})
	si := get()
	if si.Path != "mixed" {
		t.Fatalf("ozet=%q", si.Path)
	}
	for _, c := range si.Connected {
		if c.Name == alice.reg.Name && (c.Path != "direct" || c.RTTMs != 12) {
			t.Fatalf("alice yolu: %+v", c)
		}
	}
	// Alici tarafi raporu: sahibinki yoksa o kullanilir.
	nas.send(protocol.TypeNetStatus, protocol.NetStatus{Type: protocol.TypeNetStatus})
	bob.send(protocol.TypeNetStatus, protocol.NetStatus{Type: protocol.TypeNetStatus, Paths: []protocol.NetPathStatus{{PeerWGPub: nas.pub, Path: "direct"}}})
	alice.send(protocol.TypeNetStatus, protocol.NetStatus{Type: protocol.TypeNetStatus, Paths: []protocol.NetPathStatus{{PeerWGPub: nas.pub, Path: "direct"}}})
	if get().Path != "direct" {
		t.Fatalf("ozet=%q", get().Path)
	}
	// Dugum kopunca yol bilgisi silinir.
	alice.nc.Close()
	bob.nc.Close()
	if get().Path != "" {
		t.Fatalf("baglanti yokken yol: %q", get().Path)
	}
}

func TestCleanEndpoints(t *testing.T) {
	in := []string{"10.0.0.1:1", "10.0.0.1:1", "[::1]:5", "127.0.0.1:9", "169.254.1.1:5", "[fe80::1]:5", "x", "1.2.3.4:0",
		"[::ffff:192.0.2.1]:80"}
	got := cleanEndpoints(in, false)
	want := map[string]bool{"10.0.0.1:1": true, "192.0.2.1:80": true}
	if len(got) != len(want) {
		t.Fatalf("got %v", got)
	}
	for _, g := range got {
		if !want[g] {
			t.Fatalf("beklenmeyen %s", g)
		}
	}
	if len(cleanEndpoints(in, true)) != 4 {
		t.Fatalf("loopback izinli: %v", cleanEndpoints(in, true))
	}
	var many []string
	for i := 1; i < 60; i++ {
		many = append(many, netip.AddrPortFrom(netip.AddrFrom4([4]byte{10, 0, 0, byte(i)}), 100).String())
	}
	if len(cleanEndpoints(many, false)) != protocol.MaxNetEndpoints {
		t.Fatal("aday sayisi sinirlanmali")
	}
}

func TestStunServerBindingAndRateLimit(t *testing.T) {
	s := NewStunServer(nil)
	now := time.Unix(1000, 0)
	s.Now = func() time.Time { return now }
	src := netip.MustParseAddrPort("198.51.100.20:40000")
	tx := stun.NewTxID()
	req := stun.NewBindingRequest(tx)
	resp, ok := s.Handle(req, src)
	if !ok {
		t.Fatal("gecerli istek yanitlanmali")
	}
	ap, ok := stun.ParseBindingResponse(resp, tx)
	if !ok || ap != src {
		t.Fatalf("yansima %v ok=%v", ap, ok)
	}
	if len(resp) > 3*len(req) {
		t.Fatalf("amplifikasyon: %d", len(resp))
	}
	// Cop ve hatali paket: yanit yok.
	for _, junk := range [][]byte{nil, {1, 2, 3}, make([]byte, 148), stun.NewBindingResponse(tx, src)} {
		if _, ok := s.Handle(junk, src); ok {
			t.Fatal("cop paket yanitlandi")
		}
	}
	// Ayni kaynaktan sel: kova bosalir.
	served := 1
	for i := 0; i < 100; i++ {
		if _, ok := s.Handle(stun.NewBindingRequest(stun.NewTxID()), src); ok {
			served++
		}
	}
	if served > int(s.Burst)+1 {
		t.Fatalf("hiz siniri calismadi: %d yanit", served)
	}
	// Baska kaynak etkilenmez; sure gecince kova dolar.
	other := netip.MustParseAddrPort("198.51.100.21:1")
	if _, ok := s.Handle(stun.NewBindingRequest(stun.NewTxID()), other); !ok {
		t.Fatal("baska kaynak engellendi")
	}
	now = now.Add(5 * time.Second)
	if _, ok := s.Handle(stun.NewBindingRequest(stun.NewTxID()), src); !ok {
		t.Fatal("kova dolmadi")
	}
}
