package wgnet

import (
	"encoding/binary"
	"math/rand"
	"net/netip"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/tkodcumpeg4/zorven/shared/protocol"
	"golang.org/x/net/dns/dnsmessage"
	"golang.zx2c4.com/wireguard/tun"
)

var (
	selfIP  = netip.MustParseAddr("100.64.0.10")
	peerA   = netip.MustParseAddr("100.64.0.20")
	peerB   = netip.MustParseAddr("100.64.0.21")
	strange = netip.MustParseAddr("100.64.0.99")
	resolv  = netip.MustParseAddr("100.64.0.1")
)

func udpPkt(src, dst netip.Addr, sp, dp uint16, payload string) []byte {
	return buildUDP(0, src, dst, sp, dp, []byte(payload))
}

func tcpPkt(src, dst netip.Addr, sp, dp uint16, flags byte) []byte {
	return buildTCP(0, src, dst, sp, dp, 1000, 0, flags, nil)
}

func icmpPkt(src, dst netip.Addr, typ uint8, id uint16) []byte {
	l4 := make([]byte, 8)
	l4[0] = typ
	binary.BigEndian.PutUint16(l4[4:6], id)
	binary.BigEndian.PutUint16(l4[2:4], csum16(0, l4))
	return buildIPv4(0, src, dst, protoICMP, l4)
}

// icmpErr: hedefe ulasilamadi; gomulu orijinal paket (IP + 8 bayt).
func icmpErr(src, dst netip.Addr, orig []byte) []byte {
	l4 := make([]byte, 8+28)
	l4[0], l4[1] = icmpDestUnreach, 4
	copy(l4[8:], orig[:28])
	return buildIPv4(0, src, dst, protoICMP, l4)
}

func newTestFilter(t *testing.T) *Filter {
	t.Helper()
	f := NewFilter(&fakeTun{}, selfIP, resolv, "zorven.internal")
	f.SetPeers([]netip.Addr{peerA, peerB})
	return f
}

func rule(proto, ports string, src ...string) protocol.NetACLRule {
	return protocol.NetACLRule{Src: src, Proto: proto, Ports: ports}
}

func TestFilterACL(t *testing.T) {
	f := newTestFilter(t)
	f.SetRules([]protocol.NetACLRule{
		rule("tcp", "22", peerA.String()),
		rule("tcp", "8000-8100", "100.64.0.0/24"),
		rule("udp", "", peerB.String()),
		rule("icmp", "", peerA.String()),
	})
	cases := []struct {
		name string
		pkt  []byte
		want bool
	}{
		{"izinli port", tcpPkt(peerA, selfIP, 4000, 22, tcpSYN), true},
		{"izinsiz port", tcpPkt(peerA, selfIP, 4000, 23, tcpSYN), false},
		{"baska es, ayni port", tcpPkt(peerB, selfIP, 4000, 22, tcpSYN), false},
		{"aralik alti", tcpPkt(peerA, selfIP, 4000, 8000, tcpSYN), true},
		{"aralik sonu", tcpPkt(peerB, selfIP, 4000, 8100, tcpSYN), true},
		{"aralik disi", tcpPkt(peerB, selfIP, 4000, 8101, tcpSYN), false},
		{"udp herhangi port", udpPkt(peerB, selfIP, 5000, 5353, "x"), true},
		{"udp yanlis kaynak", udpPkt(peerA, selfIP, 5000, 5353, "x"), false},
		{"tcp kurali udp'yi acmaz", udpPkt(peerA, selfIP, 5000, 22, "x"), false},
		{"icmp echo", icmpPkt(peerA, selfIP, icmpEchoRequest, 7), true},
		{"icmp echo yanlis kaynak", icmpPkt(peerB, selfIP, icmpEchoRequest, 7), false},
		{"icmp echo reply kural disi", icmpPkt(peerA, selfIP, icmpEchoReply, 7), false},
		{"baska hedef", tcpPkt(peerA, peerB, 4000, 22, tcpSYN), false},
		{"bilinmeyen kaynak (IP sahteciligi)", tcpPkt(strange, selfIP, 4000, 22, tcpSYN), false},
	}
	for _, c := range cases {
		if got := f.inbound(c.pkt); got != c.want {
			t.Errorf("%s: kabul=%v, beklenen %v", c.name, got, c.want)
		}
	}
}

func TestFilterDefaultDenyAndSpoofWithWideRule(t *testing.T) {
	f := newTestFilter(t)
	if f.inbound(tcpPkt(peerA, selfIP, 1, 22, tcpSYN)) {
		t.Fatal("kural yokken hicbir sey kabul edilmemeli")
	}
	// Kural cok genis olsa bile kaynak bilinen bir es olmali.
	f.SetRules([]protocol.NetACLRule{rule("any", "", "0.0.0.0/0")})
	if f.inbound(tcpPkt(strange, selfIP, 1, 22, tcpSYN)) {
		t.Fatal("bilinmeyen kaynak (sahte IP) reddedilmeli")
	}
	if !f.inbound(tcpPkt(peerA, selfIP, 1, 22, tcpSYN)) {
		t.Fatal("bilinen es genis kuralla kabul edilmeli")
	}
	// Yerel daraltma sunucu kuralini gecersiz kilar.
	f.SetDenyInbound(true)
	if f.inbound(tcpPkt(peerA, selfIP, 1, 22, tcpSYN)) {
		t.Fatal("deny-inbound aciksa yeni baglanti kabul edilmemeli")
	}
}

func TestFilterOutboundRules(t *testing.T) {
	f := newTestFilter(t)
	// kaynak yerel IP degil
	if pass, _ := f.outbound(tcpPkt(strange, peerA, 1, 80, tcpSYN)); pass {
		t.Fatal("sahte kaynakli giden paket dusmeli")
	}
	// bilinmeyen hedef
	if pass, _ := f.outbound(tcpPkt(selfIP, strange, 1, 80, tcpSYN)); pass {
		t.Fatal("netmap'te olmayan hedefe gitmemeli")
	}
	// bozuk
	if pass, _ := f.outbound([]byte{0x45, 0, 0}); pass {
		t.Fatal("bozuk paket gecmemeli")
	}
	if pass, _ := f.outbound(tcpPkt(selfIP, peerA, 1, 80, tcpSYN)); !pass {
		t.Fatal("bilinen ese giden paket gecmeli")
	}
}

func TestFilterConntrackReturnTraffic(t *testing.T) {
	f := newTestFilter(t)
	// Kural YOK; yerel baslatilan akisin donusu yine de kabul edilir.
	if f.inbound(tcpPkt(peerA, selfIP, 80, 40000, tcpACK)) {
		t.Fatal("akis yokken donus kabul edilmemeli")
	}
	f.outbound(tcpPkt(selfIP, peerA, 40000, 80, tcpSYN))
	if !f.inbound(tcpPkt(peerA, selfIP, 80, 40000, tcpSYN|tcpACK)) {
		t.Fatal("TCP donusu kabul edilmeli")
	}
	if f.inbound(tcpPkt(peerA, selfIP, 81, 40000, tcpSYN|tcpACK)) {
		t.Fatal("farkli kaynak portu kabul edilmemeli")
	}
	if f.inbound(tcpPkt(peerB, selfIP, 80, 40000, tcpSYN|tcpACK)) {
		t.Fatal("baska esten ayni 5'li kabul edilmemeli")
	}
	// UDP
	f.outbound(udpPkt(selfIP, peerB, 5000, 53, "q"))
	if !f.inbound(udpPkt(peerB, selfIP, 53, 5000, "r")) {
		t.Fatal("UDP donusu kabul edilmeli")
	}
	// ICMP echo
	f.outbound(icmpPkt(selfIP, peerA, icmpEchoRequest, 99))
	if !f.inbound(icmpPkt(peerA, selfIP, icmpEchoReply, 99)) {
		t.Fatal("echo reply kabul edilmeli")
	}
	if f.inbound(icmpPkt(peerA, selfIP, icmpEchoReply, 100)) {
		t.Fatal("baska kimlikli echo reply kabul edilmemeli")
	}
	// ICMP hata mesaji: gomulu paket bizim akisimiz.
	orig := udpPkt(selfIP, peerB, 5000, 53, "q")
	if !f.inbound(icmpErr(peerB, selfIP, orig)) {
		t.Fatal("kendi akisimizla ilgili ICMP hatasi kabul edilmeli")
	}
	foreign := udpPkt(selfIP, peerB, 6000, 53, "q")
	if f.inbound(icmpErr(peerB, selfIP, foreign)) {
		t.Fatal("bilmedigimiz akisin ICMP hatasi reddedilmeli")
	}
}

func TestConntrackTimeoutsAndLRU(t *testing.T) {
	now := time.Unix(1000, 0)
	ct := newConntrack(3, func() time.Time { return now })
	k := func(p uint8, lp uint16) ctKey { return ctKey{p, selfIP, peerA, lp, 80} }
	ct.track(k(protoUDP, 1))
	ct.track(k(protoTCP, 2))
	ct.track(k(protoICMP, 3))
	now = now.Add(11 * time.Second)
	if ct.seen(k(protoICMP, 3)) {
		t.Error("ICMP 10 sn sonra dusmeli")
	}
	if !ct.seen(k(protoUDP, 1)) {
		t.Error("UDP 11 sn sonra hala canli olmali")
	}
	now = now.Add(31 * time.Second)
	if ct.seen(k(protoUDP, 1)) {
		t.Error("UDP 30 sn sonra dusmeli")
	}
	if !ct.seen(k(protoTCP, 2)) {
		t.Error("TCP 5 dk icinde canli olmali")
	}
	now = now.Add(5*time.Minute + time.Second)
	if ct.seen(k(protoTCP, 2)) {
		t.Error("TCP 5 dk sonra dusmeli")
	}
	// LRU: kapasite 3, 4. giris en eskiyi atar.
	for i := uint16(10); i < 14; i++ {
		ct.track(k(protoTCP, i))
	}
	if ct.size() != 3 {
		t.Errorf("boyut %d", ct.size())
	}
	if ct.seen(k(protoTCP, 10)) {
		t.Error("en eski giris atilmali")
	}
}

func TestParseIPv4Robust(t *testing.T) {
	good := tcpPkt(peerA, selfIP, 1, 2, tcpSYN)
	for n := 0; n < len(good); n++ {
		parseIPv4(good[:n]) // panik olmamali
	}
	bad := append([]byte(nil), good...)
	bad[0] = 0x65 // IPv6 sürümü
	if _, ok := parseIPv4(bad); ok {
		t.Error("IPv6 reddedilmeli")
	}
	// parcalanmis: frag isaretlenir, ACL kabul etmez
	frag := append([]byte(nil), good...)
	frag[6] = 0x20 // MF
	p, ok := parseIPv4(frag)
	if !ok || !p.frag {
		t.Error("parca isaretlenmeli")
	}
	f := newTestFilter(t)
	f.SetRules([]protocol.NetACLRule{rule("any", "", "0.0.0.0/0")})
	if f.inbound(frag) {
		t.Error("parcalanmis gelen paket dusmeli")
	}
	// Rastgele cop veri.
	rng := rand.New(rand.NewSource(1))
	for i := 0; i < 5000; i++ {
		b := make([]byte, rng.Intn(80))
		rng.Read(b)
		if len(b) > 0 && i%2 == 0 {
			b[0] = 0x45
		}
		f.inbound(b)
		f.outbound(b)
	}
}

func FuzzFilterPacket(f *testing.F) {
	f.Add(tcpPkt(peerA, selfIP, 1, 22, tcpSYN))
	f.Add(udpPkt(selfIP, resolv, 5000, 53, "\x00\x01"))
	f.Add(icmpErr(peerA, selfIP, udpPkt(selfIP, peerA, 1, 2, "x")))
	flt := NewFilter(&fakeTun{}, selfIP, resolv, "zorven.internal")
	flt.SetPeers([]netip.Addr{peerA})
	flt.SetRules([]protocol.NetACLRule{rule("any", "", "0.0.0.0/0")})
	f.Fuzz(func(t *testing.T, b []byte) {
		flt.inbound(b)
		flt.outbound(b)
	})
}

// --- sanal DNS ----------------------------------------------------------------------

func dnsQuery(t *testing.T, name string, typ dnsmessage.Type) []byte {
	t.Helper()
	b := dnsmessage.NewBuilder(nil, dnsmessage.Header{ID: 0x1234, RecursionDesired: true})
	_ = b.StartQuestions()
	_ = b.Question(dnsmessage.Question{Name: dnsmessage.MustNewName(name), Type: typ, Class: dnsmessage.ClassINET})
	out, err := b.Finish()
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func newDNSFilter(t *testing.T) (*Filter, *fakeTun) {
	ft := &fakeTun{}
	f := NewFilter(ft, selfIP, resolv, "zorven.internal")
	f.SetPeers([]netip.Addr{peerA})
	st := protocol.NewNetState()
	st.Self = protocol.NetPeer{Name: "laptop", IP: selfIP.String()}
	st.Peers["k"] = protocol.NetPeer{Name: "nas", IP: peerA.String()}
	st.DNS = &protocol.NetDNS{Resolver: resolv.String(), Zone: "zorven.internal", Suffix: "ofis"}
	f.SetDNSNames(DNSNames(st))
	return f, ft
}

func parseDNS(t *testing.T, b []byte) (dnsmessage.Header, []dnsmessage.Resource) {
	t.Helper()
	var p dnsmessage.Parser
	h, err := p.Start(b)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := p.AllQuestions(); err != nil {
		t.Fatal(err)
	}
	as, err := p.AllAnswers()
	if err != nil {
		t.Fatal(err)
	}
	return h, as
}

func TestVirtualDNSUDP(t *testing.T) {
	f, ft := newDNSFilter(t)
	ask := func(name string, typ dnsmessage.Type) (dnsmessage.Header, []dnsmessage.Resource) {
		t.Helper()
		pkt := udpPkt(selfIP, resolv, 5353, 53, string(dnsQuery(t, name, typ)))
		pass, replies := f.outbound(pkt)
		if pass {
			t.Fatal("DNS sorgusu tunele girmemeli")
		}
		if len(replies) != 1 {
			t.Fatalf("1 yanit bekleniyordu: %d", len(replies))
		}
		rp, ok := parseIPv4(replies[0][tunWriteOffset:])
		if !ok || rp.proto != protoUDP || rp.src != resolv || rp.dst != selfIP || rp.srcPort != 53 || rp.dstPort != 5353 {
			t.Fatalf("yanit paketi bozuk: %+v", rp)
		}
		// IP baslik toplami dogru olmali.
		if csum16(0, replies[0][tunWriteOffset:tunWriteOffset+20]) != 0 {
			t.Fatal("IP baslik sum'u yanlis")
		}
		return parseDNS(t, rp.payload)
	}
	h, as := ask("nas.zorven.internal.", dnsmessage.TypeA)
	if h.RCode != dnsmessage.RCodeSuccess || len(as) != 1 || as[0].Body.(*dnsmessage.AResource).A != peerA.As4() || h.ID != 0x1234 || !h.Response {
		t.Fatalf("A yaniti: %+v %+v", h, as)
	}
	if _, as := ask("nas.ofis.zorven.internal.", dnsmessage.TypeA); len(as) != 1 {
		t.Fatal("org soneki ile de cozulmeli")
	}
	if _, as := ask("NAS.Zorven.Internal.", dnsmessage.TypeA); len(as) != 1 {
		t.Fatal("buyuk/kucuk harf duyarsiz olmali")
	}
	if _, as := ask("laptop.zorven.internal.", dnsmessage.TypeA); len(as) != 1 {
		t.Fatal("kendi adi cozulmeli")
	}
	if h, as := ask("nas.zorven.internal.", dnsmessage.TypeAAAA); h.RCode != dnsmessage.RCodeSuccess || len(as) != 0 {
		t.Fatalf("AAAA: NODATA beklenir: %+v %+v", h, as)
	}
	if h, _ := ask("yok.zorven.internal.", dnsmessage.TypeA); h.RCode != dnsmessage.RCodeNameError {
		t.Fatalf("bilinmeyen ad NXDOMAIN olmali: %v", h.RCode)
	}
	// Split: bolge disi ad cevaplanmaz (REFUSED), yonlendirilmez.
	if h, as := ask("example.com.", dnsmessage.TypeA); h.RCode != dnsmessage.RCodeRefused || len(as) != 0 {
		t.Fatalf("bolge disi REFUSED olmali: %v", h.RCode)
	}
	if len(ft.writesCopy()) != 0 {
		// outbound() dogrudan cagrildi; Read yolu ayri testte.
		t.Log("not: outbound dogrudan test edildi")
	}
	// Bozuk sorgu yanitsiz.
	if pass, rep := f.outbound(udpPkt(selfIP, resolv, 1, 53, "\x01\x02")); pass || len(rep) != 0 {
		t.Fatal("bozuk sorguya yanit verilmemeli")
	}
	// 53 disi port: tunele girmez, yanit yok.
	if pass, rep := f.outbound(udpPkt(selfIP, resolv, 1, 80, "x")); pass || len(rep) != 0 {
		t.Fatal("cozucuye 53 disi trafik dusmeli")
	}
	// Baska kaynak IP'li sorgu yanitlanmaz (sahte).
	if pass, rep := f.outbound(udpPkt(peerA, resolv, 1, 53, string(dnsQuery(t, "nas.zorven.internal.", dnsmessage.TypeA)))); pass || len(rep) != 0 {
		t.Fatal("sahte kaynaklu DNS sorgusu yanitlanmamali")
	}
}

func TestVirtualDNSTCP(t *testing.T) {
	f, _ := newDNSFilter(t)
	send := func(pkt []byte) (pktInfo, bool) {
		pass, rep := f.outbound(pkt)
		if pass {
			t.Fatal("tunele girmemeli")
		}
		if len(rep) == 0 {
			return pktInfo{}, false
		}
		p, ok := parseIPv4(rep[0][tunWriteOffset:])
		return p, ok
	}
	// SYN
	syn := buildTCP(0, selfIP, resolv, 40000, 53, 5000, 0, tcpSYN, nil)
	sa, ok := send(syn)
	if !ok || sa.tcpFlags != tcpSYN|tcpACK || sa.tcpAck != 5001 {
		t.Fatalf("SYN/ACK bekleniyordu: %+v", sa)
	}
	// Veri: uzunluk onekli sorgu
	q := dnsQuery(t, "nas.zorven.internal.", dnsmessage.TypeA)
	body := append([]byte{byte(len(q) >> 8), byte(len(q))}, q...)
	data := buildTCP(0, selfIP, resolv, 40000, 53, 5001, sa.tcpSeq+1, tcpPSH|tcpACK, body)
	r, ok := send(data)
	if !ok || r.tcpFlags&tcpFIN == 0 || r.tcpSeq != sa.tcpSeq+1 || r.tcpAck != 5001+uint32(len(body)) {
		t.Fatalf("veri yaniti: %+v", r)
	}
	n := int(binary.BigEndian.Uint16(r.payload[:2]))
	_, as := parseDNS(t, r.payload[2:2+n])
	if len(as) != 1 {
		t.Fatalf("TCP A yaniti: %+v", as)
	}
	// TCP checksum dogru olmali.
	if csum16(pseudoSum(resolv, selfIP, protoTCP, len(r.l4)), r.l4) != 0 {
		t.Fatal("TCP sum'u yanlis")
	}
	// Sahte ack (cerez uymuyor) yanitsiz.
	bad := buildTCP(0, selfIP, resolv, 40000, 53, 5001, 12345, tcpPSH|tcpACK, body)
	if _, ok := send(bad); ok {
		t.Fatal("cerezsiz veri segmenti yanitlanmamali")
	}
}

func TestFilterReadWriteThroughDevice(t *testing.T) {
	ft := &fakeTun{in: make(chan []byte, 8)}
	f := NewFilter(ft, selfIP, resolv, "zorven.internal")
	f.SetPeers([]netip.Addr{peerA})
	f.SetRules([]protocol.NetACLRule{rule("tcp", "22", peerA.String())})
	st := protocol.NewNetState()
	st.Peers["k"] = protocol.NetPeer{Name: "nas", IP: peerA.String()}
	st.DNS = &protocol.NetDNS{Resolver: resolv.String(), Zone: "zorven.internal"}
	f.SetDNSNames(DNSNames(st))

	const off = 16
	mk := func(pkt []byte) []byte { b := make([]byte, off+len(pkt)+10); copy(b[off:], pkt); return b }
	ft.in <- mk(udpPkt(selfIP, resolv, 5353, 53, string(dnsQuery(t, "nas.zorven.internal.", dnsmessage.TypeA)))) // DNS: yakalanir
	ft.in <- mk(tcpPkt(selfIP, strange, 1, 80, tcpSYN))                                                          // bilinmeyen hedef: dusur
	ft.in <- mk(tcpPkt(selfIP, peerA, 40000, 80, tcpSYN))                                                        // gecer
	bufs := make([][]byte, 4)
	for i := range bufs {
		bufs[i] = make([]byte, 2048)
	}
	sizes := make([]int, 4)
	ft.batch = 3
	n, err := f.Read(bufs, sizes, off)
	if err != nil || n != 1 {
		t.Fatalf("Read: n=%d err=%v (yalniz tek paket gecmeli)", n, err)
	}
	p, _ := parseIPv4(bufs[0][off : off+sizes[0]])
	if p.dst != peerA || p.dstPort != 80 {
		t.Fatalf("yanlis paket gecti: %+v", p)
	}
	w := ft.writesCopy()
	if len(w) != 1 {
		t.Fatalf("DNS yaniti TUN'a yazilmali: %d yazma", len(w))
	}
	// Write: izinli + izinsiz karisik toplu paket.
	ok := mk(tcpPkt(peerA, selfIP, 5555, 22, tcpSYN))
	no := mk(tcpPkt(peerA, selfIP, 5555, 23, tcpSYN))
	ret := mk(tcpPkt(peerA, selfIP, 80, 40000, tcpSYN|tcpACK)) // conntrack donusu
	if n, err := f.Write([][]byte{ok, no, ret}, off); err != nil || n != 3 {
		t.Fatalf("Write: %d %v", n, err)
	}
	w = ft.writesCopy()
	if len(w) != 3 { // 1 DNS yaniti + 2 kabul edilen
		t.Fatalf("beklenen 3 yazma (dns+2), alinan %d", len(w))
	}
	if st := f.Stats(); st.DroppedIn != 1 || st.DNSServed != 1 || st.Conntrack != 1 {
		t.Fatalf("sayaclar: %+v", st)
	}
}

// fakeTun: test tun.Device'i.
type fakeTun struct {
	mu     sync.Mutex
	in     chan []byte
	batch  int
	writes [][]byte
}

func (t *fakeTun) File() *os.File        { return nil }
func (t *fakeTun) MTU() (int, error)     { return 1380, nil }
func (t *fakeTun) Name() (string, error) { return "fake0", nil }
func (t *fakeTun) Events() <-chan tun.Event {
	return make(chan tun.Event)
}
func (t *fakeTun) Close() error   { return nil }
func (t *fakeTun) BatchSize() int { return 8 }
func (t *fakeTun) Read(bufs [][]byte, sizes []int, offset int) (int, error) {
	if t.in == nil {
		select {}
	}
	n := 0
	for n < len(bufs) {
		var b []byte
		if n < t.batch {
			b = <-t.in
		} else {
			break
		}
		sizes[n] = copy(bufs[n][offset:], b[offset:])
		n++
	}
	return n, nil
}
func (t *fakeTun) Write(bufs [][]byte, offset int) (int, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	for _, b := range bufs {
		t.writes = append(t.writes, append([]byte(nil), b[offset:]...))
	}
	return len(bufs), nil
}
func (t *fakeTun) writesCopy() [][]byte {
	t.mu.Lock()
	defer t.mu.Unlock()
	return append([][]byte(nil), t.writes...)
}

func init() { _ = tun.EventUp }
