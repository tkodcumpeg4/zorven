// Package ingress, internetten gelen HTTP isteklerini tunele yonlendirir.
package ingress

import (
	"context"
	"net"
	"strings"
	"sync"

	"github.com/tkodcumpeg4/zorven/server/store"
)

// Router, hostname -> tunel eslestirmesini BELLEKTE tutar.
//
// Neden onbellek: bu sicak yoldur — her HTTP istegi icin calisir.
// Her istekte DB'ye gitmek gereksiz; eslestirmeler nadiren degisir
// (yalnizca admin islemiyle), bu yuzden degisiklikte Reload cagrilir.
type Router struct {
	st store.Store

	mu     sync.RWMutex
	byHost map[string]store.HostRoute // tam eslesme
	wild   map[string]store.HostRoute // "*.example.com" -> ".example.com" anahtariyla
}

func NewRouter(st store.Store) *Router {
	return &Router{
		st:     st,
		byHost: make(map[string]store.HostRoute),
		wild:   make(map[string]store.HostRoute),
	}
}

// Reload, eslestirmeleri veritabanindan yeniden okur.
// Sunucu acilisinda ve her tunel/ad degisikliginden sonra cagrilir.
func (r *Router) Reload(ctx context.Context) error {
	// TUM kiracilarin yonlendirmeleri: gelen istek yalnizca hostname tasir;
	// hangi kiraciya ait oldugunu ancak bu tablodan ogreniriz.
	routes, err := r.st.ListHostRoutes(ctx)
	if err != nil {
		return err
	}

	next := make(map[string]store.HostRoute, len(routes))
	nextWild := make(map[string]store.HostRoute)
	for _, rt := range routes {
		h := normalizeHost(rt.FQDN)
		if suffix, ok := strings.CutPrefix(h, "*."); ok {
			// "*.example.com" -> ".example.com" olarak saklanir; arama sirasinda
			// aday hostname'in bu son ekle bitip bitmedigine bakilir.
			nextWild["."+suffix] = rt
			continue
		}
		next[h] = rt
	}

	r.mu.Lock()
	r.byHost = next
	r.wild = nextWild
	r.mu.Unlock()
	return nil
}

// Lookup, Host basligina karsilik gelen yonlendirmeyi doner.
//
// Oncelik: TAM eslesme her zaman wildcard'i yener. Boylece "*.example.com"
// tanimliyken "api.example.com" icin ozel bir tunel eklenebilir.
func (r *Router) Lookup(host string) (store.HostRoute, bool) {
	h := normalizeHost(host)

	r.mu.RLock()
	defer r.mu.RUnlock()

	if rt, ok := r.byHost[h]; ok {
		return rt, true
	}

	// Wildcard: DNS semantigi geregi "*.example.com" TAM OLARAK BIR etiket
	// esler — "api.example.com" eslesir, "a.b.example.com" ESLESMEZ.
	// Ayrica cipla "example.com" da eslesmez.
	if i := strings.Index(h, "."); i >= 0 {
		if rt, ok := r.wild[h[i:]]; ok {
			return rt, true
		}
	}
	return store.HostRoute{}, false
}

// normalizeHost, Host basligini karsilastirilabilir hale getirir:
// port atilir, kucuk harfe cevrilir, sondaki nokta silinir.
//
// DNS buyuk/kucuk harf duyarsizdir; "API.Example.com:8443" ile
// "api.example.com" ayni tunele isaret etmelidir.
func normalizeHost(host string) string {
	// CRLF enjeksiyonu veya null byte asla kabul edilmez
	if strings.ContainsAny(host, "\r\n\x00") {
		return ""
	}
	h := strings.ToLower(strings.TrimSpace(host))
	// Iceride bosluk veya tab bulunamaz
	if strings.ContainsAny(h, " \t") {
		return ""
	}

	// IPv6 koseli parantez: [::1]:8443
	if strings.HasPrefix(h, "[") {
		if end := strings.LastIndex(h, "]"); end > 0 {
			return h[:end+1]
		}
	}
	if i := strings.LastIndex(h, ":"); i > 0 && !strings.Contains(h[i+1:], ":") {
		h = h[:i]
	}
	return strings.TrimSuffix(h, ".")
}

// DefaultControlHosts, --control-host verilmediginde kontrol duzlemine
// erisim saglayan adresler. Loopback'i varsayilan olarak dahil ediyoruz cunku
// operator sunucuya cogu zaman 127.0.0.1 uzerinden baglanir; aksi halde kendi
// health ucuna erisemez ve istek sessizce ingress'e duserdi.
//
// Uretimde --control-host acikca verilir ve YALNIZCA o deger gecerli olur.
var DefaultControlHosts = []string{"localhost", "127.0.0.1", "[::1]"}

// HostMatchesAny, Host basliginin kontrol hostname'lerinden biriyle eslesip
// eslesmedigini soyler. Port ve buyuk/kucuk harf farklari yok sayilir.
func HostMatchesAny(host string, controlHosts []string) bool {
	h := normalizeHost(host)
	for _, c := range controlHosts {
		if h == normalizeHost(c) {
			return true
		}
	}
	return false
}

// IsIPHost, verilen host veya host:port dizesinin gecerli bir IP adresi (IPv4 veya IPv6)
// olup olmadigini kontrol eder.
func IsIPHost(host string) bool {
	h := normalizeHost(host)
	raw := strings.TrimPrefix(strings.TrimSuffix(h, "]"), "[")
	return net.ParseIP(raw) != nil
}
