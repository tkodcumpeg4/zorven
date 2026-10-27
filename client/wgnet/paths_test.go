package wgnet

import (
	"net/netip"
	"sort"
	"testing"
	"time"

	"github.com/tkodcumpeg4/zorven/shared/protocol"
)

// --- sahte saat + gecikmeli sahte ag --------------------------------------------

type simNode struct {
	name string
	pm   *pathMgr
	ip   netip.Addr // ic (WG) IP'si
	pub  string     // wg pub (base64 benzeri etiket; sunucu ile eslesme icin)
	key  DiscoKey
	// addrs: bu dugumun dinledigi UDP adresleri; src: giden paketlerin kaynagi.
	src      netip.AddrPort
	punchReq []string
	changes  int
	cur      netip.AddrPort // yanit sirasinda: paketin geldigi adres (yanit buradan cikar)
}

type simPkt struct {
	due  time.Time
	to   netip.AddrPort
	src  netip.AddrPort
	from *simNode
	pkt  []byte
}

type simNet struct {
	t      *testing.T
	now    time.Time
	owners map[netip.AddrPort]*simNode
	lat    map[netip.AddrPort]time.Duration // hedef adres basina tek yon gecikme
	drop   func(from *simNode, to netip.AddrPort) bool
	q      []simPkt
	nodes  []*simNode
	sent   map[string]int // dugum -> gonderilen disco paketi
	log    [][]byte
}

func newSim(t *testing.T) *simNet {
	return &simNet{t: t, now: time.Unix(1_800_000_000, 0), owners: map[netip.AddrPort]*simNode{},
		lat: map[netip.AddrPort]time.Duration{}, sent: map[string]int{}}
}

func (s *simNet) clock() time.Time { return s.now }

func (s *simNet) addNode(name, ip string, src string, listen ...string) *simNode {
	k, err := GenerateDiscoKey()
	if err != nil {
		s.t.Fatal(err)
	}
	n := &simNode{name: name, ip: netip.MustParseAddr(ip), key: k, src: netip.MustParseAddrPort(src), pub: "wg-" + name}
	n.pm = newPathMgr(k, s.clock)
	n.pm.sendUDP = func(to netip.AddrPort, pkt []byte) error {
		s.sent[name]++
		s.log = append(s.log, append([]byte(nil), pkt...))
		if s.drop != nil && s.drop(n, to) {
			return nil
		}
		d := s.lat[to]
		if d == 0 {
			d = 2 * time.Millisecond
		}
		src := n.src
		if n.cur.IsValid() {
			src = n.cur
		}
		s.q = append(s.q, simPkt{due: s.now.Add(d), to: to, src: src, from: n, pkt: append([]byte(nil), pkt...)})
		return nil
	}
	n.pm.requestPunch = func(w string) { n.punchReq = append(n.punchReq, w) }
	n.pm.onChange = func() { n.changes++ }
	s.owners[netip.MustParseAddrPort(src)] = n
	for _, l := range listen {
		s.owners[netip.MustParseAddrPort(l)] = n
	}
	s.nodes = append(s.nodes, n)
	return n
}

func (s *simNet) link(a, b *simNode) {
	a.pm.setPeer(b.ip, b.pub, b.key.PubB64())
	b.pm.setPeer(a.ip, a.pub, a.key.PubB64())
}

// advance: d kadar sahte zaman ilerletir; 50ms'de bir step, paketler vade sirasiyla.
func (s *simNet) advance(d time.Duration) {
	end := s.now.Add(d)
	for s.now.Before(end) {
		tickEnd := s.now.Add(50 * time.Millisecond)
		if tickEnd.After(end) {
			tickEnd = end
		}
		for {
			sort.SliceStable(s.q, func(i, j int) bool { return s.q[i].due.Before(s.q[j].due) })
			if len(s.q) == 0 || s.q[0].due.After(tickEnd) {
				break
			}
			p := s.q[0]
			s.q = s.q[1:]
			if p.due.After(s.now) {
				s.now = p.due
			}
			if dst := s.owners[p.to]; dst != nil {
				dst.cur = p.to
				dst.pm.handleDisco(p.pkt, p.src)
				dst.cur = netip.AddrPort{}
			}
		}
		s.now = tickEnd
		for _, n := range s.nodes {
			n.pm.step(s.now)
		}
	}
}

func pathOf(n, peer *simNode) string {
	for _, p := range n.pm.snapshot() {
		if p.IP == peer.ip {
			return p.Path
		}
	}
	return ""
}

func twoNodes(t *testing.T) (*simNet, *simNode, *simNode) {
	s := newSim(t)
	a := s.addNode("a", "100.64.1.1", "10.0.0.1:1000")
	b := s.addNode("b", "100.64.1.2", "10.0.0.2:2000")
	s.link(a, b)
	return s, a, b
}

// --- testler -----------------------------------------------------------------------

func TestDirectPathEstablishes(t *testing.T) {
	s, a, b := twoNodes(t)
	if pathOf(a, b) != protocol.PathRelay {
		t.Fatal("baslangicta yol relay olmali")
	}
	if _, ok := a.pm.route(b.ip); ok {
		t.Fatal("dogrulanmadan dogrudan gonderilmemeli")
	}
	a.pm.punch(b.pub, []string{"10.0.0.2:2000"})
	b.pm.punch(a.pub, []string{"10.0.0.1:1000"})
	s.advance(time.Second)
	if pathOf(a, b) != protocol.PathDirect || pathOf(b, a) != protocol.PathDirect {
		t.Fatalf("dogrudan yol kurulmadi: a=%s b=%s", pathOf(a, b), pathOf(b, a))
	}
	addr, ok := a.pm.route(b.ip)
	if !ok || addr != b.src {
		t.Fatalf("route=%v %v", addr, ok)
	}
	// Dogrulanmis adresten gelen WG paketi ese atfedilir; baskasindan gelen atilir.
	if ip, ok := a.pm.lookupSrc(b.src); !ok || ip != b.ip {
		t.Fatalf("lookupSrc: %v %v", ip, ok)
	}
	if _, ok := a.pm.lookupSrc(netip.MustParseAddrPort("10.9.9.9:1")); ok {
		t.Fatal("dogrulanmamis adres kabul edildi")
	}
	if a.changes == 0 {
		t.Fatal("onChange tetiklenmedi")
	}
}

func TestOneSidedPunchStillConnects(t *testing.T) {
	// Yalniz A punch aldi: B yoklamayi duyup kendi pencere acar ve karsilik verir.
	s, a, b := twoNodes(t)
	a.pm.punch(b.pub, []string{"10.0.0.2:2000"})
	s.advance(2 * time.Second)
	if pathOf(a, b) != protocol.PathDirect || pathOf(b, a) != protocol.PathDirect {
		t.Fatalf("tek tarafli: a=%s b=%s", pathOf(a, b), pathOf(b, a))
	}
}

func TestFallbackToRelayAfter30sSilence(t *testing.T) {
	s, a, b := twoNodes(t)
	a.pm.punch(b.pub, []string{"10.0.0.2:2000"})
	b.pm.punch(a.pub, []string{"10.0.0.1:1000"})
	s.advance(time.Second)
	if pathOf(a, b) != protocol.PathDirect {
		t.Fatal("on kosul")
	}
	// UDP kesilir.
	s.drop = func(*simNode, netip.AddrPort) bool { return true }
	s.advance(24 * time.Second)
	if pathOf(a, b) != protocol.PathDirect {
		t.Fatal("30 sn dolmadan relay'e dusmemeli")
	}
	s.advance(10 * time.Second)
	if pathOf(a, b) != protocol.PathRelay {
		t.Fatal("30 sn yanitsiz: relay'e dusmeli")
	}
	if _, ok := a.pm.route(b.ip); ok {
		t.Fatal("dusus sonrasi dogrudan route olmamali")
	}
	if len(a.punchReq) == 0 {
		t.Fatal("dususten sonra sunucudan yeni punch istenmeli")
	}
	// UDP geri gelir, sunucu yeniden punch yollar: yol toparlanir.
	s.drop = nil
	a.pm.punch(b.pub, []string{"10.0.0.2:2000"})
	b.pm.punch(a.pub, []string{"10.0.0.1:1000"})
	s.advance(2 * time.Second)
	if pathOf(a, b) != protocol.PathDirect {
		t.Fatal("UDP donunce dogrudan yol toparlanmali")
	}
}

func TestKeepaliveHoldsPathUp(t *testing.T) {
	s, a, b := twoNodes(t)
	a.pm.punch(b.pub, []string{"10.0.0.2:2000"})
	b.pm.punch(a.pub, []string{"10.0.0.1:1000"})
	s.advance(time.Second)
	before := s.sent["a"]
	s.advance(10 * time.Minute)
	if pathOf(a, b) != protocol.PathDirect || pathOf(b, a) != protocol.PathDirect {
		t.Fatal("hafif ping yolu canli tutmali")
	}
	// ~25 sn'de bir ping: 10 dk icinde yaklasik 24 + yoklama, 100'u asmamali.
	extra := s.sent["a"] - before
	if extra < 30 || extra > 110 {
		t.Fatalf("keepalive ping sayisi beklenmedik: %d", extra)
	}
}

func TestBlockedUDPStaysRelayWithBackoff(t *testing.T) {
	s, a, b := twoNodes(t)
	s.drop = func(*simNode, netip.AddrPort) bool { return true } // simetrik NAT / UDP engelli
	a.pm.punch(b.pub, []string{"10.0.0.2:2000"})
	s.advance(11 * time.Second)
	first := s.sent["a"]
	if first < 5 {
		t.Fatalf("pencere icinde tekrarli yoklama beklenir: %d", first)
	}
	s.advance(2 * time.Second) // pencere bitti
	n := s.sent["a"]
	s.advance(25 * time.Second) // geri cekilme: retryMin 30 sn dolmadan ek yoklama yok
	if s.sent["a"] != n {
		t.Fatalf("geri cekilme suresinde yoklama yapildi: %d -> %d", n, s.sent["a"])
	}
	if pathOf(a, b) != protocol.PathRelay {
		t.Fatal("UDP engelliyken relay kalmali")
	}
	s.advance(30 * time.Second)
	if len(a.punchReq) == 0 {
		t.Fatal("geri cekilme sonrasi sunucudan yeni punch istenmeli")
	}
	// Ikinci bekleme ilkinden uzun (ustel).
	cnt := len(a.punchReq)
	s.advance(40 * time.Second)
	if len(a.punchReq) != cnt {
		t.Fatal("ikinci bekleme 30 sn'den uzun olmali")
	}
}

func TestLowestRTTCandidateWins(t *testing.T) {
	s := newSim(t)
	a := s.addNode("a", "100.64.1.1", "10.0.0.1:1000")
	b := s.addNode("b", "100.64.1.2", "203.0.113.2:2000", "10.0.0.3:2000")
	s.link(a, b)
	s.lat[netip.MustParseAddrPort("203.0.113.2:2000")] = 40 * time.Millisecond // yavas (genel)
	s.lat[netip.MustParseAddrPort("10.0.0.3:2000")] = 3 * time.Millisecond     // hizli (yerel)
	s.lat[netip.MustParseAddrPort("10.0.0.1:1000")] = 3 * time.Millisecond
	// Genel aday once listelenir: ilk pong onu secer, sonra yerel adaya gecilir.
	a.pm.punch(b.pub, []string{"203.0.113.2:2000", "10.0.0.3:2000"})
	s.advance(time.Second)
	var got netip.AddrPort
	for _, p := range a.pm.snapshot() {
		got = p.Addr
		if p.Path != protocol.PathDirect {
			t.Fatalf("yol=%s", p.Path)
		}
	}
	if got != netip.MustParseAddrPort("10.0.0.3:2000") {
		t.Fatalf("en dusuk RTT'li aday secilmeli, secilen %v", got)
	}
}

func TestDiscoSealAndReplayRejected(t *testing.T) {
	s, a, b := twoNodes(t)
	a.pm.punch(b.pub, []string{"10.0.0.2:2000"})
	b.pm.punch(a.pub, []string{"10.0.0.1:1000"})
	s.advance(time.Second)
	if len(s.log) < 2 {
		t.Fatal("yakalanan paket yok")
	}
	// Yakalanan gercek bir pingi B'ye tekrar oynat: pong donmemeli.
	var ping []byte
	for _, p := range s.log {
		if m, _, err := openDisco(&b.pm.peers[a.ip].shared, p); err == nil && m.Kind == discoPing {
			ping = p
			break
		}
	}
	if ping == nil {
		t.Fatal("ping yakalanamadi")
	}
	before := s.sent["b"]
	b.pm.handleDisco(ping, a.src) // ayni nonce: ikinci kez
	// Ilk teslim zaten yapilmisti (nonce kayitli) -> tekrar reddedilir.
	if s.sent["b"] != before {
		t.Fatal("tekrar oynatilan ping yanitlandi")
	}
	// Tekrar oynatilan pong: bekleyen txid tuketildi, durum degismez.
	var pong []byte
	for _, p := range s.log {
		if m, _, err := openDisco(&a.pm.peers[b.ip].shared, p); err == nil && m.Kind == discoPong {
			pong = p
		}
	}
	if pong == nil {
		t.Fatal("pong yakalanamadi")
	}
	ch := a.changes
	a.pm.handleDisco(pong, netip.MustParseAddrPort("198.51.100.66:9"))
	if a.changes != ch {
		t.Fatal("tekrar pong durumu degistirdi")
	}
	if _, ok := a.pm.lookupSrc(netip.MustParseAddrPort("198.51.100.66:9")); ok {
		t.Fatal("tekrar oynatilan pong sahte adresi dogruladi")
	}
}

func TestDiscoTamperWrongKeyAndSkew(t *testing.T) {
	s, a, b := twoNodes(t)
	_ = s
	// Yeni dogru muhurlu ping uret.
	mk := func(from *simNode, to *simNode, ts time.Time) []byte {
		sh := from.key.sharedKey(to.key.Pub)
		var tx [12]byte
		fillRand(tx[:])
		pkt, err := sealDisco(from.key, &sh, discoMsg{Kind: discoPing, TS: ts, Tx: tx})
		if err != nil {
			t.Fatal(err)
		}
		return pkt
	}
	reached := func(pkt []byte) bool {
		before := s.sent["b"]
		b.pm.handleDisco(pkt, a.src)
		return s.sent["b"] > before
	}
	if !reached(mk(a, b, s.now)) {
		t.Fatal("gecerli ping yanitlanmali")
	}
	// Bozulmus sifreli metin
	bad := mk(a, b, s.now)
	bad[len(bad)-3] ^= 0xff
	if reached(bad) {
		t.Fatal("bozulmus paket kabul edildi")
	}
	// Baska anahtarla muhurlenmis (sahte gonderen)
	evil, _ := GenerateDiscoKey()
	sh := evil.sharedKey(b.key.Pub)
	var tx [12]byte
	pkt, _ := sealDisco(evil, &sh, discoMsg{Kind: discoPing, TS: s.now, Tx: tx})
	if reached(pkt) {
		t.Fatal("bilinmeyen gonderen kabul edildi")
	}
	// Gonderen kimligi A diye ama baska anahtarla sifrelenmis icerik
	f2 := append([]byte(nil), pkt...)
	copy(f2[7:39], a.key.Pub[:]) // iddia: A, icerik: evil'in anahtariyla
	if reached(f2) {
		t.Fatal("sahte gonderen kimligi kabul edildi")
	}
	// Eski zaman damgasi (6 dk) ve gelecek (6 dk)
	if reached(mk(a, b, s.now.Add(-6*time.Minute))) {
		t.Fatal("eski zaman damgasi kabul edildi")
	}
	if reached(mk(a, b, s.now.Add(6*time.Minute))) {
		t.Fatal("gelecek zaman damgasi kabul edildi")
	}
	// Kisa/cop paketler panik yapmaz
	for _, j := range [][]byte{nil, {1, 2, 3}, discoMagic[:], append(discoMagic[:], make([]byte, 100)...)} {
		b.pm.handleDisco(j, a.src)
	}
}

func TestReplayCacheExpiry(t *testing.T) {
	r := newReplayCache()
	now := time.Unix(1000, 0)
	var n [24]byte
	n[0] = 1
	if !r.check(n, now) || r.check(n, now.Add(time.Second)) {
		t.Fatal("ilk kabul, ikinci red olmali")
	}
	// Pencere sonrasi temizlenir (zaman damgasi kontrolu ayrica korur).
	later := now.Add(replayKeep + 2*time.Minute)
	if !r.check(n, later) {
		t.Fatal("pencere sonrasi nonce unutulmali")
	}
}

func TestRejectsBadCandidates(t *testing.T) {
	s, a, b := twoNodes(t)
	_ = s
	a.pm.punch(b.pub, []string{"127.0.0.1:5", "0.0.0.0:7", "[fe80::1]:9", "224.0.0.1:3", "10.0.0.2:0", "x", "10.0.0.2:2000"})
	a.pm.mu.Lock()
	n := len(a.pm.peers[b.ip].cands)
	a.pm.mu.Unlock()
	if n != 1 {
		t.Fatalf("yalniz gecerli aday kabul edilmeli: %d", n)
	}
	// Bilinmeyen es icin punch yok sayilir.
	a.pm.punch("yok", []string{"10.0.0.2:2000"})
}

func TestPeerReKeyResetsPath(t *testing.T) {
	s, a, b := twoNodes(t)
	a.pm.punch(b.pub, []string{"10.0.0.2:2000"})
	b.pm.punch(a.pub, []string{"10.0.0.1:1000"})
	s.advance(time.Second)
	if pathOf(a, b) != protocol.PathDirect {
		t.Fatal("on kosul")
	}
	nk, _ := GenerateDiscoKey()
	if !a.pm.setPeer(b.ip, b.pub, nk.PubB64()) {
		t.Fatal("anahtari degisen es 'taze' sayilmali")
	}
	if pathOf(a, b) != protocol.PathRelay {
		t.Fatal("anahtar degisince yol sifirlanmali")
	}
	// Ayni anahtarla tekrar cagri etkisiz.
	if a.pm.setPeer(b.ip, b.pub, nk.PubB64()) {
		t.Fatal("ayni kayit taze sayilmamali")
	}
	a.pm.removePeer(b.ip)
	if len(a.pm.snapshot()) != 0 {
		t.Fatal("es silinmedi")
	}
}
