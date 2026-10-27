package wgnet

import (
	"container/list"
	"net/netip"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/tkodcumpeg4/zorven/shared/protocol"
)

// aclRule, sunucunun netmap'te gonderdigi bir izin kurali (derlenmis).
type aclRule struct {
	id     string // sunucudaki kural kimligi (erisim gunlugu)
	src    []netip.Prefix
	proto  uint8 // 0 = any
	lo, hi uint16
	ports  bool // port kisiti var
}

// compileACL, protokol kurallarini derler; gecersizleri atlar.
func compileACL(in []protocol.NetACLRule) []aclRule {
	out := make([]aclRule, 0, len(in))
	for _, r := range in {
		cr := aclRule{id: r.ID}
		switch strings.ToLower(r.Proto) {
		case "tcp":
			cr.proto = protoTCP
		case "udp":
			cr.proto = protoUDP
		case "icmp":
			cr.proto = protoICMP
		case "any", "":
		default:
			continue
		}
		if p := strings.TrimSpace(r.Ports); p != "" {
			lo, hi, ok := parsePortRange(p)
			if !ok || cr.proto == protoICMP {
				continue
			}
			cr.lo, cr.hi, cr.ports = lo, hi, true
		}
		for _, s := range r.Src {
			if pf, err := netip.ParsePrefix(s); err == nil && pf.Addr().Is4() {
				cr.src = append(cr.src, pf.Masked())
			} else if a, err := netip.ParseAddr(s); err == nil && a.Is4() {
				cr.src = append(cr.src, netip.PrefixFrom(a, 32))
			}
		}
		if len(cr.src) == 0 {
			continue
		}
		out = append(out, cr)
	}
	return out
}

func parsePortRange(s string) (lo, hi uint16, ok bool) {
	a, b, isRange := strings.Cut(s, "-")
	l, err := strconv.Atoi(strings.TrimSpace(a))
	if err != nil || l < 1 || l > 65535 {
		return 0, 0, false
	}
	h := l
	if isRange {
		if h, err = strconv.Atoi(strings.TrimSpace(b)); err != nil || h < l || h > 65535 {
			return 0, 0, false
		}
	}
	return uint16(l), uint16(h), true
}

// match: gelen paket (dst bu dugum) bu kurala uyuyor mu. Parcalanmis paketler
// buraya gelmez. ICMP kurali yalniz echo request'i acar (cevaplar conntrack'ten).
func (r *aclRule) match(p *pktInfo) bool {
	in := false
	for _, pf := range r.src {
		if pf.Contains(p.src) {
			in = true
			break
		}
	}
	if !in {
		return false
	}
	if r.proto != 0 && r.proto != p.proto {
		return false
	}
	switch p.proto {
	case protoTCP, protoUDP:
		if r.ports && (p.dstPort < r.lo || p.dstPort > r.hi) {
			return false
		}
		return true
	case protoICMP:
		return !r.ports && p.icmpType == icmpEchoRequest
	}
	return false // bilinmeyen protokoller (ESP, GRE...) acilmaz
}

// --- baglanti takibi (conntrack-lite) ---------------------------------------------

// Zaman asimlari (tasarim 5.3).
const (
	ctTCPTimeout  = 5 * time.Minute
	ctUDPTimeout  = 30 * time.Second
	ctICMPTimeout = 10 * time.Second
	ctMaxEntries  = 8192
)

// ctKey, yerel olarak baslatilan bir akis: (yerel -> uzak). ICMP echo icin
// lport = echo kimligi, rport = 0.
type ctKey struct {
	proto         uint8
	local, remote netip.Addr
	lport, rport  uint16
}

type ctEntry struct {
	key     ctKey
	expires time.Time
}

type conntrack struct {
	mu  sync.Mutex
	max int
	now func() time.Time
	ll  *list.List // on = en yeni
	m   map[ctKey]*list.Element
}

func newConntrack(max int, now func() time.Time) *conntrack {
	if now == nil {
		now = time.Now
	}
	if max <= 0 {
		max = ctMaxEntries
	}
	return &conntrack{max: max, now: now, ll: list.New(), m: map[ctKey]*list.Element{}}
}

func ctTimeout(proto uint8) time.Duration {
	switch proto {
	case protoTCP:
		return ctTCPTimeout
	case protoUDP:
		return ctUDPTimeout
	}
	return ctICMPTimeout
}

// track, yerel baslatilan akisi ekler/tazeler.
func (c *conntrack) track(k ctKey) {
	c.mu.Lock()
	defer c.mu.Unlock()
	exp := c.now().Add(ctTimeout(k.proto))
	if el, ok := c.m[k]; ok {
		el.Value.(*ctEntry).expires = exp
		c.ll.MoveToFront(el)
		return
	}
	c.m[k] = c.ll.PushFront(&ctEntry{key: k, expires: exp})
	for c.ll.Len() > c.max { // LRU
		back := c.ll.Back()
		delete(c.m, back.Value.(*ctEntry).key)
		c.ll.Remove(back)
	}
}

// seen, donus paketinin ait oldugu akisin canli olup olmadigini soyler ve
// (canliysa) tazeler.
func (c *conntrack) seen(k ctKey) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	el, ok := c.m[k]
	if !ok {
		return false
	}
	e := el.Value.(*ctEntry)
	now := c.now()
	if now.After(e.expires) {
		delete(c.m, k)
		c.ll.Remove(el)
		return false
	}
	e.expires = now.Add(ctTimeout(k.proto))
	c.ll.MoveToFront(el)
	return true
}

func (c *conntrack) size() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.ll.Len()
}
