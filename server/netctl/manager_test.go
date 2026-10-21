package netctl

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"net/netip"
	"testing"
	"time"

	"github.com/tkodcumpeg4/zorven/shared/protocol"
)

type harness struct {
	t  *testing.T
	m  *Manager
	st *MemStore
}

func newHarness(t *testing.T) *harness {
	st := NewMemStore()
	dir := MapDirectory{
		Users: map[string]map[string]string{
			"t1": {"alice": "member", "bob": "member", "root": "owner"},
			"t2": {"carol": "member"},
		},
		Devices: map[string]map[string]string{
			"t1": {"nas": "NAS Box", "lap": "laptop", "pc": "pc"},
			"t2": {"nas2": "NAS Box"},
		},
	}
	return &harness{t: t, m: New(st, dir, nil, nil), st: st}
}

func pubKey() string {
	b := make([]byte, 32)
	rand.Read(b)
	return base64.StdEncoding.EncodeToString(b)
}

// fake, bir dugumun uc tarafi: outbox'i okuyup durumu uygular.
type fake struct {
	t     *testing.T
	nc    *NodeConn
	ob    *Outbox
	state *protocol.NetState
	reg   protocol.NetRegistered
	pub   string
	data  [][]byte
}

func (h *harness) attach(id Identity) *fake {
	f := &fake{t: h.t, ob: NewOutbox(), state: protocol.NewNetState(), pub: pubKey()}
	f.nc = h.m.Attach(id, f.ob)
	reg, _ := protocol.Marshal(protocol.NetRegister{Type: protocol.TypeNetRegister, WGPub: f.pub, Name: "acc"}, protocol.TypeNetRegister)
	if err := f.nc.HandleControl(context.Background(), protocol.TypeNetRegister, reg); err != nil {
		h.t.Fatalf("register: %v", err)
	}
	f.drain()
	return f
}

// drain: kuyruktaki her seyi uygular.
func (f *fake) drain() {
	for {
		select {
		case b := <-f.ob.ctrl:
			tp, _ := protocol.PeekType(b)
			switch tp {
			case protocol.TypeNetRegistered:
				jsonUnmarshal(b, &f.reg)
			case protocol.TypeNetMap:
				var nm protocol.NetMap
				jsonUnmarshal(b, &nm)
				f.state.Apply(nm)
			}
		case b := <-f.ob.data:
			f.data = append(f.data, b)
		default:
			return
		}
	}
}

func (f *fake) hasPeer(pub string) bool { _, ok := f.state.Peers[pub]; return ok }

func dev(t, c, name string) Identity {
	return Identity{TenantID: t, Kind: KindDevice, ClientID: c, ClientName: name}
}
func sess(t, u, role string) Identity {
	return Identity{TenantID: t, Kind: KindSession, UserID: u, Role: role}
}

func TestIPAllocPersistentAndUnique(t *testing.T) {
	h := newHarness(t)
	a := h.attach(dev("t1", "nas", "NAS Box"))
	b := h.attach(dev("t1", "lap", "laptop"))
	if a.reg.IP == b.reg.IP {
		t.Fatal("ayni IP")
	}
	ip := netip.MustParseAddr(a.reg.IP)
	if !DefaultPool.Contains(ip) || ip.As4()[2] == 0 && ip.As4()[1] == 64 {
		t.Fatalf("rezerve blok disinda olmali: %s", ip)
	}
	if a.reg.Name != "nas-box" {
		t.Fatalf("ad=%q", a.reg.Name)
	}
	// Yeniden baglanma: ayni IP, ayni ad.
	a.nc.Close()
	a2 := h.attach(dev("t1", "nas", "NAS Box"))
	if a2.reg.IP != a.reg.IP {
		t.Fatalf("cihaz IP'si kalici olmali: %s != %s", a2.reg.IP, a.reg.IP)
	}
	// Yeniden baslatma (yeni Manager, ayni Store): IP korunur.
	m2 := New(h.st, h.m.dir, nil, nil)
	h2 := &harness{t: t, m: m2, st: h.st}
	a3 := h2.attach(dev("t1", "nas", "NAS Box"))
	if a3.reg.IP != a.reg.IP {
		t.Fatalf("restart sonrasi IP degisti")
	}
}

func TestSessionIPReleased(t *testing.T) {
	h := newHarness(t)
	s1 := h.attach(sess("t1", "alice", "member"))
	s1.nc.Close()
	nodes, _ := h.m.Nodes(context.Background(), "t1")
	if len(nodes) != 0 {
		t.Fatalf("oturum dugumu kapaninca silinmeli: %d", len(nodes))
	}
	s2 := h.attach(sess("t1", "alice", "member"))
	if s2.reg.IP == "" {
		t.Fatal("yeniden kayit basarisiz")
	}
}

func mkShare(t *testing.T, h *harness, tenant, owner string, port int, gs ...Grantee) Share {
	s, err := h.m.CreateShare(context.Background(), tenant, ShareInput{OwnerClientID: owner, Port: port, Grantees: gs}, "test")
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func TestNetmapVisibilityAndRevoke(t *testing.T) {
	h := newHarness(t)
	nas := h.attach(dev("t1", "nas", "nas"))
	lap := h.attach(dev("t1", "lap", "lap"))
	alice := h.attach(sess("t1", "alice", "member"))
	bob := h.attach(sess("t1", "bob", "member"))
	for _, f := range []*fake{nas, lap, alice, bob} {
		if len(f.state.Peers) != 0 {
			t.Fatal("paylasim yokken hicbir es gorunmemeli")
		}
	}
	sh := mkShare(t, h, "t1", "nas", 5432, Grantee{GranteeUser, "alice"})
	for _, f := range []*fake{nas, lap, alice, bob} {
		f.drain()
	}
	if !alice.hasPeer(nas.pub) || len(alice.state.SharesIn) != 1 {
		t.Fatalf("alice nas'i ve paylasimi gormeli: %+v", alice.state)
	}
	if !nas.hasPeer(alice.pub) || len(nas.state.SharesOut) != 1 {
		t.Fatal("nas alice'i gormeli (handshake icin)")
	}
	so := nas.state.SharesOut[sh.ID]
	if len(so.Allow) != 1 || so.Allow[0] != alice.reg.IP {
		t.Fatalf("allow=%v", so.Allow)
	}
	if bob.hasPeer(nas.pub) || lap.hasPeer(nas.pub) || nas.hasPeer(bob.pub) || nas.hasPeer(lap.pub) {
		t.Fatal("yetkisiz dugumler gorunmemeli")
	}

	// Cihaza ek grant (grup everyone yerine device).
	if _, err := h.m.UpdateShare(context.Background(), "t1", sh.ID, SharePatch{Grantees: &[]Grantee{{GranteeUser, "alice"}, {GranteeDevice, "lap"}}}, "test"); err != nil {
		t.Fatal(err)
	}
	lap.drain()
	nas.drain()
	if !lap.hasPeer(nas.pub) || !nas.hasPeer(lap.pub) {
		t.Fatal("cihaz grant'i ile lap gorunmeli")
	}

	// Revoke: grant kaldir -> peer duser.
	if _, err := h.m.UpdateShare(context.Background(), "t1", sh.ID, SharePatch{Grantees: &[]Grantee{{GranteeUser, "alice"}}}, "test"); err != nil {
		t.Fatal(err)
	}
	lap.drain()
	nas.drain()
	if lap.hasPeer(nas.pub) || nas.hasPeer(lap.pub) || len(lap.state.SharesIn) != 0 {
		t.Fatal("grant kaldirilinca peer ve paylasim dusmeli")
	}
	// Paylasimi sil.
	if err := h.m.DeleteShare(context.Background(), "t1", sh.ID, "test"); err != nil {
		t.Fatal(err)
	}
	alice.drain()
	nas.drain()
	if alice.hasPeer(nas.pub) || len(alice.state.SharesIn) != 0 || len(nas.state.SharesOut) != 0 || nas.hasPeer(alice.pub) {
		t.Fatal("silinen paylasim tum netmap'lerden dusmeli")
	}
	// Relay artik reddedilir.
	alice.nc.Relay(protocol.IPToRelayID(netip.MustParseAddr(nas.reg.IP)), []byte("x"))
	nas.drain()
	if len(nas.data) != 0 {
		t.Fatal("iptal sonrasi relay calismamali")
	}
}

func TestGroupGrantAndValidation(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	h.attach(dev("t1", "nas", "nas"))
	root := h.attach(sess("t1", "root", "owner"))
	alice := h.attach(sess("t1", "alice", "member"))
	mkShare(t, h, "t1", "nas", 80, Grantee{GranteeGroup, "role:owner"})
	root.drain()
	alice.drain()
	if len(root.state.SharesIn) != 1 || len(alice.state.SharesIn) != 0 {
		t.Fatalf("role:owner yalniz owner'a: root=%d alice=%d", len(root.state.SharesIn), len(alice.state.SharesIn))
	}
	bad := []Grantee{{GranteeUser, "ghost"}, {GranteeDevice, "yok"}, {GranteeGroup, "tag:x"}, {"x", "y"}, {GranteeUser, "carol"}}
	for _, g := range bad {
		if _, err := h.m.CreateShare(ctx, "t1", ShareInput{OwnerClientID: "nas", Port: 81, Grantees: []Grantee{g}}, "t"); err == nil {
			t.Fatalf("gecersiz alici kabul edildi: %+v", g)
		}
	}
	// Cakisma: ayni port / ayni ad.
	if _, err := h.m.CreateShare(ctx, "t1", ShareInput{OwnerClientID: "nas", Port: 80, Grantees: []Grantee{{GranteeUser, "alice"}}}, "t"); ErrorCode(err) != "port_shared" {
		t.Fatalf("port cakismasi: %v", err)
	}
	if _, err := h.m.CreateShare(ctx, "t1", ShareInput{OwnerClientID: "nas", Port: 0}, "t"); err == nil {
		t.Fatal("port 0 kabul edildi")
	}
}

func TestTenantIsolation(t *testing.T) {
	h := newHarness(t)
	// Iki kiracida ayni sirayla IP alinir (ayni degerler cikar).
	a1 := h.attach(dev("t1", "nas", "nas"))
	s1 := h.attach(sess("t1", "alice", "member"))
	a2 := h.attach(dev("t2", "nas2", "nas"))
	s2 := h.attach(sess("t2", "carol", "member"))
	if a1.reg.IP != a2.reg.IP {
		t.Skip("IP'ler cakismiyor; test anlamsiz")
	}
	mkShare(t, h, "t1", "nas", 22, Grantee{GranteeUser, "alice"})
	mkShare(t, h, "t2", "nas2", 22, Grantee{GranteeUser, "carol"})
	for _, f := range []*fake{a1, s1, a2, s2} {
		f.drain()
	}
	if !s1.hasPeer(a1.pub) || s1.hasPeer(a2.pub) || !s2.hasPeer(a2.pub) || s2.hasPeer(a1.pub) {
		t.Fatal("kiraci izolasyonu bozuk (netmap)")
	}
	// Carol'in a1'in (ayni IP) cihazina gonderdigi cerceve t1'e ASLA ulasmaz.
	s2.nc.Relay(protocol.IPToRelayID(netip.MustParseAddr(a1.reg.IP)), []byte("sizinti"))
	a1.drain()
	a2.drain()
	if len(a1.data) != 0 {
		t.Fatal("kiraclar arasi cerceve iletildi")
	}
	if len(a2.data) != 1 {
		t.Fatal("kendi kiracisindaki es cerceveyi almali")
	}
	// Ayni acik anahtar iki kiracida kaydedilemez.
	reg, _ := protocol.Marshal(protocol.NetRegister{Type: protocol.TypeNetRegister, WGPub: a1.pub}, protocol.TypeNetRegister)
	f := &fake{ob: NewOutbox()}
	nc := h.m.Attach(sess("t2", "carol", "member"), f.ob)
	if err := nc.HandleControl(context.Background(), protocol.TypeNetRegister, reg); err == nil {
		t.Fatal("tekrarlanan acik anahtar kabul edildi")
	}
}

func TestRelayDeliversWithSourceID(t *testing.T) {
	h := newHarness(t)
	nas := h.attach(dev("t1", "nas", "nas"))
	alice := h.attach(sess("t1", "alice", "member"))
	mkShare(t, h, "t1", "nas", 22, Grantee{GranteeUser, "alice"})
	nas.drain()
	alice.drain()
	alice.nc.Relay(protocol.IPToRelayID(netip.MustParseAddr(nas.reg.IP)), []byte("sifreli"))
	nas.drain()
	if len(nas.data) != 1 {
		t.Fatalf("data=%d", len(nas.data))
	}
	fr, err := protocol.DecodeBodyFrame(nas.data[0])
	if err != nil || fr.FrameType != protocol.FrameWG || string(fr.Payload) != "sifreli" {
		t.Fatalf("cerceve: %+v %v", fr, err)
	}
	if protocol.RelayIDToIP(fr.ReqID).String() != alice.reg.IP {
		t.Fatalf("kaynak kimligi yanlis: %s", protocol.RelayIDToIP(fr.ReqID))
	}
	// Rastgele bir IP'ye (peer olmayan) gonderim dusurulur.
	alice.nc.Relay(protocol.IPToRelayID(netip.MustParseAddr("100.64.9.9")), []byte("x"))
}

func TestOutboxDropsDataButClosesOnCtrlOverflow(t *testing.T) {
	ob := NewOutbox()
	for i := 0; i < 1000; i++ {
		ob.Data([]byte("x"))
	}
	if ob.Dropped.Load() == 0 {
		t.Fatal("veri kuyrugu tasinca dusurulmeli")
	}
	select {
	case <-ob.Done():
		t.Fatal("veri tasmasi baglantiyi kapatmamali")
	default:
	}
	for i := 0; i < 100; i++ {
		ob.Ctrl([]byte("x"))
	}
	select {
	case <-ob.Done():
	default:
		t.Fatal("kontrol tasmasi baglantiyi kapatmali (resync)")
	}
}

func TestEphemeralShareAndExpiry(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	now := time.Now()
	h.m.Now = func() time.Time { return now }
	nas := h.attach(dev("t1", "nas", "nas"))
	alice := h.attach(sess("t1", "alice", "member"))
	// Cihaz kendi portunu paylasir (gecici).
	req, _ := protocol.Marshal(protocol.NetShareCreate{
		Type: protocol.TypeNetShareCreate, ReqID: "r1", Port: 8080, Ephemeral: true,
		To: []protocol.NetGrantee{{Type: "user", ID: "alice"}},
	}, protocol.TypeNetShareCreate)
	if err := nas.nc.HandleControl(ctx, protocol.TypeNetShareCreate, req); err != nil {
		t.Fatal(err)
	}
	nas.drain()
	alice.drain()
	if len(alice.state.SharesIn) != 1 {
		t.Fatal("gecici paylasim alice'e gorunmeli")
	}
	// Sure bitimi: Sweep paylasimi kaldirir.
	exp := now.Add(time.Minute)
	expP := &exp
	for id := range alice.state.SharesIn {
		if _, err := h.m.UpdateShare(ctx, "t1", id, SharePatch{ExpiresAt: &expP}, "t"); err != nil {
			t.Fatal(err)
		}
	}
	now = now.Add(2 * time.Minute)
	h.m.Sweep(ctx, false)
	alice.drain()
	nas.drain()
	if len(alice.state.SharesIn) != 0 || alice.hasPeer(nas.pub) {
		t.Fatal("suresi dolan paylasim kalkmali")
	}
	// Yeni gecici paylasim, cihaz kopunca silinir.
	now = time.Now()
	mkShareEph := func() {
		if _, err := h.m.CreateShare(ctx, "t1", ShareInput{OwnerClientID: "nas", Port: 9000, Ephemeral: true, Grantees: []Grantee{{GranteeUser, "alice"}}}, "t"); err != nil {
			t.Fatal(err)
		}
	}
	mkShareEph()
	alice.drain()
	if len(alice.state.SharesIn) != 1 {
		t.Fatal("paylasim gorunmeli")
	}
	nas.nc.Close()
	alice.drain()
	if len(alice.state.SharesIn) != 0 {
		t.Fatal("gecici paylasim cihaz kopunca silinmeli")
	}
	if l, _ := h.st.ListShares(ctx, "t1"); len(l) != 0 {
		t.Fatalf("depoda kalmamali: %d", len(l))
	}
}

func TestRevokeNodeRemovesPeer(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	nas := h.attach(dev("t1", "nas", "nas"))
	alice := h.attach(sess("t1", "alice", "member"))
	mkShare(t, h, "t1", "nas", 22, Grantee{GranteeUser, "alice"})
	alice.drain()
	nas.drain()
	nodes, _ := h.m.Nodes(ctx, "t1")
	var sid string
	for _, n := range nodes {
		if n.Kind == KindSession {
			sid = n.ID
		}
	}
	if err := h.m.RevokeNode(ctx, "t1", sid, "t"); err != nil {
		t.Fatal(err)
	}
	nas.drain()
	if nas.hasPeer(alice.pub) {
		t.Fatal("iptal edilen dugum nas netmap'inden dusmeli")
	}
	select {
	case <-alice.ob.Done():
	default:
		t.Fatal("iptal edilen dugumun baglantisi kapanmali")
	}
}
