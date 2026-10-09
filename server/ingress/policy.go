package ingress

import (
	"bytes"
	"encoding/json"
	"fmt"
	"log/slog"
	"io"
	"net/http"
	"path"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/tkodcumpeg4/zorven/server/ratelimit"
	"github.com/tkodcumpeg4/zorven/server/store"
)

// FAZ 1 (F04): Birlesik Policy motoru. Kurallar match -> action seklindedir ve
// router snapshot'inda BIR KEZ parse edilir (compilePolicy); ingress hot-path'i
// yalnizca hazir struct'i uygular (per-istek JSON parse YOK), tipki trafficPolicy
// gibi. rate_limit action'i kendi token-bucket limiter'ini tasir; Reload eski
// snapshot'in limiter'larini Close ederek goroutine sizintisini onler.

// --- Ham JSON semasi ---

type rawPolicyConfig struct {
	Rules []rawPolicyRule `json:"rules"`
}
type rawPolicyRule struct {
	Match  rawMatch  `json:"match"`
	Action rawAction `json:"action"`
}
type rawMatch struct {
	PathPrefix string            `json:"path_prefix"`
	Methods    []string          `json:"methods"`
	Header     map[string]string `json:"header"`
	// Geo/IP (FAZ 1 Part 2 / F03a). Bos birakilanlar eslesmede dikkate alinmaz.
	Country   []string `json:"country"`   // ISO 3166-1 alpha-2
	Continent []string `json:"continent"` // kita kodu
	ASN       []uint   `json:"asn"`
	ISP       []string `json:"isp"` // ASN organizasyon adi (tam eslesme, buyuk/kucuk duyarsiz)
	// Liste tabanli (F03b-d). *bool: "belirtilmedi" ile "false" ayrimi icin.
	TorExit    *bool    `json:"tor_exit"`
	Hosting    *bool    `json:"hosting"`
	Reputation []string `json:"reputation"` // liste adlari; herhangi biri tutarsa eslesir
}
type rawAction struct {
	Type string `json:"type"`
	// deny
	Status  int    `json:"status"`
	Message string `json:"message"`
	// redirect
	Location string `json:"location"`
	// set_header
	Request  headerRules `json:"request"`
	Response headerRules `json:"response"`
	// rate_limit
	Key       string `json:"key"`        // ip | header:<Ad> | tunnel
	Requests  int    `json:"requests"`   // pencere basina istek
	WindowSec int    `json:"window_sec"` // pencere (sn); 0 => 1
	Burst     int    `json:"burst"`      // kova kapasitesi; 0 => requests
	// verify_webhook (FAZ 1 Part 2 / F05)
	Provider     string `json:"provider"`      // github | stripe | gitlab | shopify | slack
	SecretRef    string `json:"secret_ref"`    // {{secret:ad}} veya duz deger
	ToleranceSec int    `json:"tolerance_sec"` // zaman damgali saglayicilarda; 0 => 300
	// waf (FAZ 1 Part 2 / F02)
	Ruleset  string   `json:"ruleset"`  // "" | owasp-lite
	Patterns []string `json:"patterns"` // kuruma ozel ek regex'ler
}

// --- Derlenmis (hot-path) yapilar ---

type actionKind int

const (
	actNoop actionKind = iota
	actDeny
	actSetHeader
	actRedirect
	actRequireMTLS
	actRateLimit
	actVerifyWebhook
	actWAF
)

type matchCond struct {
	pathPrefix string
	methods    map[string]struct{} // upper; bos => her metot
	header     map[string]string
	// Geo/IP kosullari. needsGeo true ise cozum gerekir; cozulemezse kosul FALSE.
	country   map[string]struct{}
	continent map[string]struct{}
	asn       map[uint]struct{}
	isp       map[string]struct{}
	needsGeo  bool
	// Liste tabanli kosullar (F03b-d)
	torExit    *bool
	hosting    *bool
	reputation map[string]struct{}
	needsIntel bool
}

type policyAction struct {
	kind actionKind
	// deny
	status  int
	message string
	// redirect
	location string
	// set_header
	reqHeaders  headerRules
	respHeaders headerRules
	// rate_limit
	limiter *ratelimit.Limiter
	rlKey   string // ip | header:<Ad> | tunnel
	// verify_webhook
	provider  string
	secret    []byte
	tolerance time.Duration
	// waf
	wafRules []*regexp.Regexp
}

type compiledRule struct {
	match  matchCond
	action policyAction
}

type compiledPolicy struct {
	id       string // policy kimligi (Reload doldurur; yol yonlendirmesinde tekillestirme)
	priority int
	rules    []compiledRule
}

// policyOutcome, evaluate sonucu. Terminal alanlardan en fazla biri set edilir;
// terminal degilse reqHeaders/respHeaders birikmis set_header kurallaridir.
type policyOutcome struct {
	denyStatus     int // >0 => deny
	denyMessage    string
	redirectLoc    string // != "" => redirect
	redirectStatus int
	rateLimited    bool // => 429
	retryAfter     time.Duration
	mtlsFailed     bool // => 403 (require_mtls, dogrulanmamis)
	webhookFailed  bool // => 401/413 (verify_webhook)
	webhookStatus  int
	webhookMessage string
	wafBlocked     bool   // => 403
	wafRule        string // yalnizca LOG icin; yanita yazilmaz

	reqHeaders  *headerRules
	respHeaders *headerRules
}

func (o *policyOutcome) terminal() bool {
	return o.denyStatus > 0 || o.redirectLoc != "" || o.rateLimited || o.mtlsFailed || o.webhookFailed || o.wafBlocked
}

// limiterMaker, rate_limit kurali icin limiter saglar. ruleIdx kuralin config
// icindeki sirasi, rule ham kuraldir (anahtar/ozet uretmek icin). nil ise her
// cagrida yeni limiter olusur.
type limiterMaker func(ruleIdx int, rule rawPolicyRule, perSecond float64, burst int) *ratelimit.Limiter

// compilePolicy, ham config'i derler. Olusturulan tum limiter'lari da doner ki
// Router Reload'da eski snapshot'in limiter'larini Close edebilsin.
func compilePolicy(raw []byte, priority int, secrets map[string]string) (*compiledPolicy, []*ratelimit.Limiter) {
	return compilePolicyWith(raw, priority, secrets, nil)
}

// compilePolicyWith, compilePolicy ile ayni; limiter'lar mk'dan alinir (Router
// Reload'lar arasi ayni kuralin sayaclarini korumak icin onbellekten verir).
func compilePolicyWith(raw []byte, priority int, secrets map[string]string, mk limiterMaker) (*compiledPolicy, []*ratelimit.Limiter) {
	if len(raw) == 0 {
		return nil, nil
	}
	var cfg rawPolicyConfig
	if err := json.Unmarshal(raw, &cfg); err != nil {
		return nil, nil
	}
	if len(cfg.Rules) == 0 {
		return nil, nil
	}
	cp := &compiledPolicy{priority: priority}
	var limiters []*ratelimit.Limiter
	for idx, rr := range cfg.Rules {
		cr := compiledRule{match: compileMatch(rr.Match)}
		var newLim func(perSecond float64, burst int) *ratelimit.Limiter
		if mk != nil {
			idx, rr := idx, rr
			newLim = func(perSecond float64, burst int) *ratelimit.Limiter {
				return mk(idx, rr, perSecond, burst)
			}
		}
		act, lim := compileActionWith(rr.Action, secrets, newLim)
		if act.kind == actNoop {
			continue
		}
		cr.action = act
		if lim != nil {
			limiters = append(limiters, lim)
		}
		cp.rules = append(cp.rules, cr)
	}
	if len(cp.rules) == 0 {
		return nil, limiters
	}
	return cp, limiters
}

func compileMatch(m rawMatch) matchCond {
	mc := matchCond{pathPrefix: normalizePolicyPrefix(m.PathPrefix), header: m.Header}
	if len(m.Methods) > 0 {
		mc.methods = make(map[string]struct{}, len(m.Methods))
		for _, mm := range m.Methods {
			mc.methods[strings.ToUpper(strings.TrimSpace(mm))] = struct{}{}
		}
	}
	// Geo kumeleri derleme zamaninda hazirlanir; hot-path'te yalnizca harita aramasi.
	if len(m.Country) > 0 {
		mc.country = upperSet(m.Country)
		mc.needsGeo = true
	}
	if len(m.Continent) > 0 {
		mc.continent = upperSet(m.Continent)
		mc.needsGeo = true
	}
	if len(m.ISP) > 0 {
		mc.isp = upperSet(m.ISP)
		mc.needsGeo = true
	}
	if len(m.ASN) > 0 {
		mc.asn = make(map[uint]struct{}, len(m.ASN))
		for _, a := range m.ASN {
			mc.asn[a] = struct{}{}
		}
		mc.needsGeo = true
	}
	if m.TorExit != nil {
		mc.torExit = m.TorExit
		mc.needsIntel = true
	}
	if m.Hosting != nil {
		mc.hosting = m.Hosting
		mc.needsIntel = true
	}
	if len(m.Reputation) > 0 {
		mc.reputation = make(map[string]struct{}, len(m.Reputation))
		for _, r := range m.Reputation {
			r = strings.ToLower(strings.TrimSpace(r))
			if r != "" {
				mc.reputation[r] = struct{}{}
			}
		}
		mc.needsIntel = true
	}
	return mc
}

// upperSet, dizeleri buyuk harfe cevirip kumeye koyar (buyuk/kucuk duyarsiz eslesme).
func upperSet(in []string) map[string]struct{} {
	out := make(map[string]struct{}, len(in))
	for _, v := range in {
		v = strings.ToUpper(strings.TrimSpace(v))
		if v != "" {
			out[v] = struct{}{}
		}
	}
	return out
}

func compileAction(a rawAction, secrets map[string]string) (policyAction, *ratelimit.Limiter) {
	return compileActionWith(a, secrets, nil)
}

func compileActionWith(a rawAction, secrets map[string]string, newLim func(perSecond float64, burst int) *ratelimit.Limiter) (policyAction, *ratelimit.Limiter) {
	switch strings.ToLower(strings.TrimSpace(a.Type)) {
	case "deny":
		st := a.Status
		if st < 400 || st > 599 {
			st = http.StatusForbidden
		}
		return policyAction{kind: actDeny, status: st, message: a.Message}, nil
	case "redirect":
		if strings.TrimSpace(a.Location) == "" {
			return policyAction{kind: actNoop}, nil
		}
		return policyAction{kind: actRedirect, location: a.Location, status: a.Status}, nil
	case "require_mtls":
		return policyAction{kind: actRequireMTLS}, nil
	case "set_header":
		if len(a.Request.Set) == 0 && len(a.Request.Remove) == 0 &&
			len(a.Response.Set) == 0 && len(a.Response.Remove) == 0 {
			return policyAction{kind: actNoop}, nil
		}
		return policyAction{kind: actSetHeader, reqHeaders: a.Request, respHeaders: a.Response}, nil
	case "rate_limit":
		if a.Requests <= 0 {
			return policyAction{kind: actNoop}, nil
		}
		window := a.WindowSec
		if window <= 0 {
			window = 1
		}
		perSecond := float64(a.Requests) / float64(window)
		burst := a.Burst
		if burst <= 0 {
			burst = a.Requests
		}
		key := strings.TrimSpace(a.Key)
		if key == "" {
			key = "ip"
		}
		var lim *ratelimit.Limiter
		if newLim != nil {
			lim = newLim(perSecond, burst)
		} else {
			lim = ratelimit.New(perSecond, burst)
		}
		return policyAction{kind: actRateLimit, limiter: lim, rlKey: key}, lim
	case "verify_webhook":
		prov := strings.ToLower(strings.TrimSpace(a.Provider))
		if !isKnownWebhookProvider(prov) {
			return failClosedAction("verify_webhook", "bilinmeyen saglayici: "+prov)
		}
		sec, ok := resolveSecretRef(a.SecretRef, secrets)
		if !ok || sec == "" {
			// Secret cozulemedi (silinmis/yanlis ad/vault anahtari yok): kurali
			// dusurmek imzasiz istegi backend'e gecirirdi, bu yuzden 503 ile kapat.
			return failClosedAction("verify_webhook", "secret cozulemedi")
		}
		tol := time.Duration(a.ToleranceSec) * time.Second
		if tol <= 0 {
			tol = defaultWebhookTolerance
		}
		return policyAction{kind: actVerifyWebhook, provider: prov, secret: []byte(sec), tolerance: tol}, nil
	case "waf":
		base, ok := wafRuleset(a.Ruleset)
		if !ok {
			return failClosedAction("waf", "bilinmeyen kume: "+a.Ruleset)
		}
		custom, ok := compileWAFPatterns(a.Patterns)
		if !ok {
			return failClosedAction("waf", "gecersiz regex")
		}
		rules := make([]*regexp.Regexp, 0, len(base)+len(custom))
		rules = append(rules, base...)
		rules = append(rules, custom...)
		return policyAction{kind: actWAF, wafRules: rules}, nil
	default:
		return policyAction{kind: actNoop}, nil
	}
}

// failClosedAction, derlenemeyen GUVENLIK kurali (webhook/WAF) icin eslesen her
// istegi 503 ile reddeden kural uretir. Kurali sessizce dusurmek korumayi kaldirip
// istegi backend'e gecirirdi (fail-open); 503 "yapilandirma bozuk" anlamina gelir.
func failClosedAction(kind, reason string) (policyAction, *ratelimit.Limiter) {
	slog.Warn("guvenlik kurali derlenemedi, eslesen istekler 503 ile reddedilecek",
		"kural", kind, "neden", reason)
	return policyAction{
		kind:    actDeny,
		status:  http.StatusServiceUnavailable,
		message: "Güvenlik politikası yapılandırması geçersiz; istek reddedildi.",
	}, nil
}

// --- {{secret:ad}} cozumu (FAZ 1 Part 2 / S0) ---
//
// Cozum DERLEME zamaninda yapilir, istek zamaninda degil: istek basina secret
// cozmek DB turu demektir ve sicak yol kuralini bozar. Bedeli, secret degisince
// Router.Reload gerekmesidir (secret CRUD bunu tetikler).

var secretRefRe = regexp.MustCompile(`\{\{secret:([^}]+)\}\}`)

// policySecretRefs, ham config icinde gecen benzersiz secret adlarini doner.
// Reload bunlari tek tek cozup compilePolicy'ye harita olarak verir.
func policySecretRefs(raw []byte) []string {
	ms := secretRefRe.FindAllSubmatch(raw, -1)
	if len(ms) == 0 {
		return nil
	}
	seen := make(map[string]struct{}, len(ms))
	out := make([]string, 0, len(ms))
	for _, m := range ms {
		name := strings.TrimSpace(string(m[1]))
		if name == "" {
			continue
		}
		if _, ok := seen[name]; ok {
			continue
		}
		seen[name] = struct{}{}
		out = append(out, name)
	}
	return out
}

// resolveSecretRef, bir alan degerini cozer. Deger {{secret:ad}} bicimindeyse
// haritadan bakilir; degilse duz deger olarak oldugu gibi doner.
// Referans cozulemezse ok=false — cagiran kurali DUSURUR (fail-closed).
func resolveSecretRef(v string, secrets map[string]string) (string, bool) {
	v = strings.TrimSpace(v)
	if v == "" {
		return "", false
	}
	m := secretRefRe.FindStringSubmatch(v)
	if m == nil {
		return v, true // duz deger
	}
	name := strings.TrimSpace(m[1])
	val, ok := secrets[name]
	if !ok || val == "" {
		return "", false
	}
	return val, true
}

// WebhookMaxBody, verify_webhook icin bellege alinacak azami govde boyutu.
// Imza govdenin tamami uzerinden hesaplandigi icin govde once okunmak zorunda;
// sinir asilirsa istek 413 ile reddedilir (dogrulanamayan istek gecirilmez).
const WebhookMaxBody = 1 << 20 // 1 MB

// readWebhookBody, govdeyi sinirli okur ve r.Body'yi yeniden okunabilir hale
// getirir; boylece akis asagiya bozulmadan devam eder.
// tooLarge=true ise sinir asilmistir ve govde tuketilmemis sayilir.
func readWebhookBody(r *http.Request) (body []byte, tooLarge bool) {
	if r.Body == nil {
		return nil, false
	}
	buf, err := io.ReadAll(io.LimitReader(r.Body, WebhookMaxBody+1))
	if err != nil {
		return nil, false
	}
	if len(buf) > WebhookMaxBody {
		return nil, true
	}
	r.Body = io.NopCloser(bytes.NewReader(buf))
	r.ContentLength = int64(len(buf))
	return buf, false
}

// matches, istegin bu kurala uyup uymadigini soyler.
// geo, istek basina BIR KEZ cozulmus IP bilgisidir; Geo kosulu olmayan kurallarda
// kullanilmaz. Cozulememis (ok=false) bilgiyle hicbir Geo kosulu eslesmez.
func (m *matchCond) matches(r *http.Request, geo ipInfo, intel intelInfo) bool {
	if m.pathPrefix != "" && !policyPathMatches(r.URL.Path, m.pathPrefix) {
		return false
	}
	if len(m.methods) > 0 {
		if _, ok := m.methods[strings.ToUpper(r.Method)]; !ok {
			return false
		}
	}
	for k, v := range m.header {
		if r.Header.Get(k) != v {
			return false
		}
	}
	if !m.needsGeo {
		return m.matchesIntel(intel)
	}
	if !geo.ok {
		// DB yok veya IP cozulemedi: Geo kosulu eslesmez (deny tetiklenmez).
		return false
	}
	if len(m.country) > 0 {
		if _, ok := m.country[geo.country]; !ok {
			return false
		}
	}
	if len(m.continent) > 0 {
		if _, ok := m.continent[geo.continent]; !ok {
			return false
		}
	}
	if len(m.asn) > 0 {
		if _, ok := m.asn[geo.asn]; !ok {
			return false
		}
	}
	if len(m.isp) > 0 {
		if _, ok := m.isp[strings.ToUpper(geo.isp)]; !ok {
			return false
		}
	}
	return m.matchesIntel(intel)
}

// matchesIntel, liste tabanli kosullari (tor/hosting/reputation) degerlendirir.
// Saglayici tanimsizsa ilgili liste bostur ve kosul eslesmez — Geo ile ayni ilke.
func (m *matchCond) matchesIntel(intel intelInfo) bool {
	if !m.needsIntel {
		return true
	}
	if m.torExit != nil && *m.torExit != intel.torExit {
		return false
	}
	if m.hosting != nil && *m.hosting != intel.hosting {
		return false
	}
	if len(m.reputation) > 0 {
		hit := false
		for _, name := range intel.reputation {
			if _, ok := m.reputation[name]; ok {
				hit = true
				break
			}
		}
		if !hit {
			return false
		}
	}
	return true
}

// rateLimitKey, action'in anahtar tanimina gore bu istek icin sayac anahtarini uretir.
func rateLimitKey(rlKey string, r *http.Request, clientIP, tunnelID string) string {
	switch {
	case rlKey == "ip":
		return "ip:" + clientIP
	case rlKey == "tunnel":
		return "tunnel:" + tunnelID
	case strings.HasPrefix(rlKey, "header:"):
		name := strings.TrimPrefix(rlKey, "header:")
		return "hdr:" + name + ":" + r.Header.Get(name)
	default:
		return "ip:" + clientIP
	}
}

// evaluatePolicies, host'a bagli policy'leri sirayla degerlendirir. Ilk terminal
// action (deny/redirect/rate_limit/require_mtls-red) sonuclandirir; set_header'lar
// birikir. mtlsOK, istegin dogrulanmis mTLS ile geldigini gosterir.
func (rt *Router) evaluatePolicies(host string, r *http.Request, clientIP, tunnelID string, mtlsOK bool) policyOutcome {
	rt.mu.RLock()
	policies := rt.policies[normalizeHost(host)]
	rt.mu.RUnlock()
	return rt.evaluatePolicyList(policies, r, clientIP, tunnelID, mtlsOK)
}

// policiesForRoute, istegin gercek route'una uygulanacak policy listesi (O4).
// Host'a bagli policy'ler her zaman uygulanir; istek yol kuraliyla BASKA bir
// tunele gidiyorsa o tunele (tunnel_id ile) bagli policy'ler de eklenir.
// Ayni derlenmis policy iki kez eklenmez; sonuc priority artan siralidir.
func (rt *Router) policiesForRoute(host string, route store.HostRoute) []*compiledPolicy {
	rt.mu.RLock()
	defer rt.mu.RUnlock()
	base := rt.policies[normalizeHost(host)]
	if route.PathPrefix == "" {
		return base
	}
	extra := rt.tunnelPolicies[route.TunnelID]
	if len(extra) == 0 {
		return base
	}
	seen := make(map[string]bool, len(base))
	key := func(cp *compiledPolicy) string {
		if cp.id != "" {
			return cp.id
		}
		return fmt.Sprintf("%p", cp)
	}
	out := make([]*compiledPolicy, 0, len(base)+len(extra))
	for _, cp := range base {
		seen[key(cp)] = true
		out = append(out, cp)
	}
	for _, cp := range extra {
		if !seen[key(cp)] {
			out = append(out, cp)
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].priority < out[j].priority })
	return out
}

// evaluatePolicyList, verilen (priority sirali) policy listesini degerlendirir.
func (rt *Router) evaluatePolicyList(policies []*compiledPolicy, r *http.Request, clientIP, tunnelID string, mtlsOK bool) policyOutcome {
	if len(policies) == 0 {
		return policyOutcome{}
	}

	var out policyOutcome
	var reqSet, respSet map[string]string
	var reqRemove, respRemove []string
	// Webhook govdesi en fazla BIR KEZ okunur; birden cok kural eslesirse paylasilir.
	var whBody []byte
	var whRead, whTooLarge bool
	// Geo cozumu TEMBEL ve istek basina BIR KEZ: yalnizca Geo kosulu tasiyan bir
	// kurala gelindiginde yapilir, sonra tum kurallar ayni sonucu paylasir.
	var geo ipInfo
	var geoDone bool
	// Liste cozumu de TEMBEL ve istek basina BIR KEZ.
	var intel intelInfo
	var intelDone bool

	for _, cp := range policies {
		for i := range cp.rules {
			cr := &cp.rules[i]
			if cr.match.needsGeo && !geoDone {
				geo = rt.geo.lookup(clientIP)
				geoDone = true
			}
			if cr.match.needsIntel && !intelDone {
				intel = rt.intel.lookup(clientIP)
				intelDone = true
			}
			if !cr.match.matches(r, geo, intel) {
				continue
			}
			switch cr.action.kind {
			case actDeny:
				out.denyStatus = cr.action.status
				out.denyMessage = cr.action.message
				return out
			case actRedirect:
				st := cr.action.status
				switch st {
				case 301, 302, 307, 308:
				default:
					st = http.StatusFound
				}
				out.redirectLoc = cr.action.location
				out.redirectStatus = st
				return out
			case actRequireMTLS:
				if !mtlsOK {
					out.mtlsFailed = true
					return out
				}
			case actRateLimit:
				if !cr.action.limiter.Allow(rateLimitKey(cr.action.rlKey, r, clientIP, tunnelID)) {
					out.rateLimited = true
					out.retryAfter = cr.action.limiter.RetryAfter()
					return out
				}
			case actVerifyWebhook:
				if !whRead {
					whBody, whTooLarge = readWebhookBody(r)
					whRead = true
				}
				if whTooLarge {
					out.webhookFailed = true
					out.webhookStatus = http.StatusRequestEntityTooLarge
					out.webhookMessage = "Webhook govdesi dogrulama sinirini asiyor."
					return out
				}
				if !verifyWebhook(cr.action.provider, cr.action.secret, whBody, r.Header,
					cr.action.tolerance, time.Now()) {
					out.webhookFailed = true
					out.webhookStatus = http.StatusUnauthorized
					out.webhookMessage = "Webhook imzasi dogrulanamadi."
					return out
				}
			case actWAF:
				if rule, hit := wafScan(r, cr.action.wafRules); hit {
					out.wafBlocked = true
					out.wafRule = rule
					return out
				}
			case actSetHeader:
				reqSet, reqRemove = mergeHeaderRules(reqSet, reqRemove, cr.action.reqHeaders)
				respSet, respRemove = mergeHeaderRules(respSet, respRemove, cr.action.respHeaders)
			}
		}
	}

	if len(reqSet) > 0 || len(reqRemove) > 0 {
		out.reqHeaders = &headerRules{Set: reqSet, Remove: reqRemove}
	}
	if len(respSet) > 0 || len(respRemove) > 0 {
		out.respHeaders = &headerRules{Set: respSet, Remove: respRemove}
	}
	return out
}

// mergePolicyHeaders, policy set_header kurallarini mevcut trafik politikasina
// katar. tp nil ve policy header tasiyorsa yeni bir trafficPolicy olusur. Boylece
// downstream applyToRequest/applyToResponse degismeden calisir.
func mergePolicyHeaders(tp *trafficPolicy, out policyOutcome) *trafficPolicy {
	if out.reqHeaders == nil && out.respHeaders == nil {
		return tp
	}
	if tp == nil {
		tp = &trafficPolicy{}
	}
	if out.reqHeaders != nil {
		tp.RequestHeaders = combineHeaderRules(tp.RequestHeaders, *out.reqHeaders)
	}
	if out.respHeaders != nil {
		tp.ResponseHeaders = combineHeaderRules(tp.ResponseHeaders, *out.respHeaders)
	}
	return tp
}

// combineHeaderRules, iki headerRules'i birlestirir (policy, trafik uzerine yazar).
func combineHeaderRules(base, add headerRules) headerRules {
	out := headerRules{Set: map[string]string{}, Remove: nil}
	for k, v := range base.Set {
		out.Set[k] = v
	}
	for k, v := range add.Set {
		out.Set[k] = v
	}
	out.Remove = append(out.Remove, base.Remove...)
	out.Remove = append(out.Remove, add.Remove...)
	return out
}

func mergeHeaderRules(set map[string]string, remove []string, hr headerRules) (map[string]string, []string) {
	for k, v := range hr.Set {
		if set == nil {
			set = make(map[string]string)
		}
		set[k] = v
	}
	remove = append(remove, hr.Remove...)
	return set, remove
}

// policyPathMatches, policy path_prefix eslesmesi. Ham yolun YANINDA normalize
// edilmis (canonicalPath) yol da denenir: "//deny", "/./deny", "/x/../deny" gibi
// yollar backend'de cogu zaman "/deny"ye cozulur; yalnizca ham yolda HasPrefix
// bakmak deny/require_mtls kuralini atlatmaya izin verirdi. Iki bicimden biri
// eslesirse kural eslesir (genis = guvenli taraf).
func policyPathMatches(path, prefix string) bool {
	if strings.HasPrefix(path, prefix) {
		return true
	}
	return strings.HasPrefix(canonicalPath(path), prefix)
}

// canonicalPath, istek yolunu eslestirme icin guvenli bicime getirir:
// ters bolu -> '/', ardisik '/' tek, "." ve ".." segmentleri cozulur (kokun
// ustune cikilamaz), sondaki '/' korunur. Yalnizca KARAR icin kullanilir;
// backend'e giden yol degistirilmez.
func canonicalPath(p string) string {
	if p == "" {
		return "/"
	}
	p = strings.ReplaceAll(p, "\\", "/")
	trailing := strings.HasSuffix(p, "/")
	c := path.Clean("/" + p)
	if trailing && c != "/" {
		c += "/"
	}
	return c
}

// normalizePolicyPrefix, kuralin path_prefix degerini istek yoluyla ayni bicime
// getirir: bos birakilirsa bos (her yol), yoksa basa '/' eklenir, "//", "." ve ".."
// cozulur. Boylece "admin" veya "//deny" gibi yazimlar sessizce hic eslesmez
// olmaz. Sondaki '/' korunur.
func normalizePolicyPrefix(p string) string {
	p = strings.TrimSpace(p)
	if p == "" {
		return ""
	}
	return canonicalPath(p)
}
