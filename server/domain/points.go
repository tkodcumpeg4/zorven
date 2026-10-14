package domain

import (
	"context"
	"fmt"
	"math/rand/v2"
	"net"
	"strings"
	"time"

	"golang.org/x/net/publicsuffix"
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
	// Uretimde (r == nil) ad once zone'un yetkili sunucularina sorulur: kullanici
	// kaydi degistirdikten sonra ozyinelemeli onbellekler eski degeri verir.
	authoritative := r == nil
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
	got, err := lookupFQDN(ctx, r, fqdn, authoritative)
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

// lookupFQDN, adin IP'lerini doner; authoritative ise once yetkili sunuculara
// sorar, olmazsa verilen cozumleyiciye duser.
func lookupFQDN(ctx context.Context, r IPResolver, fqdn string, authoritative bool) ([]net.IPAddr, error) {
	fqdn = strings.TrimSuffix(strings.ToLower(fqdn), ".")
	if authoritative {
		actx, cancel := context.WithTimeout(ctx, 3*time.Second)
		ips, err := lookupAuthoritativeIPs(actx, fqdn)
		cancel()
		if err == nil {
			return ips, nil
		}
	}
	return r.LookupIPAddr(ctx, fqdn)
}

// HasWildcard, fqdn'in kayit edilebilir alaninda (example.org) platforma isaret
// eden bir joker kayit (*.example.org) olup olmadigini soyler: rastgele bir alt ad
// platform IP'lerine cozuluyorsa true. Joker varsa o alanin tum alt adlari ayri
// DNS kaydi olmadan trafik alir; panel bu durumda alt alan icin kayit istemez.
func HasWildcard(ctx context.Context, r IPResolver, fqdn, platformDomain string) bool {
	zone, err := publicsuffix.EffectiveTLDPlusOne(strings.TrimSuffix(strings.ToLower(fqdn), "."))
	if err != nil {
		return false
	}
	probe := fmt.Sprintf("zv-wc-%08x.%s", rand.Uint32(), zone)
	return PointsToPlatform(ctx, r, probe, platformDomain)
}
