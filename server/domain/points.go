package domain

import (
	"context"
	"net"
	"strings"
)

// IPResolver, A/AAAA sorgusunu soyutlar (testlerde mock'lanir).
type IPResolver interface {
	LookupIPAddr(ctx context.Context, host string) ([]net.IPAddr, error)
}

// PointsToPlatform, fqdn'in platformun trafik adresine (cname.<platform>)
// yonlendirilip yonlendirilmedigini soyler: iki adin cozdugu IP kumeleri
// kesisiyorsa true. Sahiplik dogrulamasindan AYRIDIR — ust zone dogrulanmis
// bir alt alan adi da DNS kaydi eklenmeden trafik alamaz.
func PointsToPlatform(ctx context.Context, r IPResolver, fqdn, platformDomain string) bool {
	if r == nil {
		r = net.DefaultResolver
	}
	target := "cname.zorven.app"
	if platformDomain != "" {
		target = "cname." + platformDomain
	}
	want, err := r.LookupIPAddr(ctx, target)
	if err != nil || len(want) == 0 {
		return false
	}
	got, err := r.LookupIPAddr(ctx, strings.TrimSuffix(strings.ToLower(fqdn), "."))
	if err != nil {
		return false
	}
	set := make(map[string]bool, len(want))
	for _, a := range want {
		set[a.IP.String()] = true
	}
	for _, a := range got {
		if set[a.IP.String()] {
			return true
		}
	}
	return false
}
