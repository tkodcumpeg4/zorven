package tunsec

// Guvenilir IP'ler: bu adreslerden gelen istekler tunel WAF'i, bot sorgusu ve
// hiz sinirindan (istege bagli Geo'dan da) muaftir. Erisim denetimleri (ziyaretci
// girisi, IP izin listesi, politika deny, mTLS) yine gecerlidir.

import (
	"fmt"
	"net/netip"
	"strings"
)

// Sinirlar.
const (
	MaxTrustedCIDRs  = 100
	minTrustedBits4  = 8  // IPv4 icin en genis /8
	minTrustedBits6  = 32 // IPv6 icin en genis /32
	maxTrustedLength = 64
)

// TrustedConfig, tunel guvenilir IP ayari.
type TrustedConfig struct {
	CIDRs   []string `json:"cidrs"`
	SkipGeo bool     `json:"skip_geo"`
}

func normalizeTrusted(t TrustedConfig) (TrustedConfig, error) {
	if len(t.CIDRs) > MaxTrustedCIDRs {
		return t, fmt.Errorf("trusted.cidrs: en fazla %d girdi", MaxTrustedCIDRs)
	}
	seen := map[string]bool{}
	out := []string{}
	for _, raw := range t.CIDRs {
		raw = strings.TrimSpace(raw)
		if raw == "" {
			continue
		}
		if len(raw) > maxTrustedLength {
			return t, fmt.Errorf("trusted.cidrs: gecersiz girdi %q", raw)
		}
		p, err := parseTrusted(raw)
		if err != nil {
			return t, fmt.Errorf("trusted.cidrs: gecersiz IP/CIDR %q", raw)
		}
		min := minTrustedBits6
		if p.Addr().Is4() {
			min = minTrustedBits4
		}
		if p.Bits() < min {
			return t, fmt.Errorf("trusted.cidrs: %q cok genis (IPv4 en fazla /%d, IPv6 en fazla /%d); herkesi guvenilir yapmak korumayi kapatir",
				raw, minTrustedBits4, minTrustedBits6)
		}
		s := p.String()
		if seen[s] {
			continue
		}
		seen[s] = true
		out = append(out, s)
	}
	t.CIDRs = out
	return t, nil
}

// parseTrusted, tek IP'yi /32 (/128) yapar, CIDR'i ag adresine indirger.
// IPv4-mapped IPv6 (::ffff:a.b.c.d) IPv4'e acilir.
func parseTrusted(s string) (netip.Prefix, error) {
	if strings.Contains(s, "/") {
		p, err := netip.ParsePrefix(s)
		if err != nil {
			return netip.Prefix{}, err
		}
		a := p.Addr()
		if a.Is4In6() {
			if p.Bits() < 96 {
				return netip.Prefix{}, fmt.Errorf("mapped")
			}
			p = netip.PrefixFrom(a.Unmap(), p.Bits()-96)
		}
		return p.Masked(), nil
	}
	a, err := netip.ParseAddr(s)
	if err != nil {
		return netip.Prefix{}, err
	}
	a = a.Unmap()
	return netip.PrefixFrom(a, a.BitLen()), nil
}

// IsTrusted, ip guvenilir listede mi.
func (c *Compiled) IsTrusted(ip string) bool {
	if c == nil || len(c.trusted) == 0 {
		return false
	}
	a, err := netip.ParseAddr(strings.TrimSpace(ip))
	if err != nil {
		return false
	}
	a = a.Unmap().WithZone("")
	for _, p := range c.trusted {
		if p.Contains(a) {
			return true
		}
	}
	return false
}
