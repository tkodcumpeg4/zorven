package wgnet

import (
	"context"
	"net/netip"
	"os"
	"testing"
	"time"

	"github.com/tkodcumpeg4/zorven/shared/protocol"
	"golang.org/x/net/dns/dnsmessage"
	"golang.zx2c4.com/wireguard/tun"
	"golang.zx2c4.com/wireguard/tun/tuntest"
)

// fakeOS: OS'a dokunmayan OSNet; TUN yerine tuntest.ChannelTUN verir.
type fakeOS struct {
	ch         *tuntest.ChannelTUN
	configured bool
	undone     bool
	dnsUndone  bool
	createErr  error
}

func (f *fakeOS) Name() string { return "fake" }
func (f *fakeOS) CreateTUN(string, int) (tun.Device, error) {
	if f.createErr != nil {
		return nil, f.createErr
	}
	f.ch = tuntest.NewChannelTUN()
	return f.ch.TUN(), nil
}
func (f *fakeOS) Configure(tun.Device, netip.Addr, netip.Prefix, int) (func(), error) {
	f.configured = true
	return func() { f.undone = true }, nil
}
func (f *fakeOS) SetupDNS(tun.Device, netip.Addr, string) DNSSetup {
	return DNSSetup{OK: true, Method: "fake", Undo: func() { f.dnsUndone = true }}
}
func (f *fakeOS) CleanupStale() {}

func noLocalAddrs() ([]IfaceAddr, error) { return nil, nil }

func recvOS(t *testing.T, f *fakeOS, d time.Duration) []byte {
	t.Helper()
	select {
	case p := <-f.ch.Inbound:
		return p
	case <-time.After(d):
		return nil
	}
}

func sendOS(t *testing.T, f *fakeOS, pkt []byte) {
	t.Helper()
	select {
	case f.ch.Outbound <- pkt:
	case <-time.After(3 * time.Second):
		t.Fatal("paket TUN'a verilemedi")
	}
}

// Mod B ucdan uca: gercek wireguard-go, relay yolu, sahte OS TUN.
func TestTunModeEndToEnd(t *testing.T) {
	s := newMini(t)
	a := s.add("a", true)
	b := s.add("b", true)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	if err := a.WaitReady(ctx); err != nil {
		t.Fatal(err)
	}
	if err := b.WaitReady(ctx); err != nil {
		t.Fatal(err)
	}
	aIP, bIP := netip.MustParseAddr(nodeIP(a)), netip.MustParseAddr(nodeIP(b))
	osA, osB := &fakeOS{}, &fakeOS{}

	if _, err := a.EnableTUN(TUNOptions{OS: &fakeOS{createErr: os.ErrPermission}, LocalAddrs: noLocalAddrs}); err == nil {
		t.Fatal("yetki hatasi iletilmeli")
	}
	if a.TUNActive() {
		t.Fatal("hata sonrasi Mod B acik kalmamali")
	}
	// Havuz cakismasi: Mod B baslamaz, Mod A bozulmaz.
	conflict := func() ([]IfaceAddr, error) {
		return []IfaceAddr{{"tailscale0", netip.MustParsePrefix("100.101.102.103/32")}}, nil
	}
	if _, err := a.EnableTUN(TUNOptions{OS: osA, LocalAddrs: conflict}); err == nil || a.TUNActive() {
		t.Fatalf("cakisma tespit edilmeli: %v", err)
	}
	if osA.ch != nil {
		t.Fatal("cakismada TUN olusturulmamali")
	}

	for _, p := range []struct {
		n  *Node
		os *fakeOS
	}{{a, osA}, {b, osB}} {
		st, err := p.n.EnableTUN(TUNOptions{OS: p.os, LocalAddrs: noLocalAddrs})
		if err != nil {
			t.Fatal(err)
		}
		if !st.Active || st.Resolver != "100.64.0.1" || !p.os.configured || !st.DNSOK {
			t.Fatalf("durum: %+v", st)
		}
	}
	if !a.TUNActive() {
		t.Fatal("Mod B acik olmali")
	}

	// Sunucu: b'ye "a -> tcp/22" ACL'i, a'ya DNS bilgisi (ACL yok = default deny).
	mnA, mnB := s.nodes["a"], s.nodes["b"]
	s.enqueue(mnB, protocol.TypeNetMap, protocol.NetMap{
		Type: protocol.TypeNetMap, Epoch: 2, Self: protocol.NetPeer{WGPub: mnB.wgPub, IP: bIP.String(), Name: "b"},
		ACLSet: true, ACL: []protocol.NetACLRule{{Src: []string{aIP.String()}, Proto: "tcp", Ports: "22"}},
		DNS: &protocol.NetDNS{Resolver: "100.64.0.1", Zone: "zorven.internal"},
	})
	s.enqueue(mnA, protocol.TypeNetMap, protocol.NetMap{
		Type: protocol.TypeNetMap, Epoch: 2, Self: protocol.NetPeer{WGPub: mnA.wgPub, IP: aIP.String(), Name: "a"},
		ACLSet: true, DNS: &protocol.NetDNS{Resolver: "100.64.0.1", Zone: "zorven.internal", Suffix: "ofis"},
	})
	waitUntil(t, "ACL uygulandi", 5*time.Second, func() bool {
		return len(b.State().ACL) == 1 && a.State().DNS != nil
	})

	// 1) a -> b:22 gecer (sifrelenir, relay'den gider, b'nin OS'una duser).
	syn := buildTCP(0, aIP, bIP, 40000, 22, 1000, 0, tcpSYN, nil)
	sendOS(t, osA, syn)
	got := recvOS(t, osB, 5*time.Second)
	if got == nil {
		t.Fatal("izinli paket b'ye ulasmadi")
	}
	if p, ok := parseIPv4(got); !ok || p.src != aIP || p.dstPort != 22 {
		t.Fatalf("yanlis paket: %+v", p)
	}
	// 2) a -> b:23 b'nin filtresinde dusurulur.
	sendOS(t, osA, buildTCP(0, aIP, bIP, 40001, 23, 1, 0, tcpSYN, nil))
	if p := recvOS(t, osB, 700*time.Millisecond); p != nil {
		t.Fatal("izinsiz port b'ye ulasti")
	}
	// 3) b -> a: a'nin baslattigi akisin donusu (conntrack) gecer; solicited olmayan duser.
	sendOS(t, osB, buildTCP(0, bIP, aIP, 22, 40000, 5000, 1001, tcpSYN|tcpACK, nil))
	if p := recvOS(t, osA, 5*time.Second); p == nil {
		t.Fatal("akis donusu a'ya ulasmadi")
	}
	sendOS(t, osB, buildTCP(0, bIP, aIP, 9999, 8080, 1, 0, tcpSYN, nil))
	if p := recvOS(t, osA, 700*time.Millisecond); p != nil {
		t.Fatal("istenmeyen gelen baglanti a'ya ulasti (default deny)")
	}
	// 4) Sanal DNS: a'nin OS'u cozucuyu sorgular, yanit TUN'a geri yazilir.
	q := dnsmessage.NewBuilder(nil, dnsmessage.Header{ID: 7, RecursionDesired: true})
	_ = q.StartQuestions()
	_ = q.Question(dnsmessage.Question{Name: dnsmessage.MustNewName("b.zorven.internal."), Type: dnsmessage.TypeA, Class: dnsmessage.ClassINET})
	qb, _ := q.Finish()
	sendOS(t, osA, buildUDP(0, aIP, netip.MustParseAddr("100.64.0.1"), 5353, 53, qb))
	reply := recvOS(t, osA, 5*time.Second)
	if reply == nil {
		t.Fatal("DNS yaniti gelmedi")
	}
	rp, ok := parseIPv4(reply)
	if !ok || rp.srcPort != 53 {
		t.Fatalf("DNS yaniti bozuk: %+v", rp)
	}
	_, as := parseDNS(t, rp.payload)
	if len(as) != 1 || as[0].Body.(*dnsmessage.AResource).A != bIP.As4() {
		t.Fatalf("b.zorven.internal -> %v", as)
	}
	if !a.TUNStatus().Active || a.TUNStatus().Filter.DNSServed != 1 {
		t.Fatalf("durum: %+v", a.TUNStatus())
	}

	// 5) Mod A'ya don: OS kayitlari geri alinir, netstack yolu yine calisir.
	if err := a.DisableTUN(); err != nil {
		t.Fatal(err)
	}
	if err := b.DisableTUN(); err != nil {
		t.Fatal(err)
	}
	if !osA.undone || !osA.dnsUndone || a.TUNActive() {
		t.Fatal("geri alma calismadi")
	}
	echoThrough(t, a, b, 7101, 32<<10)
}

func TestTunRefusedWithoutServerSupport(t *testing.T) {
	k, _ := GenerateKey()
	n := NewNode(nil, miniTr{newMini(t), "x"}, k, "x")
	defer n.Close()
	if _, err := n.EnableTUN(TUNOptions{OS: &fakeOS{}, LocalAddrs: noLocalAddrs}); err == nil {
		t.Fatal("kayit olmadan Mod B acilmamali")
	}
}

func TestPoolConflictDetection(t *testing.T) {
	pool := netip.MustParsePrefix("100.64.0.0/10")
	addrs := []IfaceAddr{
		{"eth0", netip.MustParsePrefix("192.168.1.5/24")},
		{"lo", netip.MustParsePrefix("127.0.0.1/8")},
		{"docker0", netip.MustParsePrefix("172.17.0.1/16")},
	}
	if cs := CheckPoolConflict(pool, addrs, ""); len(cs) != 0 {
		t.Fatalf("cakisma yok: %v", cs)
	}
	ts := append(addrs, IfaceAddr{"tailscale0", netip.MustParsePrefix("100.100.1.2/32")})
	cs := CheckPoolConflict(pool, ts, "")
	if len(cs) != 1 || cs[0].Iface != "tailscale0" {
		t.Fatalf("tailscale cakismasi: %v", cs)
	}
	if cs := CheckPoolConflict(pool, ts, "tailscale0"); len(cs) != 0 {
		t.Fatal("kendi arayuzumuz atlanmali")
	}
	// Daha genis bir LAN (100.64/10'u kapsayan) da cakisir.
	wide := []IfaceAddr{{"vpn0", netip.MustParsePrefix("100.0.0.5/8")}}
	if len(CheckPoolConflict(pool, wide, "")) != 1 {
		t.Fatal("kapsayan on ek cakismasi")
	}
	// Alternatif havuz (10.99/16) 100.x arayuzleriyle cakismaz.
	if cs := CheckPoolConflict(netip.MustParsePrefix("10.99.0.0/16"), ts, ""); len(cs) != 0 {
		t.Fatalf("alternatif havuz: %v", cs)
	}
	err := PoolConflictError(pool, CheckPoolConflict(pool, ts, ""))
	if err == nil || len(err.Error()) < 40 {
		t.Fatal("hata metni")
	}
	if ResolverFor(pool).String() != "100.64.0.1" || ResolverFor(netip.MustParsePrefix("10.99.7.0/16")).String() != "10.99.0.1" {
		t.Fatal("ResolverFor")
	}
}

// Gercek TUN: yalniz ZORVEN_TUN_TEST=1 (root/CAP_NET_ADMIN gerekir; Linux).
func TestRealTUNLinux(t *testing.T) {
	if os.Getenv("ZORVEN_TUN_TEST") != "1" {
		t.Skip("gercek TUN testi icin ZORVEN_TUN_TEST=1 (root/CAP_NET_ADMIN) gerekir")
	}
	osn := DefaultOS()
	if osn.Name() != "linux" {
		t.Skip("yalniz Linux")
	}
	dev, err := osn.CreateTUN("zvtest0", 1380)
	if err != nil {
		t.Fatalf("TUN: %v", err)
	}
	defer dev.Close()
	undo, err := osn.Configure(dev, netip.MustParseAddr("100.127.250.2"), netip.MustParsePrefix("100.127.250.0/24"), 1380)
	if err != nil {
		t.Fatalf("Configure: %v", err)
	}
	defer undo()
	addrs, err := LocalIfaceAddrs()
	if err != nil {
		t.Fatal(err)
	}
	var found bool
	for _, a := range addrs {
		if a.Iface == "zvtest0" && a.Prefix.Addr().String() == "100.127.250.2" {
			found = true
		}
	}
	if !found {
		t.Fatalf("arayuz adresi gorunmuyor: %v", addrs)
	}
	if cs := CheckPoolConflict(netip.MustParsePrefix("100.127.250.0/24"), addrs, ""); len(cs) != 1 {
		t.Fatalf("gercek cakisma tespiti: %v", cs)
	}
}
