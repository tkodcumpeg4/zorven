package netctl

import (
	"encoding/binary"
	"net/netip"
)

// DefaultPool, varsayilan ic adres havuzu (CGNAT araligi).
var DefaultPool = netip.MustParsePrefix("100.64.0.0/10")

// ipAlloc, kiraci basina IPv4 tahsisi. Havuzun ilk /24 blogu rezerve
// (.1 ileride sanal DNS icin), son adres (yayin) kullanilmaz.
type ipAlloc struct {
	pool netip.Prefix
	next uint32
}

func newAlloc(pool netip.Prefix) *ipAlloc {
	base := a2u(pool.Masked().Addr())
	return &ipAlloc{pool: pool, next: base + 256}
}

func a2u(a netip.Addr) uint32 { b := a.As4(); return binary.BigEndian.Uint32(b[:]) }
func u2a(u uint32) netip.Addr {
	var b [4]byte
	binary.BigEndian.PutUint32(b[:], u)
	return netip.AddrFrom4(b)
}

// alloc, used icinde olmayan siradaki adresi verir.
func (a *ipAlloc) alloc(used func(netip.Addr) bool) (netip.Addr, bool) {
	base := a2u(a.pool.Masked().Addr())
	size := uint32(1) << (32 - a.pool.Bits())
	first := base + 256
	last := base + size - 2
	if last < first {
		return netip.Addr{}, false
	}
	n := last - first + 1
	cur := a.next
	if cur < first || cur > last {
		cur = first
	}
	for i := uint32(0); i < n; i++ {
		ip := u2a(cur)
		if !used(ip) {
			a.next = cur + 1
			return ip, true
		}
		cur++
		if cur > last {
			cur = first
		}
	}
	return netip.Addr{}, false
}

func (a *ipAlloc) contains(ip netip.Addr) bool { return a.pool.Contains(ip) }
