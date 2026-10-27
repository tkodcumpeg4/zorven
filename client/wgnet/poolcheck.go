package wgnet

import (
	"fmt"
	"net"
	"net/netip"
	"strings"
)

// IfaceAddr, yerel bir arayuz adresi (cakisma denetimi girdisi).
type IfaceAddr struct {
	Iface  string
	Prefix netip.Prefix
}

// PoolConflict, ic adres havuzu ile bir yerel arayuz arasindaki cakisma.
type PoolConflict struct {
	Iface  string
	Prefix netip.Prefix
}

func (c PoolConflict) String() string { return fmt.Sprintf("%s (%s)", c.Iface, c.Prefix) }

// CheckPoolConflict, havuzla ortusen yerel arayuz adreslerini bulur. Ortusme:
// arayuzun adresi havuzun icinde ya da arayuzun bagli agi (on ek) havuzla kesisiyor
// (orn. Tailscale 100.x/32, ayni araligi kullanan bir VPN veya LAN).
// skipIface ("zorven0" gibi) onceki bir calismadan kalan kendi arayuzumuzdur.
func CheckPoolConflict(pool netip.Prefix, addrs []IfaceAddr, skipIface string) []PoolConflict {
	var out []PoolConflict
	pool = pool.Masked()
	for _, a := range addrs {
		if skipIface != "" && a.Iface == skipIface {
			continue
		}
		if !a.Prefix.Addr().Is4() || a.Prefix.Addr().IsLoopback() {
			continue
		}
		if pool.Overlaps(a.Prefix.Masked()) || pool.Contains(a.Prefix.Addr()) {
			out = append(out, PoolConflict{a.Iface, a.Prefix})
		}
	}
	return out
}

// LocalIfaceAddrs, yerel arayuz IPv4 adreslerini listeler.
func LocalIfaceAddrs() ([]IfaceAddr, error) {
	ifs, err := net.Interfaces()
	if err != nil {
		return nil, err
	}
	var out []IfaceAddr
	for _, ifc := range ifs {
		if ifc.Flags&net.FlagUp == 0 {
			continue
		}
		as, err := ifc.Addrs()
		if err != nil {
			continue
		}
		for _, a := range as {
			if ipn, ok := a.(*net.IPNet); ok {
				if p, ok := netip.AddrFromSlice(ipn.IP); ok {
					ones, _ := ipn.Mask.Size()
					out = append(out, IfaceAddr{ifc.Name, netip.PrefixFrom(p.Unmap(), ones)})
				}
			}
		}
	}
	return out, nil
}

// PoolConflictError, kullaniciya acik hata metni uretir.
func PoolConflictError(pool netip.Prefix, cs []PoolConflict) error {
	parts := make([]string, 0, len(cs))
	for _, c := range cs {
		parts = append(parts, c.String())
	}
	return fmt.Errorf("Zorven ag araligi %s yerel arayuzlerle cakisiyor: %s. "+
		"Tam ag katmani (Mod B) baslatilmadi. Cozum: panelde Zorven Net > Ag araligi'ndan "+
		"baska bir aralik secin (ornek 10.99.0.0/16; hic dugum kayitliyken degistirilebilir) "+
		"veya cakisan arayuzu kapatin", pool, strings.Join(parts, ", "))
}
