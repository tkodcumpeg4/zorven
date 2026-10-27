package wgnet

import (
	"net/netip"
	"sort"
	"sync"
	"time"

	"github.com/tkodcumpeg4/zorven/shared/protocol"
)

// Yol yonetimi (magicsock'un asgari karsiligi). Her es icin iki yol vardir:
// relay (her zaman) ve dogrudan UDP (yalniz disco pong ile dogrulaninca).
//
//   - Ilk paketler relay'den gider; dogrudan yol dogrulaninca gecilir.
//   - Dogrudan yol directTimeout (30 sn) boyunca yanitsizsa relay'e dusulur.
//   - Dogrudan yol varken keepalive (25 sn) aralikla hafif ping atilir; 25-30 sn
//     arasinda yanitsiz kalirsa 3 sn aralikla yeniden denenir.
//   - Dogrulama basarisizsa (simetrik NAT / UDP engelli) probeWindow sonra durur,
//     retryBackoff ile (30 sn -> 5 dk) bir daha denenir; trafik kalici relay'dedir.
//
// Zaman sabitleri degisken: yalniz paket ici testler kisaltir.
var (
	probeInterval = 900 * time.Millisecond
	probeWindow   = 12 * time.Second
	keepalive     = 25 * time.Second
	directTimeout = 30 * time.Second
	pingRetry     = 3 * time.Second
	retryMin      = 30 * time.Second
	retryMax      = 5 * time.Minute
	pendingTTL    = 10 * time.Second
	verifiedTTL   = 2 * time.Minute
)

const (
	maxCands = 2 * protocol.MaxNetEndpoints
	// switchGain: baska bir adresin RTT'si mevcuttan bu kadar iyiyse yol degisir.
	switchGain = 0.75
)

type probe struct {
	addr netip.AddrPort
	sent time.Time
}

type peerPath struct {
	ip       netip.Addr
	wgPub    string
	discoPub [32]byte
	hasDisco bool
	shared   [32]byte

	cands      []netip.AddrPort // sunucudan gelen + es-yansimali
	probeUntil time.Time
	nextProbe  time.Time
	retryAt    time.Time
	retryWait  time.Duration
	askServer  bool // net_punch_req gonderilmeli

	pending map[[12]byte]probe
	// verified: bu esten gelen WG paketlerinin kabul edildigi adresler.
	verified map[netip.AddrPort]time.Time

	direct    netip.AddrPort
	directOK  bool
	rtt       time.Duration
	lastAlive time.Time
	lastPing  time.Time
}

// PeerPath, bir esin disariya gosterilen yol durumu.
type PeerPath struct {
	WGPub    string
	IP       netip.Addr
	Path     string // direct | relay
	Addr     netip.AddrPort
	RTT      time.Duration
	HasDisco bool
}

// pathMgr, yol secimi durum makinesidir. Saat ve gonderim enjekte edilir
// (testlerde sahte saat + bellek ici ag).
type pathMgr struct {
	mu   sync.Mutex
	now  func() time.Time
	self DiscoKey

	peers   map[netip.Addr]*peerPath
	byDisco map[[32]byte]*peerPath
	byAddr  map[netip.AddrPort]*peerPath // verified adresler
	replay  *replayCache

	sendUDP       func(netip.AddrPort, []byte) error
	onChange      func()             // yol durumu degisti (kilit disinda)
	requestPunch  func(wgPub string) // net_punch_req (kilit disinda)
	allowLoopback bool
}

func newPathMgr(self DiscoKey, now func() time.Time) *pathMgr {
	if now == nil {
		now = time.Now
	}
	return &pathMgr{
		now: now, self: self, peers: map[netip.Addr]*peerPath{}, byDisco: map[[32]byte]*peerPath{},
		byAddr: map[netip.AddrPort]*peerPath{}, replay: newReplayCache(),
	}
}

// acceptCandidate: sunucudan/esten gelen adayin yoklanmasina izin verilir mi.
func (pm *pathMgr) acceptCandidate(ap netip.AddrPort) bool {
	a := ap.Addr().Unmap()
	if !ap.IsValid() || ap.Port() == 0 || a.IsUnspecified() || a.IsMulticast() || a.IsLinkLocalUnicast() || a.IsLinkLocalMulticast() {
		return false
	}
	return !a.IsLoopback() || pm.allowLoopback
}

// setPeer, esi ekler/gunceller. Es disco anahtarini degistirdiyse (yeniden
// baglandi) yol durumu sifirlanir. Donus: yeni (veya anahtari degisen) mi.
func (pm *pathMgr) setPeer(ip netip.Addr, wgPub, discoPubB64 string) (fresh bool) {
	pm.mu.Lock()
	defer pm.mu.Unlock()
	p := pm.peers[ip]
	dp, ok := decodeDiscoPub(discoPubB64)
	if p != nil && p.wgPub == wgPub && p.hasDisco == ok && p.discoPub == dp {
		return false
	}
	if p != nil {
		pm.dropLocked(p)
	}
	p = &peerPath{ip: ip, wgPub: wgPub, pending: map[[12]byte]probe{}, verified: map[netip.AddrPort]time.Time{}, retryWait: retryMin}
	if ok {
		p.discoPub, p.hasDisco = dp, true
		p.shared = pm.self.sharedKey(dp)
		pm.byDisco[dp] = p
	}
	pm.peers[ip] = p
	return ok
}

func (pm *pathMgr) dropLocked(p *peerPath) {
	for a := range p.verified {
		if pm.byAddr[a] == p {
			delete(pm.byAddr, a)
		}
	}
	if p.hasDisco && pm.byDisco[p.discoPub] == p {
		delete(pm.byDisco, p.discoPub)
	}
	delete(pm.peers, p.ip)
}

func (pm *pathMgr) removePeer(ip netip.Addr) {
	pm.mu.Lock()
	if p := pm.peers[ip]; p != nil {
		pm.dropLocked(p)
	}
	pm.mu.Unlock()
	pm.fireChange()
}

func (pm *pathMgr) fireChange() {
	if pm.onChange != nil {
		pm.onChange()
	}
}

// punch: sunucu net_punch gonderdi: es adaylarini al ve hemen yoklamaya basla.
func (pm *pathMgr) punch(wgPub string, eps []string) {
	now := pm.now()
	pm.mu.Lock()
	var p *peerPath
	for _, c := range pm.peers {
		if c.wgPub == wgPub {
			p = c
			break
		}
	}
	if p == nil || !p.hasDisco {
		pm.mu.Unlock()
		return
	}
	for _, s := range eps {
		if len(p.cands) >= maxCands {
			break
		}
		ap, err := netip.ParseAddrPort(s)
		if err != nil {
			continue
		}
		ap = netip.AddrPortFrom(ap.Addr().Unmap(), ap.Port())
		if pm.acceptCandidate(ap) && !containsAP(p.cands, ap) {
			p.cands = append(p.cands, ap)
		}
	}
	p.probeUntil = now.Add(probeWindow)
	p.nextProbe = now
	p.retryWait = retryMin
	p.retryAt = time.Time{}
	pm.mu.Unlock()
	pm.step(now)
}

func containsAP(l []netip.AddrPort, a netip.AddrPort) bool {
	for _, x := range l {
		if x == a {
			return true
		}
	}
	return false
}

// step: zamana bagli isler (yoklama turlari, keepalive, zaman asimi, yeniden deneme).
func (pm *pathMgr) step(now time.Time) {
	type outPkt struct {
		to  netip.AddrPort
		pkt []byte
	}
	var out []outPkt
	var reqs []string
	changed := false

	pm.mu.Lock()
	for _, p := range pm.peers {
		if !p.hasDisco {
			continue
		}
		for tx, pr := range p.pending {
			if now.Sub(pr.sent) > pendingTTL {
				delete(p.pending, tx)
			}
		}
		for a, t := range p.verified {
			if now.Sub(t) > verifiedTTL && a != p.direct {
				delete(p.verified, a)
				if pm.byAddr[a] == p {
					delete(pm.byAddr, a)
				}
			}
		}
		if p.directOK && now.Sub(p.lastAlive) > directTimeout {
			p.directOK = false
			p.rtt = 0
			p.probeUntil = now // yeni pencere net_punch ile (hemen istenir)
			p.retryAt = time.Time{}
			p.retryWait = retryMin
			p.askServer = true
			changed = true
		}
		ping := func(to netip.AddrPort) {
			var tx [12]byte
			fillRand(tx[:])
			pkt, err := sealDisco(pm.self, &p.shared, discoMsg{Kind: discoPing, TS: now, Tx: tx})
			if err != nil {
				return
			}
			p.pending[tx] = probe{addr: to, sent: now}
			out = append(out, outPkt{to, pkt})
		}
		if now.Before(p.probeUntil) && !now.Before(p.nextProbe) {
			for _, c := range p.cands {
				ping(c)
			}
			p.nextProbe = now.Add(probeInterval)
			p.lastPing = now
		}
		if p.directOK && now.Sub(p.lastAlive) >= keepalive && now.Sub(p.lastPing) >= pingRetry {
			ping(p.direct)
			p.lastPing = now
		}
		if !p.directOK && !p.probeUntil.IsZero() && !now.Before(p.probeUntil) {
			if p.retryAt.IsZero() {
				p.retryAt = p.probeUntil.Add(p.retryWait)
			}
			if !now.Before(p.retryAt) {
				// Yeniden deneme: sunucudan taze aday iste ve eldekileri yokla.
				p.askServer = true
				p.probeUntil = now.Add(probeWindow)
				p.nextProbe = now
				p.retryAt = time.Time{}
				p.retryWait *= 2
				if p.retryWait > retryMax {
					p.retryWait = retryMax
				}
			}
		}
		if p.askServer {
			p.askServer = false
			reqs = append(reqs, p.wgPub)
		}
	}
	send := pm.sendUDP
	pm.mu.Unlock()

	if send != nil {
		for _, o := range out {
			_ = send(o.to, o.pkt)
		}
	}
	if pm.requestPunch != nil {
		for _, r := range reqs {
			pm.requestPunch(r)
		}
	}
	if changed {
		pm.fireChange()
	}
}

// handleDisco: UDP'den gelen bir disco paketini isler (src = paketin kaynagi).
func (pm *pathMgr) handleDisco(pkt []byte, src netip.AddrPort) {
	if !isDisco(pkt) {
		return
	}
	src = netip.AddrPortFrom(src.Addr().Unmap(), src.Port())
	now := pm.now()

	pm.mu.Lock()
	p := pm.byDisco[discoSender(pkt)]
	if p == nil {
		pm.mu.Unlock()
		return // bilinmeyen gonderen: sessizce at (anahtar sizdirmaz)
	}
	msg, nonce, err := openDisco(&p.shared, pkt)
	if err != nil {
		pm.mu.Unlock()
		return
	}
	if d := now.Sub(msg.TS); d > discoSkew || d < -discoSkew {
		pm.mu.Unlock()
		return // eski/gelecek zaman damgasi
	}
	if !pm.replay.check(nonce, now) {
		pm.mu.Unlock()
		return // tekrar oynatma
	}
	var reply []byte
	var changed bool
	var pingNow bool
	switch msg.Kind {
	case discoPing:
		// Geçerli yoklama: bu adresten gelen WG paketleri kabul edilir ve adres
		// aday olarak eklenir (es-yansimali). Pong, yoklamanin geldigi adrese doner.
		pm.verifyLocked(p, src, now)
		if pm.acceptCandidate(src) && !containsAP(p.cands, src) && len(p.cands) < maxCands {
			p.cands = append(p.cands, src)
		}
		if !p.directOK && now.After(p.probeUntil) {
			p.probeUntil = now.Add(probeWindow)
			p.nextProbe = now
			pingNow = true
		}
		reply, _ = sealDisco(pm.self, &p.shared, discoMsg{Kind: discoPong, TS: now, Tx: msg.Tx, Seen: src})
	case discoPong:
		pr, ok := p.pending[msg.Tx]
		if !ok {
			pm.mu.Unlock()
			return // bekleyen yoklamaya ait degil (tekrar/uydurma)
		}
		delete(p.pending, msg.Tx)
		rtt := now.Sub(pr.sent)
		pm.verifyLocked(p, src, now)
		switch {
		case !p.directOK:
			p.direct, p.directOK, p.rtt, p.lastAlive = src, true, rtt, now
			p.retryWait = retryMin
			p.retryAt = time.Time{}
			changed = true
		case src == p.direct:
			p.rtt, p.lastAlive = rtt, now
		default:
			p.lastAlive = now
			if float64(rtt) < float64(p.rtt)*switchGain && p.rtt-rtt > 2*time.Millisecond {
				p.direct, p.rtt = src, rtt
				changed = true
			}
		}
	}
	send := pm.sendUDP
	pm.mu.Unlock()

	if reply != nil && send != nil {
		_ = send(src, reply)
	}
	if pingNow {
		pm.step(now)
	}
	if changed {
		pm.fireChange()
	}
}

func (pm *pathMgr) verifyLocked(p *peerPath, a netip.AddrPort, now time.Time) {
	if old := pm.byAddr[a]; old != nil && old != p {
		delete(old.verified, a)
	}
	if len(p.verified) >= 16 {
		var oldest netip.AddrPort
		var ot time.Time
		for k, t := range p.verified {
			if ot.IsZero() || t.Before(ot) {
				oldest, ot = k, t
			}
		}
		delete(p.verified, oldest)
		if pm.byAddr[oldest] == p {
			delete(pm.byAddr, oldest)
		}
	}
	p.verified[a] = now
	pm.byAddr[a] = p
}

// route: ese gonderim icin dogrudan adres (dogrulanmis ve canli ise).
func (pm *pathMgr) route(ip netip.Addr) (netip.AddrPort, bool) {
	now := pm.now()
	pm.mu.Lock()
	defer pm.mu.Unlock()
	p := pm.peers[ip]
	if p == nil || !p.directOK || now.Sub(p.lastAlive) > directTimeout {
		return netip.AddrPort{}, false
	}
	return p.direct, true
}

// lookupSrc: UDP'den gelen WG paketinin kaynak adresi hangi ese ait.
func (pm *pathMgr) lookupSrc(src netip.AddrPort) (netip.Addr, bool) {
	src = netip.AddrPortFrom(src.Addr().Unmap(), src.Port())
	pm.mu.Lock()
	defer pm.mu.Unlock()
	if p := pm.byAddr[src]; p != nil {
		return p.ip, true
	}
	return netip.Addr{}, false
}

// failDirect: gonderim hatasi; yol relay'e dusurulur.
func (pm *pathMgr) failDirect(ip netip.Addr) {
	pm.mu.Lock()
	p := pm.peers[ip]
	changed := p != nil && p.directOK
	if changed {
		p.directOK = false
		p.probeUntil = pm.now()
		p.retryAt = time.Time{}
		p.askServer = true
	}
	pm.mu.Unlock()
	if changed {
		pm.fireChange()
	}
}

// snapshot: disariya gosterilen yol listesi (ada gore degil IP'ye gore sirali).
func (pm *pathMgr) snapshot() []PeerPath {
	now := pm.now()
	pm.mu.Lock()
	defer pm.mu.Unlock()
	out := make([]PeerPath, 0, len(pm.peers))
	for _, p := range pm.peers {
		pp := PeerPath{WGPub: p.wgPub, IP: p.ip, Path: protocol.PathRelay, HasDisco: p.hasDisco}
		if p.directOK && now.Sub(p.lastAlive) <= directTimeout {
			pp.Path, pp.Addr, pp.RTT = protocol.PathDirect, p.direct, p.rtt
		}
		out = append(out, pp)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].IP.Less(out[j].IP) })
	return out
}
