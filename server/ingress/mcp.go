package ingress

// Tunel "MCP" modu (MCP M3/M4).
//
// Bir tunel MCP modundayken (tunnel_mcp.enabled):
//   - auth=oauth|apikey: TUM yollar (yalniz MCP yolu degil) Zorven kimlik
//     dogrulamasindan gecer; kimliksiz hicbir bayt upstream'e ulasmaz. Tunelin
//     klasik erisim politikasi (Basic/OAuth ziyaretci) bu tunelde uygulanmaz.
//   - Authorization / X-Zorven-* istemci basliklari upstream'e ASLA gitmez;
//     yerine ingress'in dogruladigi kimlik basliklari yazilir.
//   - Tam govde yakalama zorla kapalidir; akis (SSE) tamponsuz akar; baslik
//     zaman asimi uzundur; bos akislar idle zaman asimiyla temizlenir.
//   - POST govdeleri JSON-RPC olarak incelenir: arac izin/ret, hiz siniri, log,
//     tools/list suzme (bkz. mcp_resp.go).

import (
	"bytes"
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/tkodcumpeg4/zorven/server/mcpconf"
	"github.com/tkodcumpeg4/zorven/server/ratelimit"
	"github.com/tkodcumpeg4/zorven/server/store"
)

// MCP'ye ozgu red nedenleri (reqlog.Entry.RejectReason).
const (
	RejectMCPToolDenied   = "mcp_tool_denied"
	RejectMCPTokenInvalid = "mcp_token_invalid"
)

// MCPClaims, dogrulanmis bir kaynak token'inin ingress'e gereken kisimlari.
type MCPClaims struct {
	Subject    string // Zorven kullanici kimligi
	TenantID   string // token'in baglandigi (tunel sahibi) kiraci
	GrantID    string
	ClientID   string
	ClientName string
	Scope      string
}

// MCPUpstreamIdentity, upstream'e iletilecek imzali kimligin icerigi.
type MCPUpstreamIdentity struct {
	Subject    string
	Email      string
	TenantID   string
	TunnelID   string
	ClientID   string
	Scope      string
	AuthMethod string
	Host       string
}

// MCPVerifier, OAuth AS'in kaynak-tarafi islevleri (oauthas.Server uyarlayicisi).
type MCPVerifier interface {
	// VerifyResource, token'in imzasini, suresini, iptal durumunu ve
	// aud == resourceKey (sema-duyarsiz "host/yol") sartini dogrular.
	VerifyResource(r *http.Request, token, resourceKey string) (MCPClaims, error)
	// SignUpstream, upstream icin kisa omurlu (60 sn) imzali kimlik (JWS) uretir.
	SignUpstream(id MCPUpstreamIdentity) (string, error)
}

// MCPUserLookup, kullanicinin e-postasini ve tunel kiracisinin uyesi olup
// olmadigini doner. Hata fail-closed (erisim yok) sayilir.
type MCPUserLookup func(ctx context.Context, tenantID, userID string) (email string, member bool, err error)

// --- yonlendirme anlik goruntusu -------------------------------------------

type mcpKeyEntry struct {
	ID    string
	Label string
	Hash  string
	Exp   time.Time // sifir = suresiz
}

// mcpRoute, bir tunelin parse edilmis MCP ayari (Router.Reload'da BIR KEZ uretilir).
type mcpRoute struct {
	cfg      mcpconf.Config
	keys     []mcpKeyEntry
	tunnelID string
	tenantID string
}

func buildMCPRoute(rt store.HostRoute) *mcpRoute {
	if !rt.MCPEnabled {
		return nil
	}
	m := &mcpRoute{cfg: mcpconf.ParseLenient(rt.MCPConfig), tunnelID: rt.TunnelID, tenantID: rt.TenantID}
	var raw []struct {
		ID    string `json:"id"`
		Label string `json:"label"`
		Hash  string `json:"hash"`
		Exp   string `json:"exp"`
	}
	if len(rt.MCPKeys) > 0 && json.Unmarshal(rt.MCPKeys, &raw) == nil {
		for _, k := range raw {
			e := mcpKeyEntry{ID: k.ID, Label: k.Label, Hash: strings.ToLower(k.Hash)}
			if k.Exp != "" {
				if t, err := time.Parse(time.RFC3339Nano, k.Exp); err == nil {
					e.Exp = t
				} else if t, err := time.Parse("2006-01-02T15:04:05.999999999Z07:00", k.Exp); err == nil {
					e.Exp = t
				} else {
					// Okunamayan son kullanma: en kisitlayici davran (anahtari gecersiz say).
					e.Exp = time.Unix(1, 0)
				}
			}
			m.keys = append(m.keys, e)
		}
	}
	return m
}

// mcpFor, tunelin MCP ayarini doner (etkin degilse false).
func (r *Router) mcpFor(tunnelID string) (*mcpRoute, bool) {
	r.mu.RLock()
	m, ok := r.mcp[tunnelID]
	r.mu.RUnlock()
	return m, ok
}

// --- handler durumu ---------------------------------------------------------

type mcpState struct {
	mu      sync.Mutex
	streams map[string]int
	rl      map[string]*mcpRL
	users   map[string]mcpUserEntry
	fail    *ratelimit.Limiter
	meta    *ratelimit.Limiter
}

type mcpRL struct {
	rate int
	lim  *ratelimit.Limiter
}

type mcpUserEntry struct {
	email  string
	member bool
	exp    time.Time
}

func (h *Handler) mcpSt() *mcpState {
	h.mcpOnce.Do(func() {
		h.mcpState = &mcpState{
			streams: map[string]int{}, rl: map[string]*mcpRL{}, users: map[string]mcpUserEntry{},
			fail: ratelimit.New(30.0/60, 30), // IP basina dakikada 30 basarisiz kimlik denemesi
			meta: ratelimit.New(120.0/60, 60),
		}
	})
	return h.mcpState
}

// mcpAllowCall, tunel basina dakikalik cagri sinirini uygular.
func (h *Handler) mcpAllowCall(rt *mcpRoute) bool {
	if rt.cfg.RateLimitPerMin <= 0 {
		return true
	}
	st := h.mcpSt()
	st.mu.Lock()
	e := st.rl[rt.tunnelID]
	if e == nil || e.rate != rt.cfg.RateLimitPerMin {
		if e != nil {
			e.lim.Close()
		}
		burst := max(5, rt.cfg.RateLimitPerMin/6)
		burst = min(burst, rt.cfg.RateLimitPerMin)
		e = &mcpRL{rate: rt.cfg.RateLimitPerMin, lim: ratelimit.New(float64(rt.cfg.RateLimitPerMin)/60, burst)}
		st.rl[rt.tunnelID] = e
	}
	st.mu.Unlock()
	return e.lim.Allow(rt.tunnelID)
}

func (h *Handler) mcpAcquireStream(rt *mcpRoute) bool {
	st := h.mcpSt()
	st.mu.Lock()
	defer st.mu.Unlock()
	if st.streams[rt.tunnelID] >= rt.cfg.MaxStreams {
		return false
	}
	st.streams[rt.tunnelID]++
	return true
}

func (h *Handler) mcpReleaseStream(rt *mcpRoute) {
	st := h.mcpSt()
	st.mu.Lock()
	if st.streams[rt.tunnelID] > 0 {
		st.streams[rt.tunnelID]--
	}
	if st.streams[rt.tunnelID] == 0 {
		delete(st.streams, rt.tunnelID)
	}
	st.mu.Unlock()
}

// --- kimlik -----------------------------------------------------------------

// mcpIdent, dogrulanmis arayanin kimligi.
type mcpIdent struct {
	Method   string // oauth | apikey | none
	UserID   string
	Email    string
	ClientID string
	Label    string // apikey etiketi veya OAuth istemci adi
	Scope    string
}

func (i mcpIdent) principal() string {
	switch {
	case i.Method == mcpconf.AuthAPIKey:
		return "apikey:" + i.Label
	case i.Email != "":
		return i.Email
	}
	return i.UserID
}

func (i mcpIdent) client() string {
	if i.ClientID != "" {
		if i.Label != "" && i.Method == mcpconf.AuthOAuth {
			return i.Label
		}
		return i.ClientID
	}
	return i.Label
}

func bearerToken(r *http.Request) string {
	f := strings.Fields(r.Header.Get("Authorization"))
	if len(f) == 2 && strings.EqualFold(f[0], "bearer") {
		return f[1]
	}
	return ""
}

func (h *Handler) mcpScheme(r *http.Request) string {
	if r.TLS != nil || strings.EqualFold(r.Header.Get("X-Forwarded-Proto"), "https") {
		return "https"
	}
	return "http"
}

func (h *Handler) mcpResourceMetadataURL(r *http.Request, rt *mcpRoute) string {
	host := normalizeHost(r.Host)
	p := "/.well-known/oauth-protected-resource"
	if rt.cfg.Path != "/" {
		p += rt.cfg.Path
	}
	return h.mcpScheme(r) + "://" + host + p
}

// mcpChallenge, 401 yazar. Upstream cagrilmaz.
func (h *Handler) mcpChallenge(w http.ResponseWriter, r *http.Request, rt *mcpRoute, errCode string) {
	v := `Bearer realm="zorven-mcp"`
	if rt.cfg.Auth == mcpconf.AuthOAuth {
		v += `, resource_metadata="` + h.mcpResourceMetadataURL(r, rt) + `"`
	}
	if errCode != "" {
		v += `, error="` + errCode + `"`
	}
	w.Header().Set("WWW-Authenticate", v)
	w.Header().Set("Cache-Control", "no-store")
	writeError(w, r, http.StatusUnauthorized, "auth_required", "MCP uc noktasi kimlik dogrulama gerektirir.")
}

func hashKey(k string) string {
	s := sha256.Sum256([]byte(k))
	return hex.EncodeToString(s[:])
}

// mcpAuthenticate, Bearer'i dogrular. false ise yanit yazilmistir.
func (h *Handler) mcpAuthenticate(w http.ResponseWriter, r *http.Request, tun store.HostRoute, rt *mcpRoute, started time.Time) (mcpIdent, bool) {
	fail := func(status int, reason, errCode string) (mcpIdent, bool) {
		st := h.mcpSt()
		if !st.fail.Allow(clientIP(r)) {
			w.Header().Set("Retry-After", "30")
			writeError(w, r, http.StatusTooManyRequests, "rate_limited", "Cok fazla basarisiz kimlik denemesi.")
			h.logReject(tun, r, http.StatusTooManyRequests, RejectRateLimited, started)
			return mcpIdent{}, false
		}
		if status == http.StatusUnauthorized {
			h.mcpChallenge(w, r, rt, errCode)
		} else {
			writeError(w, r, status, "access_denied", "Bu MCP kaynagina erisim yetkiniz yok.")
		}
		h.logReject(tun, r, status, reason, started)
		return mcpIdent{}, false
	}

	tok := bearerToken(r)
	if tok == "" && rt.cfg.Auth == mcpconf.AuthAPIKey {
		tok = strings.TrimSpace(r.Header.Get("X-API-Key"))
	}
	if tok == "" {
		// Kimlik yok: ilk kesif istegi; basarisiz deneme SAYILMAZ (limiter'i
		// tuketmez), challenge doner.
		h.mcpChallenge(w, r, rt, "")
		h.logReject(tun, r, http.StatusUnauthorized, RejectAuthRequired, started)
		return mcpIdent{}, false
	}

	switch rt.cfg.Auth {
	case mcpconf.AuthAPIKey:
		if !strings.HasPrefix(tok, "zmk_") {
			return fail(http.StatusUnauthorized, RejectMCPTokenInvalid, "invalid_token")
		}
		sum := hashKey(tok)
		now := time.Now()
		var hit *mcpKeyEntry
		for i := range rt.keys {
			k := &rt.keys[i]
			// Tum anahtarlar sabit zamanli karsilastirilir (erken cikis yok).
			if subtle.ConstantTimeCompare([]byte(sum), []byte(k.Hash)) == 1 && (k.Exp.IsZero() || now.Before(k.Exp)) {
				hit = k
			}
		}
		if hit == nil {
			return fail(http.StatusUnauthorized, RejectMCPTokenInvalid, "invalid_token")
		}
		return mcpIdent{Method: mcpconf.AuthAPIKey, Label: sanitizeHeaderValue(hit.Label, 64)}, true

	case mcpconf.AuthOAuth:
		if h.MCPVerifier == nil || h.MCPIssuer == "" {
			writeError(w, r, http.StatusServiceUnavailable, "mcp_oauth_unavailable", "MCP OAuth bu sunucuda yapilandirilmamis.")
			h.logReject(tun, r, http.StatusServiceUnavailable, RejectAccessDenied, started)
			return mcpIdent{}, false
		}
		if strings.HasPrefix(tok, "zmk_") || strings.Count(tok, ".") != 2 {
			return fail(http.StatusUnauthorized, RejectMCPTokenInvalid, "invalid_token")
		}
		res := mcpconf.CanonicalResource(r.Host, rt.cfg.Path)
		c, err := h.MCPVerifier.VerifyResource(r, tok, res)
		if err != nil || c.Subject == "" || c.TenantID != rt.tenantID {
			// Baska kiraci/tunel icin verilmis veya gecersiz token.
			return fail(http.StatusUnauthorized, RejectMCPTokenInvalid, "invalid_token")
		}
		email, ok := h.mcpEligible(r.Context(), rt, c)
		if !ok {
			// Token gecerli ama kullanici artik izinli degil (uyelik/e-posta degisti).
			return fail(http.StatusForbidden, RejectAccessDenied, "")
		}
		label := c.ClientName
		return mcpIdent{Method: mcpconf.AuthOAuth, UserID: c.Subject, Email: email, ClientID: c.ClientID,
			Label: sanitizeHeaderValue(label, 64), Scope: c.Scope}, true
	}
	return fail(http.StatusUnauthorized, RejectMCPTokenInvalid, "invalid_token")
}

// mcpEligible, token sahibinin tunel politikasina gore HALA izinli olup olmadigi.
func (h *Handler) mcpEligible(ctx context.Context, rt *mcpRoute, c MCPClaims) (string, bool) {
	if h.MCPUsers == nil {
		return "", false
	}
	key := rt.tenantID + "|" + c.Subject
	st := h.mcpSt()
	now := time.Now()
	st.mu.Lock()
	e, ok := st.users[key]
	st.mu.Unlock()
	if !ok || now.After(e.exp) {
		email, member, err := h.MCPUsers(ctx, rt.tenantID, c.Subject)
		if err != nil {
			return "", false
		}
		e = mcpUserEntry{email: strings.ToLower(strings.TrimSpace(email)), member: member, exp: now.Add(30 * time.Second)}
		st.mu.Lock()
		if len(st.users) > 4096 {
			st.users = map[string]mcpUserEntry{}
		}
		st.users[key] = e
		st.mu.Unlock()
	}
	if rt.cfg.AllowOrgMembers && e.member {
		return e.email, true
	}
	if e.email != "" && emailListed(rt.cfg.AllowedEmails, e.email) {
		return e.email, true
	}
	return e.email, false
}

func emailListed(list []string, email string) bool { return mcpconf.EmailListed(list, email) }

// sanitizeHeaderValue, baslik degerinden kontrol karakterlerini atar ve keser.
func sanitizeHeaderValue(s string, max int) string {
	var b strings.Builder
	for _, r := range s {
		if r < 0x20 || r == 0x7f || r > 0x7e {
			continue
		}
		b.WriteRune(r)
		if b.Len() >= max {
			break
		}
	}
	return strings.TrimSpace(b.String())
}

// --- korunan kaynak metadata (RFC 9728) -------------------------------------

// serveMCPMetadata, tunel host'unda /.well-known/oauth-protected-resource[/yol]
// isteklerini kendisi yanitlar; bu yollar upstream'e ASLA gitmez (auth=oauth|apikey).
func (h *Handler) serveMCPMetadata(w http.ResponseWriter, r *http.Request, rt *mcpRoute) bool {
	if rt.cfg.Auth == mcpconf.AuthNone {
		return false
	}
	p := canonicalPath(r.URL.Path)
	base := "/.well-known/oauth-protected-resource"
	if p != base && !strings.HasPrefix(p, base+"/") {
		return false
	}
	if !h.mcpSt().meta.Allow(clientIP(r)) {
		w.Header().Set("Retry-After", "30")
		writeError(w, r, http.StatusTooManyRequests, "rate_limited", "Cok fazla istek.")
		return true
	}
	hd := w.Header()
	hd.Set("Access-Control-Allow-Origin", "*")
	hd.Set("Access-Control-Allow-Methods", "GET, OPTIONS")
	hd.Set("Access-Control-Allow-Headers", "Content-Type, MCP-Protocol-Version")
	if r.Method == http.MethodOptions {
		w.WriteHeader(http.StatusNoContent)
		return true
	}
	suffix := strings.TrimPrefix(p, base)
	want := rt.cfg.Path
	if rt.cfg.Path == "/" {
		want = ""
	}
	if r.Method != http.MethodGet || rt.cfg.Auth != mcpconf.AuthOAuth || h.MCPIssuer == "" || (suffix != "" && suffix != want) {
		writeError(w, r, http.StatusNotFound, "not_found", "Bu kaynak icin korunan kaynak metadata'si yok.")
		return true
	}
	host := normalizeHost(r.Host)
	hd.Set("Content-Type", "application/json")
	hd.Set("Cache-Control", "public, max-age=300")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"resource":                 h.mcpScheme(r) + "://" + host + rt.cfg.Path,
		"authorization_servers":    []string{strings.TrimRight(h.MCPIssuer, "/")},
		"scopes_supported":         []string{"mcp"},
		"bearer_methods_supported": []string{"header"},
		"resource_name":            host,
	})
	return true
}

// --- istek hazirligi --------------------------------------------------------

// mcpCall, yanit bekleyen bir JSON-RPC istegi.
type mcpCall struct {
	id      string
	method  string
	tool    string
	start   time.Time
	bytesIn int64
	done    bool
}

// mcpReq, bir MCP isteginin durumu (istek basina).
type mcpReq struct {
	h       *Handler
	rt      *mcpRoute
	tun     store.HostRoute
	ident   mcpIdent
	started time.Time

	legacy   bool
	stream   bool // GET akisi (sayac tutuldu)
	session  string
	calls    map[string]*mcpCall
	order    []*mcpCall
	listIDs  map[string]bool
	extras   [][]byte // sentetik hata yanitlari (batch)
	released bool
	bytesIn  int64
}

func (m *mcpReq) setSession(raw string) {
	if raw == "" || m.session != "" {
		return
	}
	s := sha256.Sum256([]byte(raw))
	m.session = hex.EncodeToString(s[:6])
}

func isJSONContentType(ct string) bool {
	ct = strings.ToLower(strings.TrimSpace(ct))
	if i := strings.IndexByte(ct, ';'); i >= 0 {
		ct = strings.TrimSpace(ct[:i])
	}
	return ct == "application/json" || strings.HasSuffix(ct, "+json")
}

// mcpBegin, kimlik dogrulamasi gecmis istek icin mcpReq kurar ve istemciden
// gelen kimlik/Zorven baslklarini SILER. (Authorization yalniz Zorven kimligi
// tuketildiyse silinir; auth=none'da kullanicinin kendi upstream token'i gecer.)
func (h *Handler) mcpBegin(r *http.Request, tun store.HostRoute, rt *mcpRoute, ident mcpIdent, started time.Time) *mcpReq {
	m := &mcpReq{h: h, rt: rt, tun: tun, ident: ident, started: started,
		calls: map[string]*mcpCall{}, listIDs: map[string]bool{}}
	m.setSession(r.Header.Get("Mcp-Session-Id"))
	stripZorvenHeaders(r.Header)
	if ident.Method != mcpconf.AuthNone {
		r.Header.Del("Authorization")
		if ident.Method == mcpconf.AuthAPIKey {
			r.Header.Del("X-Api-Key")
		}
	}
	return m
}

func stripZorvenHeaders(h http.Header) {
	for k := range h {
		if strings.HasPrefix(strings.ToLower(k), "x-zorven-") {
			delete(h, k)
		}
	}
}

// upstreamHeaders, upstream'e yazilacak kimlik basliklari. Her zaman gelen ayni
// adli basliklar silindikten SONRA, trafik politikasi kurallarindan sonra uygulanir.
func (m *mcpReq) applyUpstream(hd map[string][]string) {
	for k := range hd {
		lk := strings.ToLower(k)
		if strings.HasPrefix(lk, "x-zorven-") {
			delete(hd, k)
			continue
		}
		if m.ident.Method != mcpconf.AuthNone && lk == "authorization" {
			delete(hd, k)
		}
		if m.ident.Method == mcpconf.AuthAPIKey && lk == "x-api-key" {
			delete(hd, k)
		}
		if lk == "accept-encoding" {
			delete(hd, k)
		}
	}
	// Sikistirilmis yanit SSE'yi tamponlar ve suzmeyi imkansiz kilar.
	hd["Accept-Encoding"] = []string{"identity"}
	set := func(k, v string) {
		if v != "" {
			hd[k] = []string{v}
		}
	}
	set("X-Zorven-Tunnel-Id", m.rt.tunnelID)
	set("X-Zorven-Auth-Method", m.ident.Method)
	if m.ident.Method == mcpconf.AuthNone {
		return
	}
	user := m.ident.principal()
	set("X-Zorven-User", sanitizeHeaderValue(user, 254))
	set("X-Zorven-User-Id", m.ident.UserID)
	set("X-Zorven-Client-Id", m.ident.ClientID)
	set("X-Zorven-Scopes", sanitizeHeaderValue(m.ident.Scope, 200))
	if m.h.MCPVerifier != nil {
		sub := m.ident.UserID
		if m.ident.Method == mcpconf.AuthAPIKey {
			sub = "apikey:" + m.ident.Label
		}
		if jws, err := m.h.MCPVerifier.SignUpstream(MCPUpstreamIdentity{
			Subject: sub, Email: m.ident.Email, TenantID: m.rt.tenantID, TunnelID: m.rt.tunnelID,
			ClientID: m.ident.ClientID, Scope: m.ident.Scope, AuthMethod: m.ident.Method,
		}); err == nil {
			set("X-Zorven-Identity", jws)
		}
	}
}

// applyHeader, http.Header uzerinde applyUpstream (kume yonlendirmesi icin).
func (m *mcpReq) applyHeader(hd http.Header) {
	mp := map[string][]string(hd)
	m.applyUpstream(mp)
}

// prepare, istek govdesini inceler (POST/PUT/PATCH) ve akis sayacini tutar.
// handled=true ise yanit yazilmistir (red/hiz siniri/hata).
func (m *mcpReq) prepare(w http.ResponseWriter, r *http.Request) (handled bool) {
	h, cfg := m.h, m.rt.cfg
	canon := canonicalPath(r.URL.Path)
	isMCP := cfg.IsMCPPath(canon)
	m.legacy = cfg.IsLegacyPath(canon)

	switch r.Method {
	case http.MethodGet:
		wantsStream := strings.Contains(strings.ToLower(r.Header.Get("Accept")), "text/event-stream")
		if isMCP && (wantsStream || m.legacy) {
			if !h.mcpAcquireStream(m.rt) {
				w.Header().Set("Retry-After", "5")
				writeError(w, r, http.StatusTooManyRequests, protocolRateLimited, "Bu MCP tuneli icin acik akis siniri asildi.")
				h.logReject(m.tun, r, http.StatusTooManyRequests, RejectRateLimited, m.started)
				m.released = true
				return true
			}
			m.stream = true
		}
		return false
	case http.MethodPost, http.MethodPut, http.MethodPatch:
	default:
		return false
	}
	if r.Body == nil || r.Body == http.NoBody || r.ContentLength == 0 {
		return false
	}
	ct := r.Header.Get("Content-Type")
	strict := isMCP || cfg.HasToolPolicy()
	if !isJSONContentType(ct) {
		if isMCP && cfg.HasToolPolicy() {
			writeError(w, r, http.StatusUnsupportedMediaType, "unsupported_media_type", "MCP istekleri application/json olmali.")
			h.logReject(m.tun, r, http.StatusUnsupportedMediaType, RejectMCPToolDenied, m.started)
			return true
		}
		return false
	}
	if ce := strings.ToLower(strings.TrimSpace(r.Header.Get("Content-Encoding"))); ce != "" && ce != "identity" {
		if strict {
			writeError(w, r, http.StatusUnsupportedMediaType, "unsupported_media_type", "Sikistirilmis MCP istek govdesi desteklenmez.")
			h.logReject(m.tun, r, http.StatusUnsupportedMediaType, RejectMCPToolDenied, m.started)
			return true
		}
		return false
	}
	limit := cfg.MaxRequestBytes
	if r.ContentLength > limit {
		writeError(w, r, http.StatusRequestEntityTooLarge, "body_too_large", "MCP istek govdesi ayarlanan siniri asiyor.")
		h.logReject(m.tun, r, http.StatusRequestEntityTooLarge, RejectAccessDenied, m.started)
		return true
	}
	body, err := io.ReadAll(io.LimitReader(r.Body, limit+1))
	_ = r.Body.Close()
	if err != nil {
		writeError(w, r, http.StatusBadRequest, "bad_request", "Istek govdesi okunamadi.")
		return true
	}
	if int64(len(body)) > limit {
		writeError(w, r, http.StatusRequestEntityTooLarge, "body_too_large", "MCP istek govdesi ayarlanan siniri asiyor.")
		h.logReject(m.tun, r, http.StatusRequestEntityTooLarge, RejectAccessDenied, m.started)
		return true
	}
	setBody := func(b []byte) {
		r.Body = io.NopCloser(bytes.NewReader(b))
		r.ContentLength = int64(len(b))
		r.Header.Del("Content-Length")
		r.GetBody = nil
	}
	setBody(body)
	m.bytesIn = int64(len(body))

	msgs, batch, perr := parseRPC(body)
	if perr != nil {
		if cfg.HasToolPolicy() && isMCP {
			// Fail-closed: ayristirilamayan govde politika modunda iletilmez.
			m.writeJSON(w, http.StatusBadRequest, rpcErrorBody("", -32700, "Parse error", "mcp_unparseable"))
			h.logReject(m.tun, r, http.StatusBadRequest, RejectMCPToolDenied, m.started)
			return true
		}
		return false
	}

	keep := make([]bool, len(msgs))
	var immediate [][]byte // sentetik yanitlar
	anyDenied := false
	for i, msg := range msgs {
		keep[i] = true
		if msg.Response {
			continue
		}
		reason := ""
		if msg.Method == "tools/call" {
			switch {
			case cfg.HasToolPolicy() && (!msg.ToolOK || !cfg.ToolAllowed(msg.Tool)):
				reason = "mcp_tool_denied"
			case !h.mcpAllowCall(m.rt):
				reason = "rate_limited"
			}
		}
		if reason != "" {
			keep[i] = false
			anyDenied = true
			status := "denied"
			if reason == "rate_limited" {
				status = "rate_limited"
			}
			m.logCall(&mcpCall{id: msg.ID, method: msg.Method, tool: msg.Tool, start: time.Now(), bytesIn: int64(len(msg.Raw))},
				status, -32602, 0, 0)
			if msg.HasID {
				if reason == "rate_limited" {
					immediate = append(immediate, rpcErrorBody(msg.ID, -32000, "Hiz siniri asildi; biraz sonra tekrar deneyin", reason))
				} else {
					immediate = append(immediate, rpcDenyBody(msg.ID, msg.Tool, reason))
				}
			}
			continue
		}
		if msg.HasID {
			c := &mcpCall{id: msg.ID, method: msg.Method, tool: msg.Tool, start: time.Now(), bytesIn: int64(len(msg.Raw))}
			if msg.Method != "ping" && !strings.HasPrefix(msg.Method, "notifications/") {
				m.calls[msg.ID] = c
				m.order = append(m.order, c)
			}
			if msg.Method == "tools/list" && cfg.HasToolPolicy() {
				m.listIDs[msg.ID] = true
			}
		} else if msg.Method != "" && !strings.HasPrefix(msg.Method, "notifications/") && msg.Method != "ping" {
			m.logCall(&mcpCall{method: msg.Method, tool: msg.Tool, start: time.Now(), bytesIn: int64(len(msg.Raw))}, "ok", 0, 0, 0)
		}
	}
	if !anyDenied {
		return false
	}

	// Reddedilen oge(ler) var.
	remaining := 0
	for _, k := range keep {
		if k {
			remaining++
		}
	}
	denyReason := RejectMCPToolDenied
	if remaining == 0 {
		status := http.StatusOK
		var out []byte
		switch {
		case len(immediate) == 0:
			// Yalnizca reddedilmis bildirim(ler): sessizce dusur.
			w.WriteHeader(http.StatusAccepted)
			h.logReject(m.tun, r, http.StatusAccepted, denyReason, m.started)
			return true
		case batch:
			out = append([]byte{'['}, bytes.Join(immediate, []byte{','})...)
			out = append(out, ']')
		default:
			out = immediate[0]
		}
		if m.legacy {
			// Eski HTTP+SSE tasimasinda yanit ayri akistan gelir; dogrudan
			// HTTP 403 + JSON-RPC hata govdesi doneriz.
			status = http.StatusForbidden
		}
		m.writeJSON(w, status, out)
		h.logReject(m.tun, r, status, denyReason, m.started)
		return true
	}
	// Batch'in kalani iletilir; sentetik hatalar yanita eklenir.
	m.extras = immediate
	setBody(rewriteBatch(msgs, keep))
	m.bytesIn = r.ContentLength
	// Reddedilen ogeler icin bir reqlog satiri.
	h.logReject(m.tun, r, http.StatusOK, denyReason, m.started)
	return false
}

func (m *mcpReq) writeJSON(w http.ResponseWriter, status int, body []byte) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_, _ = w.Write(body)
}

// protocolRateLimited, protocol.CodeRateLimited ile ayni dizge (ingress.go'da kullaniliyor).
const protocolRateLimited = "rate_limited"

// logCall, tek bir cagri satiri yazar (log_level=none ise yazmaz).
func (m *mcpReq) logCall(c *mcpCall, status string, errCode int, durMS int64, bytesOut int64) {
	if m.rt.cfg.LogLevel == mcpconf.LogNone || m.h.MCPLog == nil {
		return
	}
	dur := durMS
	if dur == 0 && !c.start.IsZero() && status != "denied" && status != "rate_limited" {
		dur = time.Since(c.start).Milliseconds()
	}
	ec := errCode
	if status == "ok" || status == "denied" || status == "rate_limited" {
		ec = 0
	}
	m.h.MCPLog.Enqueue(store.MCPCall{
		TenantID: m.rt.tenantID, TunnelID: m.rt.tunnelID, TS: c.start.UTC(), Session: m.session,
		Client: m.ident.client(), Principal: m.ident.principal(), Method: c.method, Tool: c.tool,
		Status: status, ErrorCode: ec, DurationMS: int(min(dur, 1<<30)), BytesIn: c.bytesIn, BytesOut: bytesOut,
	})
}

// release, akis sayacini birakir (bir kez).
func (m *mcpReq) release() {
	if m.stream && !m.released {
		m.released = true
		m.h.mcpReleaseStream(m.rt)
	}
}

var _ = strconv.Itoa

// MCPResource, bir host+yol icin MCP etkin tunelin ozetini doner (OAuth AS
// resource cozumlemesi). Yol, tunelin MCP yoluyla birebir eslesmelidir (cagiran dener).
func (r *Router) MCPResource(host, path string) (tenantID, tunnelID string, cfg mcpconf.Config, ok bool) {
	tun, found := r.LookupPath(host, path)
	if !found || tun.Door != nil || !tun.Enabled || tun.Frozen {
		return "", "", mcpconf.Config{}, false
	}
	m, found := r.mcpFor(tun.TunnelID)
	if !found {
		return "", "", mcpconf.Config{}, false
	}
	return m.tenantID, m.tunnelID, m.cfg, true
}
