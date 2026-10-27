package netctl

import (
	"context"
	"encoding/base64"
	"net/netip"
	"sort"
	"time"

	"github.com/tkodcumpeg4/zorven/shared/protocol"
)

// W2: dogrudan P2P yol koordinasyonu. Sunucu aday adresleri YALNIZ iki dugum
// birbirini netmap'inde goruyorsa (paylasim ACL'i) iletir; baska kiraciya veya
// yetkisiz esle hicbir aday paylasilmaz (3.6 kiraci izolasyonu). Sunucu paket
// icerigi gormez; disco muhru uc dugum arasindadir.

const (
	// punchReqMin: ayni cift icin net_punch_req'e yanit araligi.
	punchReqMin = 3 * time.Second
	// endpointsMin: bir dugumun net_endpoints'i icin asgari aralik (kotuye kullanim siniri).
	endpointsMin   = 500 * time.Millisecond
	maxPathReports = 256
)

type pathStat struct {
	path string
	rtt  int
	at   time.Time
}

// validKey32, 32 baytlik anahtarin KANONIK base64 oldugunu dogrular. Go'nun
// cozucusu satir sonlarini ve sifir olmayan dolgu bitlerini kabul eder; ayni
// anahtar farkli dizeyle kaydedilip benzersizlik (key_in_use) atlatilmasin
// diye yeniden kodlanmis hal girdiyle birebir ayni olmali.
func validKey32(b64 string) bool {
	raw, err := base64.StdEncoding.Strict().DecodeString(b64)
	return err == nil && len(raw) == 32 && base64.StdEncoding.EncodeToString(raw) == b64
}

// cleanEndpoints, aday listesini dogrular: gecerli ip:port, port != 0,
// belirsiz/coklu yayin/loopback-disi olmayanlar, tekrarsiz, en fazla MaxNetEndpoints.
// Loopback yalniz allowLoopback ile (testler / ayni makine) kabul edilir.
func cleanEndpoints(in []string, allowLoopback bool) []string {
	seen := map[netip.AddrPort]bool{}
	var out []string
	for _, s := range in {
		ap, err := netip.ParseAddrPort(s)
		if err != nil || ap.Port() == 0 {
			continue
		}
		a := ap.Addr().Unmap()
		if a.IsUnspecified() || a.IsMulticast() || a.IsLinkLocalUnicast() || a.IsLinkLocalMulticast() {
			continue
		}
		if a.IsLoopback() && !allowLoopback {
			continue
		}
		ap = netip.AddrPortFrom(a, ap.Port())
		if seen[ap] {
			continue
		}
		seen[ap] = true
		out = append(out, ap.String())
		if len(out) >= protocol.MaxNetEndpoints {
			break
		}
	}
	sort.Strings(out)
	return out
}

func (a *node) sees(b *node) bool {
	v := a.vis.Load()
	if v == nil {
		return false
	}
	_, ok := (*v)[a2u(b.rec.IP)]
	return ok
}

// punchable: iki dugum da cevrimici, dogrudan yol destekli ve karsilikli gorunur.
func punchable(a, b *node) bool {
	return a != b && a.ob != nil && b.ob != nil && a.punch && b.punch && a.sees(b) && b.sees(a)
}

// sendPunchLocked: a'ya "b ile simdi yokla" (b'nin adaylariyla) gonderir.
func (m *Manager) sendPunchLocked(to, about *node, nonce string) {
	if to.ob == nil || len(about.endpoints) == 0 {
		return
	}
	b, err := protocol.Marshal(protocol.NetPunch{
		Type: protocol.TypeNetPunch, PeerWGPub: about.rec.WGPub, PeerDiscoPub: about.discoPub,
		Endpoints: about.endpoints, Nonce: nonce,
	}, protocol.TypeNetPunch)
	if err == nil {
		to.ob.Ctrl(b)
	}
}

// punchPairLocked: iki yana da birbirinin adaylarini ayni anda gonderir.
func (m *Manager) punchPairLocked(a, b *node) {
	if !punchable(a, b) {
		return
	}
	nonce := randHex(8)
	m.sendPunchLocked(a, b, nonce)
	m.sendPunchLocked(b, a, nonce)
}

func (c *NodeConn) handleEndpoints(data []byte) {
	n := c.node.Load()
	if n == nil {
		return
	}
	var req protocol.NetEndpoints
	if jsonUnmarshal(data, &req) != nil {
		return
	}
	m := c.m
	eps := cleanEndpoints(req.Endpoints, m.AllowLoopbackEndpoints)
	m.mu.Lock()
	defer m.mu.Unlock()
	if !n.punch || n.ob != c.ob {
		return
	}
	now := m.Now()
	limited := now.Sub(n.epAt) < endpointsMin
	n.epAt = now
	if equalStrings(eps, n.endpoints) {
		return
	}
	n.endpoints = eps // asiri sik raporlarda saklanir ama yeniden yayinlanmaz
	if limited {
		n.denied.Add(1)
		return
	}
	t := m.tenants[c.id.TenantID]
	if t == nil {
		return
	}
	for _, p := range t.nodes {
		m.punchPairLocked(n, p)
	}
}

func (c *NodeConn) handlePunchReq(data []byte) {
	n := c.node.Load()
	if n == nil {
		return
	}
	var req protocol.NetPunchReq
	if jsonUnmarshal(data, &req) != nil || req.PeerWGPub == "" {
		return
	}
	m := c.m
	m.mu.Lock()
	defer m.mu.Unlock()
	t := m.tenants[c.id.TenantID]
	if t == nil || !n.punch || n.ob != c.ob {
		return
	}
	var peer *node
	for _, p := range t.nodes {
		if p.rec.WGPub == req.PeerWGPub {
			peer = p
			break
		}
	}
	if peer == nil {
		return
	}
	now := m.Now()
	if n.punchReqAt == nil {
		n.punchReqAt = map[string]time.Time{}
	}
	if last, ok := n.punchReqAt[req.PeerWGPub]; ok && now.Sub(last) < punchReqMin {
		n.denied.Add(1)
		return
	}
	if len(n.punchReqAt) > 512 {
		n.punchReqAt = map[string]time.Time{}
	}
	n.punchReqAt[req.PeerWGPub] = now
	m.punchPairLocked(n, peer)
}

func (c *NodeConn) handleStatus(data []byte) {
	n := c.node.Load()
	if n == nil {
		return
	}
	var req protocol.NetStatus
	if jsonUnmarshal(data, &req) != nil {
		return
	}
	paths := make(map[string]pathStat, len(req.Paths))
	now := c.m.Now()
	for _, p := range req.Paths {
		if len(paths) >= maxPathReports {
			break
		}
		if p.Path != protocol.PathDirect && p.Path != protocol.PathRelay {
			continue
		}
		if p.RTTMs < 0 || p.RTTMs > 60000 {
			p.RTTMs = 0
		}
		paths[p.PeerWGPub] = pathStat{path: p.Path, rtt: p.RTTMs, at: now}
	}
	c.m.mu.Lock()
	if n.ob == c.ob {
		n.paths = paths
	}
	c.m.mu.Unlock()
}

// pathBetween: owner ile y arasindaki yol (owner'in raporu once, yoksa y'ninki).
func pathBetween(owner, y *node) (string, int) {
	if st, ok := owner.paths[y.rec.WGPub]; ok {
		return st.path, st.rtt
	}
	if st, ok := y.paths[owner.rec.WGPub]; ok {
		return st.path, st.rtt
	}
	return "", 0
}

// SummarizePaths, paylasimin bagli alicilarinin yollarini tek degere indirger:
// direct | relay | mixed | "" (bilinmiyor / bagli alici yok).
func SummarizePaths(conn []NodeInfo) string {
	var d, r int
	for _, c := range conn {
		switch c.Path {
		case protocol.PathDirect:
			d++
		case protocol.PathRelay:
			r++
		}
	}
	switch {
	case d > 0 && r > 0:
		return "mixed"
	case d > 0:
		return protocol.PathDirect
	case r > 0:
		return protocol.PathRelay
	}
	return ""
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// handlePunchControl: W2 mesajlarinin ortak girisi (HandleControl ve oturum ucu).
func (c *NodeConn) handlePunchControl(_ context.Context, t protocol.MessageType, data []byte) bool {
	switch t {
	case protocol.TypeNetEndpoints:
		c.handleEndpoints(data)
	case protocol.TypeNetPunchReq:
		c.handlePunchReq(data)
	case protocol.TypeNetStatus:
		c.handleStatus(data)
	default:
		return false
	}
	return true
}
