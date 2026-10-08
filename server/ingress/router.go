// Package ingress, internetten gelen HTTP isteklerini tunele yonlendirir.
package ingress

import (
	"context"
	"crypto/sha256"
	"crypto/x509"
	"encoding/hex"
	"encoding/json"
	"net"
	"sort"
	"strconv"
	"strings"
	"sync"

	"github.com/tkodcumpeg4/zorven/server/ratelimit"
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

	// traffic (FAZ 6): FQDN -> parse edilmis trafik politikasi. Reload'da BIR KEZ
	// parse edilir; ingress hot-path'i yalnizca hazir struct'i uygular.
	// Yalnizca TrafficEnabled tuneller icin doldurulur.
	traffic map[string]*trafficPolicy

	// paths (FAZ 6.5): host -> yol kurallari (en uzun on-ek once sirali).
	// LookupPath, path bir kurala uyarsa o kuralin route'unu doner; degilse
	// varsayilan (byHost/wild) route'a duser.
	paths map[string][]pathRule

	// mtls (FAZ 6.6): host -> istemci sertifikasi CA havuzu. Yalnizca mTLS etkin
	// hostlar icin doldurulur. TLS GetConfigForClient bu haritayi kullanip ClientAuth
	// ister; diger hostlar etkilenmez.
	mtls map[string]*x509.CertPool

	// policies (FAZ 1 / F04): host -> ona bagli etkin policy'ler (priority artan
	// sirali). Reload'da BIR KEZ derlenir; ingress hot-path yalnizca uygular.
	policies map[string][]*compiledPolicy
	// geo, Geo/IP cozumleyicisi (FAZ 1 Part 2 / F03a). nil ise ozellik kapali;
	// Geo kosullari eslesmez ama istek normal akisina devam eder.
	geo *geoResolver

	// lb (FAZ 4 / F21): saglik tablosu + acik istek sayaclari. Deger alani:
	// router tek sefer kurulur ve pointer ile paylasilir.
	lb lbState
	// lbEntries: tunnelID -> yuk dengeleme gorunumu. Reload'da kurulur; kaydi
	// olmayan tunel eski davranisla (round-robin, saglik denetimi yok) calisir.
	lbEntries map[string]*lbEntry

	// intel, liste tabanli IP bilgisi (tor/hosting/reputation) — F03b-d.
	// nil ise ilgili kosullar eslesmez, istek normal akar.
	intel *ipIntel

	// policyLimiters, mevcut snapshot'in rate_limit limiter'lari. Reload swap'inda
	// artik kullanilmayanlar Close edilir (goroutine sizintisi olmasin).
	policyLimiters []*ratelimit.Limiter

	// limiterCache (O1): host|policy|kural|ozet -> limiter. Reload'lar arasi
	// AYNI kuralin limiter'i (ve sayaclari) korunur; aksi halde her 30 sn'lik
	// yoklama veya herhangi bir kiracinin degisikligi rate_limit'i sifirlardi.
	// Yalnizca Reload icinde (reloadMu altinda) okunur/yazilir.
	limiterCache map[string]*ratelimit.Limiter

	// tunnelPolicies (O4): tunele (tunnel_id ile) baglanmis policy'ler. Yol
	// yonlendirmesiyle baska bir tunele giden istekte hedef tunelin policy'leri
	// de uygulanir.
	tunnelPolicies map[string][]*compiledPolicy

	// mtlsByTunnel: tunnelID -> CA havuzu (mTLS etkin tuneller). Ingress, route
	// mTLS istiyorsa istemci sertifikasini BU havuza gore yeniden dogrular (yol
	// yonlendirmesinde hedef tunelin CA'si host'unkinden farkli olabilir).
	mtlsByTunnel map[string]*x509.CertPool

	// reloadMu, Reload'lari serilestirir (O8): es zamanli iki Reload'dan eski
	// olanin yenisini ezmesi ve limiter onbelleginin yarismasi engellenir.
	reloadMu sync.Mutex
}

// pathRule, bir host icin tek bir yol-on-eki -> route eslesmesi.
type pathRule struct {
	prefix string
	route  store.HostRoute
	// traffic, hedef tunelin parse edilmis trafik politikasi (O4); nil olabilir.
	traffic *trafficPolicy
}

func NewRouter(st store.Store) *Router {
	return &Router{
		st:       st,
		byHost:   make(map[string]store.HostRoute),
		wild:     make(map[string]store.HostRoute),
		traffic:  make(map[string]*trafficPolicy),
		paths:    make(map[string][]pathRule),
		mtls:     make(map[string]*x509.CertPool),
		policies: make(map[string][]*compiledPolicy),

		limiterCache:   make(map[string]*ratelimit.Limiter),
		tunnelPolicies: make(map[string][]*compiledPolicy),
		mtlsByTunnel:   make(map[string]*x509.CertPool),
	}
}

// SetGeoResolver, mmdb tabanli Geo cozumleyicisini baglar. Yol bos ise ozellik
// kapali kalir ve nil hata doner — bu bir hata durumu degildir.
// Zorven veritabani DAGITMAZ; yol isletmeciden (ZORVEN_GEOIP_DB) gelir.
func (r *Router) SetGeoResolver(path string) error {
	g, err := newGeoResolver(path)
	if err != nil {
		return err
	}
	r.mu.Lock()
	old := r.geo
	r.geo = g
	r.mu.Unlock()
	old.close()
	return nil
}

// SetIPIntel, liste tabanli IP bilgi saglayicilarini baglar.
// Zorven liste DAGITMAZ; kaynaklar isletmeciden gelir.
func (r *Router) SetIPIntel(x *ipIntel) {
	r.mu.Lock()
	r.intel = x
	r.mu.Unlock()
}

// Reload, eslestirmeleri veritabanindan yeniden okur.
// Sunucu acilisinda ve her tunel/ad degisikliginden sonra cagrilir.
func (r *Router) Reload(ctx context.Context) error {
	r.reloadMu.Lock()
	defer r.reloadMu.Unlock()

	// TUM kiracilarin yonlendirmeleri: gelen istek yalnizca hostname tasir;
	// hangi kiraciya ait oldugunu ancak bu tablodan ogreniriz.
	routes, err := r.st.ListHostRoutes(ctx)
	if err != nil {
		return err
	}

	next := make(map[string]store.HostRoute, len(routes))
	nextWild := make(map[string]store.HostRoute)
	nextTraffic := make(map[string]*trafficPolicy)
	nextMTLS := make(map[string]*x509.CertPool)
	nextMTLSByTunnel := make(map[string]*x509.CertPool)
	addTunnelCA := func(rt store.HostRoute) *x509.CertPool {
		if !rt.MTLSEnabled || strings.TrimSpace(rt.MTLSCAPem) == "" {
			return nil
		}
		if pool, ok := nextMTLSByTunnel[rt.TunnelID]; ok {
			return pool
		}
		pool := x509.NewCertPool()
		if !pool.AppendCertsFromPEM([]byte(rt.MTLSCAPem)) {
			return nil
		}
		nextMTLSByTunnel[rt.TunnelID] = pool
		return pool
	}
	for _, rt := range routes {
		h := normalizeHost(rt.FQDN)
		// Trafik politikasini BIR KEZ parse et (yalnizca etkinse).
		if rt.TrafficEnabled {
			if tp := parseTrafficPolicy(rt.TrafficConfig); tp != nil {
				nextTraffic[h] = tp
			}
		}
		// mTLS: CA PEM'i BIR KEZ parse et (yalnizca etkin + gecerli CA ise).
		if pool := addTunnelCA(rt); pool != nil {
			nextMTLS[h] = pool
		}
		if suffix, ok := strings.CutPrefix(h, "*."); ok {
			// "*.example.com" -> ".example.com" olarak saklanir; arama sirasinda
			// aday hostname'in bu son ekle bitip bitmedigine bakilir.
			nextWild["."+suffix] = rt
			continue
		}
		next[h] = rt
	}

	// Web door hostname'leri (ham TCP/UDP tuneli kapilari). Gercek bir kayit ayni
	// adi zaten tasiyorsa o kazanir (kapi adi hicbir zaman bir tuneli golgelemez).
	// Okunamazsa kapilar bu turda yoktur (404); ham port grant'siz zaten kapali kalir.
	if drs, derr := r.st.ListDoorRoutes(ctx); derr == nil {
		for _, rt := range drs {
			h := normalizeHost(rt.FQDN)
			if _, exists := next[h]; !exists {
				next[h] = rt
			}
		}
	}

	// FAZ 6.5: yol kurallari. Her biri hedef tunelin tam HostRoute'u + PathPrefix.
	// En uzun on-ek once eslessin diye host basina uzunluga gore azalan siralanir.
	nextPaths := make(map[string][]pathRule)
	if pr, perr := r.st.ListPathRouteEntries(ctx); perr == nil {
		for _, rt := range pr {
			if rt.PathPrefix == "" {
				continue
			}
			// O4: yol kuralinin hedef tunelinin trafik politikasi kuralla birlikte
			// tasinir; yola gelen istek varsayilan tunelin degil HEDEF tunelin
			// trafik kurallarini alir. mTLS CA'si da tunel haritasina eklenir.
			var tp *trafficPolicy
			if rt.TrafficEnabled {
				tp = parseTrafficPolicy(rt.TrafficConfig)
			}
			addTunnelCA(rt)
			h := normalizeHost(rt.FQDN)
			nextPaths[h] = append(nextPaths[h], pathRule{prefix: rt.PathPrefix, route: rt, traffic: tp})
		}
		for h := range nextPaths {
			rules := nextPaths[h]
			sort.SliceStable(rules, func(i, j int) bool {
				return len(rules[i].prefix) > len(rules[j].prefix)
			})
			nextPaths[h] = rules
		}
	}

	// FAZ 1 (F04): policy'ler. Etkin policy'ler host cozumuyle okunur; her host icin
	// priority artan sirali derlenir. rate_limit limiter'lari burada olusur.
	nextPolicies := make(map[string][]*compiledPolicy)
	nextTunnelPolicies := make(map[string][]*compiledPolicy)
	var nextLimiters []*ratelimit.Limiter
	nextLimCache := make(map[string]*ratelimit.Limiter)
	proutes, policyErr := r.st.ListPolicyRoutes(ctx)
	if policyErr == nil {
		// {{secret:ad}} referanslari BURADA, derleme zamaninda cozulur (FAZ 1 Part 2 / S0).
		// Istek basina cozmek DB turu demek olurdu; sicak yol kurali bunu yasakliyor.
		// Ayni kiraci/proje/ad ucusu tekrar sorgulanmasin diye kucuk bir onbellek tutulur.
		secCache := make(map[string]string)
		resolveRefs := func(pol store.Policy) map[string]string {
			refs := policySecretRefs(pol.Config)
			if len(refs) == 0 {
				return nil
			}
			out := make(map[string]string, len(refs))
			for _, name := range refs {
				ck := pol.TenantID + "/" + pol.ProjectID + "/" + name
				if v, ok := secCache[ck]; ok {
					if v != "" {
						out[name] = v
					}
					continue
				}
				v, err := r.st.ResolveSecret(ctx, pol.TenantID, pol.ProjectID, name)
				if err != nil || v == "" {
					// Cozulemeyen referans: onbellege bos yaz, kural compileAction'da dusecek.
					secCache[ck] = ""
					continue
				}
				secCache[ck] = v
				out[name] = v
			}
			return out
		}
		// Ayni policy+tunel bagi birden cok host'a cozulur; tunel haritasina
		// bir kez eklensin.
		seenTunnelPol := make(map[string]bool)
		for _, prt := range proutes {
			h := normalizeHost(prt.Host)
			pol := prt.Policy
			mk := func(idx int, rule rawPolicyRule, perSecond float64, burst int) *ratelimit.Limiter {
				key := limiterCacheKey(h, pol.ID, idx, rule)
				if lim, ok := nextLimCache[key]; ok {
					return lim
				}
				lim, ok := r.limiterCache[key]
				if !ok {
					lim = ratelimit.New(perSecond, burst)
				}
				nextLimCache[key] = lim
				return lim
			}
			cp, _ := compilePolicyWith(pol.Config, pol.Priority, resolveRefs(pol), mk)
			if cp == nil {
				continue
			}
			cp.id = pol.ID
			nextPolicies[h] = append(nextPolicies[h], cp)
			if prt.TunnelID != "" && !seenTunnelPol[prt.TunnelID+"|"+pol.ID] {
				seenTunnelPol[prt.TunnelID+"|"+pol.ID] = true
				nextTunnelPolicies[prt.TunnelID] = append(nextTunnelPolicies[prt.TunnelID], cp)
			}
		}
		// Host/tunel basina priority artan sirala (kucuk once uygulanir).
		for _, m := range []map[string][]*compiledPolicy{nextPolicies, nextTunnelPolicies} {
			for k := range m {
				list := m[k]
				sort.SliceStable(list, func(i, j int) bool { return list[i].priority < list[j].priority })
				m[k] = list
			}
		}
		for _, lim := range nextLimCache {
			nextLimiters = append(nextLimiters, lim)
		}
	}

	// Yuk dengeleme (F21): kayitlar BIR KEZ okunur. Okunamazsa ESKI gorunum
	// korunur (fail-stale): gecici bir DB hatasi saglik denetimini kapatmasin.
	nextLB, lbErr := r.buildLBEntries(ctx, routes)

	r.mu.Lock()
	if lbErr == nil {
		r.lbEntries = nextLB
	}
	r.byHost = next
	r.wild = nextWild
	r.traffic = nextTraffic
	r.paths = nextPaths
	r.mtls = nextMTLS
	r.mtlsByTunnel = nextMTLSByTunnel
	var oldLimiters []*ratelimit.Limiter
	if policyErr == nil {
		r.policies = nextPolicies
		r.tunnelPolicies = nextTunnelPolicies
		oldLimiters = r.policyLimiters
		r.policyLimiters = nextLimiters
	}
	// policyErr != nil: policy tablosu okunamadi. ESKI policy snapshot'i korunur
	// (fail-stale, O8); bos haritaya gecmek tum deny/waf kurallarini sessizce
	// kaldirirdi (fail-open).
	r.mu.Unlock()

	if policyErr == nil {
		// Yalnizca ARTIK kullanilmayan limiter'lari kapat (janitor goroutine'leri
		// dursun); yeni snapshot'a tasinanlar sayaclariyla calismaya devam eder.
		for _, lim := range oldLimiters {
			if !limiterIn(nextLimCache, lim) {
				lim.Close()
			}
		}
		r.limiterCache = nextLimCache
	}
	return nil
}

// limiterCacheKey, rate_limit limiter'inin Reload'lar arasi kimligi:
// host + policy id + kural sirasi + kuralin icerik ozeti. Kural (match/action)
// degisirse ozet degisir ve yeni (sifir sayacli) limiter olusur.
func limiterCacheKey(host, policyID string, ruleIdx int, rule rawPolicyRule) string {
	b, _ := json.Marshal(rule)
	sum := sha256.Sum256(b)
	return host + "|" + policyID + "|" + strconv.Itoa(ruleIdx) + "|" + hex.EncodeToString(sum[:8])
}

func limiterIn(m map[string]*ratelimit.Limiter, lim *ratelimit.Limiter) bool {
	for _, v := range m {
		if v == lim {
			return true
		}
	}
	return false
}

// MTLSFor, verilen hostname icin mTLS etkin mi ve imzalayan CA havuzu nedir.
// TLS GetConfigForClient bunu kullanir. Etkin degilse (nil, false).
func (r *Router) MTLSFor(host string) (*x509.CertPool, bool) {
	h := normalizeHost(host)
	r.mu.RLock()
	defer r.mu.RUnlock()
	if pool, ok := r.mtls[h]; ok {
		return pool, true
	}
	return nil, false
}

// LookupPath, Host + istek yoluna gore yonlendirmeyi doner (FAZ 6.5). Once host
// icin tanimli yol kurallari (en uzun eslesen on-ek) denenir; hicbiri uymazsa
// varsayilan route'a (Lookup) duser.
func (r *Router) LookupPath(host, path string) (store.HostRoute, bool) {
	if rule, ok := r.pathRuleFor(host, path); ok {
		return rule.route, true
	}
	return r.Lookup(host)
}

// pathRuleFor, host+yol icin eslesen yol kuralini doner. Yol once canonicalPath
// ile normalize edilir: "//api/x" veya "/./api" de "/api" kuralina gider
// (backend'in gorecegi yolla ayni karar).
func (r *Router) pathRuleFor(host, path string) (pathRule, bool) {
	h := normalizeHost(host)
	cp := canonicalPath(path)
	r.mu.RLock()
	defer r.mu.RUnlock()
	for _, pr := range r.paths[h] {
		if pathMatches(cp, pr.prefix) {
			return pr, true
		}
	}
	return pathRule{}, false
}

// trafficForRoute, istegin gercekte gidecegi route'un trafik politikasi (O4):
// yol kuralindan geldiyse hedef tunelinki, degilse host'unki.
func (r *Router) trafficForRoute(host string, rt store.HostRoute, path string) *trafficPolicy {
	if rt.PathPrefix != "" {
		if rule, ok := r.pathRuleFor(host, path); ok && rule.route.TunnelID == rt.TunnelID {
			return rule.traffic
		}
	}
	return r.trafficFor(host)
}

// tunnelCAPool, mTLS etkin tunelin CA havuzu.
func (r *Router) tunnelCAPool(tunnelID string) (*x509.CertPool, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	p, ok := r.mtlsByTunnel[tunnelID]
	return p, ok
}

// pathMatches, istek yolunun bir on-ek kuralina uyup uymadigi. Tam segment
// eslesmesi: "/api" kurali "/api" ve "/api/..." ile eslesir, "/apix" ile ESLESMEZ.
func pathMatches(path, prefix string) bool {
	if prefix == "" || prefix == "/" {
		return true
	}
	if !strings.HasPrefix(path, prefix) {
		return false
	}
	// Segment siniri: prefix'ten sonra ya son ya da '/' gelmeli.
	if len(path) == len(prefix) {
		return true
	}
	return path[len(prefix)] == '/'
}

// trafficFor, hostname'e karsilik gelen parse edilmis trafik politikasini doner
// (yoksa nil). Tam eslesme wildcard'i yener (Lookup ile ayni oncelik).
func (r *Router) trafficFor(host string) *trafficPolicy {
	h := normalizeHost(host)
	r.mu.RLock()
	defer r.mu.RUnlock()
	if tp, ok := r.traffic[h]; ok {
		return tp
	}
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

// buildLBEntries, yuk dengeleme kayitlarini tunel bilgileriyle birlestirir.
// Yalnizca yonlendirilen (host route'u olan) tuneller icin kayit kurulur.
func (r *Router) buildLBEntries(ctx context.Context, routes []store.HostRoute) (map[string]*lbEntry, error) {
	cfgs, err := r.st.ListTunnelLBs(ctx)
	if err != nil {
		return nil, err
	}
	if len(cfgs) == 0 {
		return map[string]*lbEntry{}, nil
	}
	byTunnel := make(map[string]store.HostRoute, len(routes))
	for _, rt := range routes {
		if _, ok := byTunnel[rt.TunnelID]; !ok {
			byTunnel[rt.TunnelID] = rt
		}
	}
	out := make(map[string]*lbEntry, len(cfgs))
	for _, c := range cfgs {
		rt, ok := byTunnel[c.TunnelID]
		if !ok {
			continue
		}
		out[c.TunnelID] = &lbEntry{cfg: c, clients: candidateClients(rt), proto: rt.Proto}
	}
	return out, nil
}

// lbFor, tunelin yuk dengeleme gorunumu.
func (r *Router) lbFor(tunnelID string) (*lbEntry, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	e, ok := r.lbEntries[tunnelID]
	return e, ok
}

// lbSnapshot, saglik denetleyicisi icin tum gorunumlerin kopyasi.
func (r *Router) lbSnapshot() map[string]*lbEntry {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make(map[string]*lbEntry, len(r.lbEntries))
	for k, v := range r.lbEntries {
		out[k] = v
	}
	return out
}
