package api

// FAZ 3 / F20 — Alt ag yonlendirmesi (subnet routes) ve F19 site-to-site'in
// temel tasi. Bir cihaz kendi yerel agindan bir araligi (or. 192.168.1.0/24)
// yayinlar; yetkili kullanici o araliktaki IP'lere "zorven connect" ile ulasir.
//
// GUVENLIK:
//   - Yalnizca OZEL adres araliklari yayinlanabilir. 0.0.0.0/0 gibi bir aralik
//     ajani, uyeler icin internete acik bir cikis proxy'sine cevirirdi.
//   - Hedef bir IP LITERALI olmalidir (ad kabul edilmez: DNS cozumlemesi
//     araligin disina cikabilirdi). Ajan ayni kontrolu BAGIMSIZ olarak da yapar.
//   - Cakisan araliklarda EN SPESIFIK olan secilir (uzun onek kazanir).

import (
	"net/netip"
	"strings"

	"github.com/tkodcumpeg4/zorven/server/store"
)

// subnetTargetPrefix, alt ag tunel hedeflerinin oneki. Ajan da ayni oneki bilir.
const subnetTargetPrefix = "subnet:"

// privateRanges, yayinlanabilecek adres alanlari (RFC 1918, RFC 6598 CGNAT, ULA).
var privateRanges = []netip.Prefix{
	netip.MustParsePrefix("10.0.0.0/8"),
	netip.MustParsePrefix("172.16.0.0/12"),
	netip.MustParsePrefix("192.168.0.0/16"),
	netip.MustParsePrefix("100.64.0.0/10"),
	netip.MustParsePrefix("fc00::/7"),
}

// minIPv6SubnetBits, IPv6'da yayinlanabilecek en genis aralik (/48).
const minIPv6SubnetBits = 48

// validPublishableSubnet, araligi dogrular ve kanonik bicimini doner.
// Aralik TAMAMEN bir ozel alanin icinde olmali.
func validPublishableSubnet(v string) (netip.Prefix, string) {
	p, err := netip.ParsePrefix(strings.TrimSpace(v))
	if err != nil {
		return netip.Prefix{}, "aralık CIDR biçiminde olmalı (ör. 192.168.1.0/24)"
	}
	p = p.Masked()
	if p.Addr().Is6() && !p.Addr().Is4In6() && p.Bits() < minIPv6SubnetBits {
		return netip.Prefix{}, "IPv6 aralığı en fazla /48 genişliğinde olabilir"
	}
	for _, r := range privateRanges {
		if r.Bits() <= p.Bits() && r.Contains(p.Addr()) {
			return p, ""
		}
	}
	return netip.Prefix{}, "yalnızca özel adres aralıkları yayınlanabilir (10/8, 172.16/12, 192.168/16, 100.64/10, fc00::/7)"
}

// subnetOf, tunel hedefinden araligi cikarir; alt ag degilse ok=false.
func subnetOf(target string) (netip.Prefix, bool) {
	if !strings.HasPrefix(target, subnetTargetPrefix) {
		return netip.Prefix{}, false
	}
	p, err := netip.ParsePrefix(strings.TrimPrefix(target, subnetTargetPrefix))
	return p, err == nil
}

// pickSubnet, IP'yi iceren EN SPESIFIK alt ag kaynagini secer.
func pickSubnet(ip netip.Addr, tunnels []store.Tunnel) (store.Tunnel, bool) {
	ip = ip.Unmap()
	var best store.Tunnel
	bestBits := -1
	for _, t := range tunnels {
		p, ok := subnetOf(t.Target)
		if !ok || !p.Contains(ip) {
			continue
		}
		if p.Bits() > bestBits {
			best, bestBits = t, p.Bits()
		}
	}
	return best, bestBits >= 0
}
