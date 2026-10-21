// Package oauthas, Zorven'in kucuk OAuth 2.1 yetkilendirme sunucusudur (AS).
//
// Amac: Claude "ozel baglayici" ve diger MCP istemcilerinin https://<alan>/mcp
// uc noktasina kullanici adina baglanabilmesi. Kapsam bilincli olarak dardir:
//
//   - RFC 8414 AS metadata, RFC 9728 korunan kaynak metadata, JWKS
//   - RFC 7591 dinamik istemci kaydi (yalniz kamusal istemci + PKCE)
//   - /oauth/authorize: dogrular, paneldeki onay sayfasina yonlendirir
//   - /oauth/token: authorization_code (yalniz PKCE S256) + refresh_token (donen,
//     yeniden kullanimda tum aile iptal)
//   - RFC 8707 resource: token'in aud'u kaynaga baglanir
//   - RFC 7009 iptal; grant listeleme/iptal (panel "Bagli uygulamalar")
//
// Access token'lar kisa omurlu (1 sa) Ed25519 imzali JWT'dir; her istekte grant'in
// iptal edilip edilmedigi kisa onbellekle denetlenir. Refresh token'lar opaktir,
// veritabaninda yalnizca SHA-256 ozeti saklanir.
//
// Kimlik: Zorven hesabi girisi panelde (Better Auth) yapilir. Cerez panel
// host'una bagli oldugundan /oauth/authorize kullaniciyi panelin onay sayfasina
// (ConsentBase) yonlendirir; sayfa /oauth/consent/info ve /decision uclarini
// oturumla cagirir. Bu paket api paketini ICE AKTARMAZ (api bu paketi kullanir).
package oauthas

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/tkodcumpeg4/zorven/server/ratelimit"
)

// Sabitler.
const (
	// ResourcePath, korunan kaynagin (MCP) yolu.
	ResourcePath = "/mcp"

	codeTTL         = 60 * time.Second
	refreshTTL      = 30 * 24 * time.Hour
	grantCacheTTL   = 30 * time.Second
	maxClients      = 20000
	maxRedirectURIs = 10
	maxFormBytes    = 16 << 10
)

// Session, panel oturumundaki kullanici.
type Session struct {
	UserID string
	Email  string
	Name   string
}

// Org, kullanicinin uye oldugu organizasyon.
type Org struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	Role string `json:"role"`
}

// Config, AS'in bagimliliklari.
type Config struct {
	// Store ZORUNLU.
	Store Store
	// SigningSeed, Ed25519 tohumu (32 bayt; ZORVEN gizli anahtarindan turetilmis). ZORUNLU.
	SigningSeed []byte
	// Issuer, ornegin "https://zorven.app". Bos ise istekten (sema+host) turetilir (gelistirme).
	Issuer string
	// ConsentBase, onay sayfasinin origin'i ("https://panel.zorven.app"). Bos ise
	// ayni origin'e goreli yonlendirilir.
	ConsentBase string
	// Session, panel oturumunu cozer (onay/grant uclari). nil ise bu uclar 401 verir.
	Session func(*http.Request) (Session, bool)
	// Orgs, kullanicinin organizasyonlari ve rolleri.
	Orgs func(ctx context.Context, userID string) ([]Org, error)
	// OriginTrusted, durum degistiren panel uclarinda (CSRF) Origin denetimi. nil ise reddedilir.
	OriginTrusted func(*http.Request) bool
	// ClientIP, hiz siniri/denetim icin.
	ClientIP func(*http.Request) string
	// Audit, denetim kaydi (nil olabilir).
	Audit func(tenantID, actor, ip, action, target, detail string)
	// RegisterLimiter / TokenLimiter, IP basina hiz sinirlari (nil ise sinirsiz).
	RegisterLimiter *ratelimit.Limiter
	TokenLimiter    *ratelimit.Limiter
	// Now, test icin saat.
	Now func() time.Time
}

// Server, AS isleyicisi.
type Server struct {
	cfg  Config
	priv ed25519.PrivateKey
	pub  ed25519.PublicKey

	mu    sync.Mutex
	cache map[string]cachedGrant
}

type cachedGrant struct {
	g  Grant
	at time.Time
}

// New, AS'i kurar.
func New(cfg Config) (*Server, error) {
	if cfg.Store == nil {
		return nil, errors.New("oauthas: Store gerekli")
	}
	if len(cfg.SigningSeed) != ed25519.SeedSize {
		return nil, errors.New("oauthas: imza tohumu 32 bayt olmali")
	}
	priv := ed25519.NewKeyFromSeed(cfg.SigningSeed)
	if cfg.Now == nil {
		cfg.Now = time.Now
	}
	return &Server{cfg: cfg, priv: priv, pub: priv.Public().(ed25519.PublicKey), cache: map[string]cachedGrant{}}, nil
}

// Match, yolun bu AS tarafindan karsilanip karsilanmadigini soyler. /oauth/consent
// (HTML sayfa) bilerek DAHIL DEGILDIR; panel SPA'si sunar.
func Match(path string) bool {
	switch path {
	case "/.well-known/oauth-authorization-server", "/.well-known/openid-configuration",
		"/.well-known/oauth-protected-resource", "/.well-known/oauth-protected-resource" + ResourcePath,
		"/.well-known/jwks.json",
		"/oauth/register", "/oauth/authorize", "/oauth/token", "/oauth/revoke",
		"/oauth/consent/info", "/oauth/consent/decision", "/oauth/grants":
		return true
	}
	return strings.HasPrefix(path, "/oauth/grants/")
}

func (s *Server) ip(r *http.Request) string {
	if s.cfg.ClientIP != nil {
		return s.cfg.ClientIP(r)
	}
	return r.RemoteAddr
}

// Issuer, bu istek icin yayinci URL'si.
func (s *Server) Issuer(r *http.Request) string {
	if s.cfg.Issuer != "" {
		return strings.TrimRight(s.cfg.Issuer, "/")
	}
	scheme := "http"
	if r.TLS != nil || strings.EqualFold(r.Header.Get("X-Forwarded-Proto"), "https") {
		scheme = "https"
	}
	return scheme + "://" + r.Host
}

// Resource, MCP kaynaginin kanonik URL'si (token aud'u).
func (s *Server) Resource(r *http.Request) string { return s.Issuer(r) + ResourcePath }

// ResourceMetadataURL, 401 WWW-Authenticate icin PRM adresi.
func (s *Server) ResourceMetadataURL(r *http.Request) string {
	return s.Issuer(r) + "/.well-known/oauth-protected-resource"
}

// ServeHTTP, Match(path) olan istekleri karsilar.
func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	p := r.URL.Path
	switch {
	case p == "/.well-known/oauth-authorization-server" || p == "/.well-known/openid-configuration":
		s.metadata(w, r)
	case p == "/.well-known/oauth-protected-resource" || p == "/.well-known/oauth-protected-resource"+ResourcePath:
		s.protectedResource(w, r)
	case p == "/.well-known/jwks.json":
		s.jwks(w, r)
	case p == "/oauth/register":
		s.register(w, r)
	case p == "/oauth/authorize":
		s.authorize(w, r)
	case p == "/oauth/token":
		s.token(w, r)
	case p == "/oauth/revoke":
		s.revoke(w, r)
	case p == "/oauth/consent/info":
		s.consentInfo(w, r)
	case p == "/oauth/consent/decision":
		s.consentDecision(w, r)
	case p == "/oauth/grants" || strings.HasPrefix(p, "/oauth/grants/"):
		s.grants(w, r)
	default:
		http.NotFound(w, r)
	}
}

// --- yardimcilar ------------------------------------------------------------

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func oauthErr(w http.ResponseWriter, status int, code, desc string) {
	writeJSON(w, status, map[string]string{"error": code, "error_description": desc})
}

// publicCORS, kimliksiz genel uclar icin (metadata, register, token, revoke):
// tarayici tabanli MCP istemcileri (Inspector) cagirabilsin. Cerez kullanilmaz.
func publicCORS(w http.ResponseWriter, r *http.Request) bool {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.Header().Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
	w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization, MCP-Protocol-Version")
	if r.Method == http.MethodOptions {
		w.WriteHeader(http.StatusNoContent)
		return true
	}
	return false
}

func randToken(n int) string {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		panic("rand: " + err.Error())
	}
	return b64.EncodeToString(b)
}

func randID(prefix string) string {
	b := make([]byte, 12)
	if _, err := rand.Read(b); err != nil {
		panic("rand: " + err.Error())
	}
	return prefix + hex.EncodeToString(b)
}

func hashToken(t string) string {
	sum := sha256.Sum256([]byte(t))
	return hex.EncodeToString(sum[:])
}

func (s *Server) audit(tenant, actor, r_ip, action, target, detail string) {
	if s.cfg.Audit != nil {
		s.cfg.Audit(tenant, actor, r_ip, action, target, detail)
	}
}

// --- metadata ----------------------------------------------------------------

func (s *Server) metadata(w http.ResponseWriter, r *http.Request) {
	if publicCORS(w, r) {
		return
	}
	iss := s.Issuer(r)
	w.Header().Set("Cache-Control", "public, max-age=300")
	writeJSONCached(w, map[string]any{
		"issuer":                                         iss,
		"authorization_endpoint":                         iss + "/oauth/authorize",
		"token_endpoint":                                 iss + "/oauth/token",
		"registration_endpoint":                          iss + "/oauth/register",
		"revocation_endpoint":                            iss + "/oauth/revoke",
		"jwks_uri":                                       iss + "/.well-known/jwks.json",
		"response_types_supported":                       []string{"code"},
		"response_modes_supported":                       []string{"query"},
		"grant_types_supported":                          []string{"authorization_code", "refresh_token"},
		"code_challenge_methods_supported":               []string{"S256"},
		"token_endpoint_auth_methods_supported":          []string{"none"},
		"scopes_supported":                               SupportedScopes(),
		"resource_indicators_supported":                  true,
		"authorization_response_iss_parameter_supported": true,
		"service_documentation":                          "https://github.com/tkodcumpeg4/zorven/blob/main/docs/mcp.md",
	})
}

func writeJSONCached(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(v)
}

func (s *Server) protectedResource(w http.ResponseWriter, r *http.Request) {
	if publicCORS(w, r) {
		return
	}
	w.Header().Set("Cache-Control", "public, max-age=300")
	writeJSONCached(w, map[string]any{
		"resource":                 s.Resource(r),
		"authorization_servers":    []string{s.Issuer(r)},
		"scopes_supported":         SupportedScopes(),
		"bearer_methods_supported": []string{"header"},
		"resource_name":            "Zorven MCP",
	})
}

func (s *Server) jwks(w http.ResponseWriter, r *http.Request) {
	if publicCORS(w, r) {
		return
	}
	w.Header().Set("Cache-Control", "public, max-age=300")
	writeJSONCached(w, map[string]any{"keys": []map[string]string{{
		"kty": "OKP", "crv": "Ed25519", "alg": "EdDSA", "use": "sig",
		"kid": keyID(s.pub), "x": b64.EncodeToString(s.pub),
	}}})
}

// --- dinamik istemci kaydi (RFC 7591) ----------------------------------------

// ValidateRedirectURI, kayit icin yonlendirme URI'sini denetler: https veya
// dongu (loopback) http; parca (#) ve kullanici bilgisi yok.
func ValidateRedirectURI(raw string) error {
	if len(raw) > 2000 {
		return errors.New("redirect_uri cok uzun")
	}
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" || u.Opaque != "" {
		return errors.New("redirect_uri mutlak bir URL olmali")
	}
	if u.Fragment != "" || strings.Contains(raw, "#") {
		return errors.New("redirect_uri parca (#) iceremez")
	}
	if u.User != nil {
		return errors.New("redirect_uri kullanici bilgisi iceremez")
	}
	switch u.Scheme {
	case "https":
		return nil
	case "http":
		if isLoopbackHost(u.Hostname()) {
			return nil
		}
		return errors.New("http yalnizca dongu adreslerinde (localhost, 127.0.0.1, [::1]) kabul edilir")
	}
	return errors.New("redirect_uri semasi https (veya dongu http) olmali")
}

func isLoopbackHost(h string) bool {
	return h == "localhost" || h == "127.0.0.1" || h == "::1"
}

func (s *Server) register(w http.ResponseWriter, r *http.Request) {
	if publicCORS(w, r) {
		return
	}
	if r.Method != http.MethodPost {
		oauthErr(w, http.StatusMethodNotAllowed, "invalid_request", "POST gerekli")
		return
	}
	if s.cfg.RegisterLimiter != nil && !s.cfg.RegisterLimiter.Allow(s.ip(r)) {
		w.Header().Set("Retry-After", "60")
		oauthErr(w, http.StatusTooManyRequests, "temporarily_unavailable", "cok fazla kayit denemesi")
		return
	}
	var req struct {
		RedirectURIs            []string `json:"redirect_uris"`
		ClientName              string   `json:"client_name"`
		GrantTypes              []string `json:"grant_types"`
		ResponseTypes           []string `json:"response_types"`
		TokenEndpointAuthMethod string   `json:"token_endpoint_auth_method"`
	}
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxFormBytes))
	if err := dec.Decode(&req); err != nil {
		oauthErr(w, http.StatusBadRequest, "invalid_client_metadata", "gecersiz JSON")
		return
	}
	if len(req.RedirectURIs) == 0 || len(req.RedirectURIs) > maxRedirectURIs {
		oauthErr(w, http.StatusBadRequest, "invalid_redirect_uri", fmt.Sprintf("1-%d adet redirect_uri gerekli", maxRedirectURIs))
		return
	}
	seen := map[string]bool{}
	var uris []string
	for _, u := range req.RedirectURIs {
		if err := ValidateRedirectURI(u); err != nil {
			oauthErr(w, http.StatusBadRequest, "invalid_redirect_uri", err.Error())
			return
		}
		if !seen[u] {
			seen[u] = true
			uris = append(uris, u)
		}
	}
	method := req.TokenEndpointAuthMethod
	if method == "" {
		method = "none"
	}
	if method != "none" {
		oauthErr(w, http.StatusBadRequest, "invalid_client_metadata", "yalnizca token_endpoint_auth_method=none (kamusal istemci + PKCE) desteklenir")
		return
	}
	grantTypes := req.GrantTypes
	if len(grantTypes) == 0 {
		grantTypes = []string{"authorization_code", "refresh_token"}
	}
	for _, g := range grantTypes {
		if g != "authorization_code" && g != "refresh_token" {
			oauthErr(w, http.StatusBadRequest, "invalid_client_metadata", "desteklenmeyen grant_type: "+g)
			return
		}
	}
	for _, rt := range req.ResponseTypes {
		if rt != "code" {
			oauthErr(w, http.StatusBadRequest, "invalid_client_metadata", "yalnizca response_type=code desteklenir")
			return
		}
	}
	name := cleanName(req.ClientName)
	if name == "" {
		name = "MCP istemcisi"
	}
	n, err := s.cfg.Store.CountClients(r.Context())
	if err != nil {
		oauthErr(w, http.StatusInternalServerError, "server_error", "kayit yapilamadi")
		return
	}
	if n >= maxClients {
		oauthErr(w, http.StatusServiceUnavailable, "temporarily_unavailable", "istemci kayit tavani doldu")
		return
	}
	c := Client{ID: randID("zoc_"), Name: name, RedirectURIs: uris, GrantTypes: grantTypes,
		CreatedIP: s.ip(r), CreatedAt: s.cfg.Now().UTC()}
	if err := s.cfg.Store.CreateClient(r.Context(), c); err != nil {
		oauthErr(w, http.StatusInternalServerError, "server_error", "kayit yapilamadi")
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{
		"client_id":                  c.ID,
		"client_id_issued_at":        c.CreatedAt.Unix(),
		"client_name":                c.Name,
		"redirect_uris":              c.RedirectURIs,
		"grant_types":                c.GrantTypes,
		"response_types":             []string{"code"},
		"token_endpoint_auth_method": "none",
	})
}

// cleanName, istemci adini gosterim icin temizler (kontrol karakteri yok, 80 rune).
func cleanName(s string) string {
	var b strings.Builder
	n := 0
	for _, r := range strings.TrimSpace(s) {
		if r < 0x20 || r == 0x7f || r == '<' || r == '>' {
			continue
		}
		b.WriteRune(r)
		if n++; n >= 80 {
			break
		}
	}
	return strings.TrimSpace(b.String())
}

// --- yetkilendirme istegi ----------------------------------------------------

type authzReq struct {
	Client      Client
	RedirectURI string
	State       string
	Scopes      []string
	Resource    string
	Challenge   string
}

type authzErr struct {
	code, desc string
	// redirectable false ise (istemci/redirect_uri guvenilmez) kullanici yonlendirilmez.
	redirectable bool
}

// matchRedirect, kayitli URI'lerle tam eslesme; dongu http URI'lerinde port serbest (RFC 8252).
func matchRedirect(registered []string, got string) bool {
	for _, reg := range registered {
		if reg == got {
			return true
		}
	}
	gu, err := url.Parse(got)
	if err != nil || gu.Scheme != "http" || !isLoopbackHost(gu.Hostname()) {
		return false
	}
	for _, reg := range registered {
		ru, err := url.Parse(reg)
		if err != nil || ru.Scheme != "http" || ru.Hostname() != gu.Hostname() {
			continue
		}
		if ru.Path == gu.Path && ru.RawQuery == gu.RawQuery {
			return true
		}
	}
	return false
}

func (s *Server) parseAuthz(r *http.Request, v url.Values) (*authzReq, *authzErr) {
	clientID := v.Get("client_id")
	if clientID == "" {
		return nil, &authzErr{"invalid_request", "client_id gerekli", false}
	}
	c, err := s.cfg.Store.GetClient(r.Context(), clientID)
	if err != nil {
		return nil, &authzErr{"invalid_request", "bilinmeyen client_id", false}
	}
	redirect := v.Get("redirect_uri")
	if redirect == "" && len(c.RedirectURIs) == 1 {
		redirect = c.RedirectURIs[0]
	}
	if redirect == "" || !matchRedirect(c.RedirectURIs, redirect) {
		return nil, &authzErr{"invalid_request", "redirect_uri kayitli degil", false}
	}
	req := &authzReq{Client: c, RedirectURI: redirect, State: v.Get("state")}
	if v.Get("response_type") != "code" {
		return req, &authzErr{"unsupported_response_type", "yalnizca response_type=code desteklenir", true}
	}
	if v.Get("code_challenge") == "" || v.Get("code_challenge_method") != "S256" {
		return req, &authzErr{"invalid_request", "PKCE zorunlu: code_challenge ve code_challenge_method=S256", true}
	}
	if len(v.Get("code_challenge")) < 43 || len(v.Get("code_challenge")) > 128 {
		return req, &authzErr{"invalid_request", "code_challenge gecersiz", true}
	}
	req.Challenge = v.Get("code_challenge")
	want := s.Resource(r)
	res := v.Get("resource")
	if res == "" {
		res = want // RFC 8707: istemci vermediyse tek kaynagimiza baglariz
	}
	if strings.TrimRight(res, "/") != want {
		return req, &authzErr{"invalid_target", "resource bu sunucunun MCP kaynagi olmali: " + want, true}
	}
	req.Resource = want
	scopes, unknown := ParseScopes(v.Get("scope"))
	if unknown != "" {
		return req, &authzErr{"invalid_scope", "bilinmeyen kapsam: " + unknown, true}
	}
	if len(scopes) == 0 {
		scopes = []string{ScopeMCPRead}
	}
	req.Scopes = scopes
	return req, nil
}

func (s *Server) redirectWith(w http.ResponseWriter, r *http.Request, redirect string, params map[string]string) {
	u, err := url.Parse(redirect)
	if err != nil {
		http.Error(w, "gecersiz redirect_uri", http.StatusBadRequest)
		return
	}
	q := u.Query()
	for k, v := range params {
		if v != "" {
			q.Set(k, v)
		}
	}
	q.Set("iss", s.Issuer(r))
	u.RawQuery = q.Encode()
	w.Header().Set("Cache-Control", "no-store")
	http.Redirect(w, r, u.String(), http.StatusFound)
}

func (s *Server) authorize(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "GET gerekli", http.StatusMethodNotAllowed)
		return
	}
	v := r.URL.Query()
	req, aerr := s.parseAuthz(r, v)
	if aerr != nil {
		if !aerr.redirectable {
			w.Header().Set("Content-Type", "text/plain; charset=utf-8")
			w.Header().Set("Cache-Control", "no-store")
			w.WriteHeader(http.StatusBadRequest)
			_, _ = fmt.Fprintf(w, "Yetkilendirme istegi gecersiz: %s", aerr.desc)
			return
		}
		s.redirectWith(w, r, req.RedirectURI, map[string]string{"error": aerr.code, "error_description": aerr.desc, "state": req.State})
		return
	}
	// Dogrulanmis istegi paneldeki onay sayfasina tasi (durumsuz: parametreler
	// onay uclarinda yeniden dogrulanir).
	q := url.Values{}
	q.Set("client_id", req.Client.ID)
	q.Set("redirect_uri", req.RedirectURI)
	q.Set("response_type", "code")
	q.Set("code_challenge", req.Challenge)
	q.Set("code_challenge_method", "S256")
	q.Set("scope", strings.Join(req.Scopes, " "))
	q.Set("resource", req.Resource)
	if req.State != "" {
		q.Set("state", req.State)
	}
	w.Header().Set("Cache-Control", "no-store")
	http.Redirect(w, r, strings.TrimRight(s.cfg.ConsentBase, "/")+"/oauth/consent?"+q.Encode(), http.StatusFound)
}

// --- token uc noktasi --------------------------------------------------------

func (s *Server) token(w http.ResponseWriter, r *http.Request) {
	if publicCORS(w, r) {
		return
	}
	if r.Method != http.MethodPost {
		oauthErr(w, http.StatusMethodNotAllowed, "invalid_request", "POST gerekli")
		return
	}
	if s.cfg.TokenLimiter != nil && !s.cfg.TokenLimiter.Allow(s.ip(r)) {
		w.Header().Set("Retry-After", "30")
		oauthErr(w, http.StatusTooManyRequests, "temporarily_unavailable", "cok fazla istek")
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxFormBytes)
	if err := r.ParseForm(); err != nil {
		oauthErr(w, http.StatusBadRequest, "invalid_request", "gecersiz form")
		return
	}
	switch r.PostForm.Get("grant_type") {
	case "authorization_code":
		s.tokenCode(w, r)
	case "refresh_token":
		s.tokenRefresh(w, r)
	default:
		oauthErr(w, http.StatusBadRequest, "unsupported_grant_type", "authorization_code veya refresh_token")
	}
}

func validVerifier(v string) bool {
	if len(v) < 43 || len(v) > 128 {
		return false
	}
	for _, c := range v {
		if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '-' || c == '.' || c == '_' || c == '~') {
			return false
		}
	}
	return true
}

func (s *Server) tokenCode(w http.ResponseWriter, r *http.Request) {
	f := r.PostForm
	ctx := r.Context()
	now := s.cfg.Now()
	if f.Get("code") == "" || f.Get("client_id") == "" {
		oauthErr(w, http.StatusBadRequest, "invalid_request", "code ve client_id gerekli")
		return
	}
	verifier := f.Get("code_verifier")
	if !validVerifier(verifier) {
		oauthErr(w, http.StatusBadRequest, "invalid_grant", "code_verifier gecersiz")
		return
	}
	code, err := s.cfg.Store.ConsumeCode(ctx, hashToken(f.Get("code")), now)
	if err != nil {
		oauthErr(w, http.StatusBadRequest, "invalid_grant", "kod gecersiz, suresi dolmus veya kullanilmis")
		return
	}
	if subtle.ConstantTimeCompare([]byte(code.ClientID), []byte(f.Get("client_id"))) != 1 {
		oauthErr(w, http.StatusBadRequest, "invalid_grant", "kod bu istemciye ait degil")
		return
	}
	if f.Get("redirect_uri") != code.RedirectURI {
		oauthErr(w, http.StatusBadRequest, "invalid_grant", "redirect_uri eslesmiyor")
		return
	}
	sum := sha256.Sum256([]byte(verifier))
	if subtle.ConstantTimeCompare([]byte(b64.EncodeToString(sum[:])), []byte(code.CodeChallenge)) != 1 {
		oauthErr(w, http.StatusBadRequest, "invalid_grant", "PKCE dogrulamasi basarisiz")
		return
	}
	if res := f.Get("resource"); res != "" && strings.TrimRight(res, "/") != code.Resource {
		oauthErr(w, http.StatusBadRequest, "invalid_target", "resource yetkilendirme istegiyle eslesmiyor")
		return
	}
	g := Grant{ID: randID("zog_"), UserID: code.UserID, TenantID: code.TenantID, ClientID: code.ClientID,
		Resource: code.Resource, Scope: code.Scope, CreatedAt: now.UTC()}
	if err := s.cfg.Store.CreateGrant(ctx, g); err != nil {
		oauthErr(w, http.StatusInternalServerError, "server_error", "grant olusturulamadi")
		return
	}
	s.audit(g.TenantID, "oauth:"+g.UserID, s.ip(r), "oauth.grant", g.ID, "client="+g.ClientID+" scope="+g.Scope)
	s.issue(w, r, g, g.Scope)
}

func (s *Server) tokenRefresh(w http.ResponseWriter, r *http.Request) {
	f := r.PostForm
	ctx := r.Context()
	now := s.cfg.Now()
	rt := f.Get("refresh_token")
	if rt == "" || f.Get("client_id") == "" {
		oauthErr(w, http.StatusBadRequest, "invalid_request", "refresh_token ve client_id gerekli")
		return
	}
	gid, err := s.cfg.Store.RotateRefresh(ctx, hashToken(rt), now)
	if errors.Is(err, ErrUsed) {
		// Yeniden kullanim: calinmis token belirtisi. Tum aile (grant) iptal.
		_ = s.cfg.Store.RevokeGrant(ctx, gid, now)
		s.forget(gid)
		if g, gerr := s.cfg.Store.GetGrant(ctx, gid); gerr == nil {
			s.audit(g.TenantID, "oauth:"+g.UserID, s.ip(r), "oauth.reuse_revoke", gid, "client="+g.ClientID)
		}
		oauthErr(w, http.StatusBadRequest, "invalid_grant", "refresh token yeniden kullanildi; yetki iptal edildi")
		return
	}
	if err != nil {
		oauthErr(w, http.StatusBadRequest, "invalid_grant", "refresh token gecersiz veya suresi dolmus")
		return
	}
	g, err := s.cfg.Store.GetGrant(ctx, gid)
	if err != nil || !g.Active() || g.ClientID != f.Get("client_id") {
		oauthErr(w, http.StatusBadRequest, "invalid_grant", "yetki iptal edilmis veya istemci eslesmiyor")
		return
	}
	if res := f.Get("resource"); res != "" && strings.TrimRight(res, "/") != g.Resource {
		oauthErr(w, http.StatusBadRequest, "invalid_target", "resource eslesmiyor")
		return
	}
	scope := g.Scope
	if want := f.Get("scope"); want != "" {
		ws, unknown := ParseScopes(want)
		gs, _ := ParseScopes(g.Scope)
		if unknown != "" || !subsetOf(ws, gs) {
			oauthErr(w, http.StatusBadRequest, "invalid_scope", "kapsam verilen yetkinin alt kumesi olmali")
			return
		}
		scope = strings.Join(ws, " ")
	}
	s.issue(w, r, g, scope)
}

// issue, access token + yeni refresh token uretir ve yaniti yazar.
func (s *Server) issue(w http.ResponseWriter, r *http.Request, g Grant, scope string) {
	now := s.cfg.Now()
	tok, err := signJWT(s.priv, claims{
		Iss: s.Issuer(r), Sub: g.UserID, Aud: g.Resource, Scope: scope, Tid: g.TenantID,
		Gid: g.ID, Cid: g.ClientID, Iat: now.Unix(), Exp: now.Add(AccessTokenTTL).Unix(), Jti: randToken(12),
	})
	if err != nil {
		oauthErr(w, http.StatusInternalServerError, "server_error", "token uretilemedi")
		return
	}
	refresh := "zor_rt_" + randToken(32)
	if err := s.cfg.Store.SaveRefresh(r.Context(), hashToken(refresh), g.ID, now.Add(refreshTTL)); err != nil {
		oauthErr(w, http.StatusInternalServerError, "server_error", "refresh token saklanamadi")
		return
	}
	_ = s.cfg.Store.TouchGrant(r.Context(), g.ID, now)
	w.Header().Set("Pragma", "no-cache")
	writeJSON(w, http.StatusOK, map[string]any{
		"access_token":  tok,
		"token_type":    "Bearer",
		"expires_in":    int(AccessTokenTTL.Seconds()),
		"refresh_token": refresh,
		"scope":         scope,
	})
}

// --- iptal (RFC 7009) --------------------------------------------------------

func (s *Server) revoke(w http.ResponseWriter, r *http.Request) {
	if publicCORS(w, r) {
		return
	}
	if r.Method != http.MethodPost {
		oauthErr(w, http.StatusMethodNotAllowed, "invalid_request", "POST gerekli")
		return
	}
	if s.cfg.TokenLimiter != nil && !s.cfg.TokenLimiter.Allow(s.ip(r)) {
		oauthErr(w, http.StatusTooManyRequests, "temporarily_unavailable", "cok fazla istek")
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxFormBytes)
	if err := r.ParseForm(); err != nil {
		oauthErr(w, http.StatusBadRequest, "invalid_request", "gecersiz form")
		return
	}
	tok, clientID := r.PostForm.Get("token"), r.PostForm.Get("client_id")
	ctx := r.Context()
	now := s.cfg.Now()
	gid := ""
	if strings.HasPrefix(tok, "zor_rt_") {
		// Ozeti arayip grant'i bul: RotateRefresh yan etkili oldugundan
		// dogrudan kullanilir; kullanilmis olsa da grant iptal edilir.
		id, err := s.cfg.Store.RotateRefresh(ctx, hashToken(tok), now)
		if err == nil || errors.Is(err, ErrUsed) {
			gid = id
		}
	} else if c, err := parseJWT(s.pub, tok); err == nil {
		gid = c.Gid
	}
	if gid != "" {
		if g, err := s.cfg.Store.GetGrant(ctx, gid); err == nil && (clientID == "" || g.ClientID == clientID) {
			_ = s.cfg.Store.RevokeGrant(ctx, gid, now)
			s.forget(gid)
			s.audit(g.TenantID, "oauth:"+g.UserID, s.ip(r), "oauth.revoke", gid, "client="+g.ClientID)
		}
	}
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusOK) // RFC 7009: gecersiz token da 200
}

func (s *Server) forget(gid string) {
	s.mu.Lock()
	delete(s.cache, gid)
	s.mu.Unlock()
}

// Prune, suresi dolmus kayitlari siler (periyodik cagirin).
func (s *Server) Prune(ctx context.Context) error { return s.cfg.Store.Prune(ctx, s.cfg.Now()) }

// --- kaynak tarafi dogrulama -------------------------------------------------

// Principal, dogrulanmis bir OAuth access token'in kimligi. Yalnizca bu paketin
// Authenticate'i uretir; sureç-ici isteklerde context ile api.Middleware'e tasinir.
type Principal struct {
	UserID     string
	TenantID   string
	GrantID    string
	ClientID   string
	ClientName string
	// Scopes, OAuth kapsamlari; APIScopes bunlarin REST kapsamlarina genislemis hali.
	Scopes    []string
	APIScopes []string
}

type principalKey struct{}

// WithPrincipal, kimligi context'e koyar. YALNIZCA Authenticate sonucuyla,
// sureç-ici (disaridan taklit edilemeyen) cagrilarda kullanilmalidir.
func WithPrincipal(ctx context.Context, p Principal) context.Context {
	return context.WithValue(ctx, principalKey{}, p)
}

// PrincipalFrom, context'teki kimligi okur.
func PrincipalFrom(ctx context.Context) (Principal, bool) {
	p, ok := ctx.Value(principalKey{}).(Principal)
	return p, ok && p.UserID != "" && p.TenantID != ""
}

// LooksLikeAccessToken, bearer degerinin bir JWT gibi gorunup gorunmedigi (uc nokta).
func LooksLikeAccessToken(tok string) bool {
	return strings.Count(tok, ".") == 2 && !strings.HasPrefix(tok, "zrv_") && !strings.HasPrefix(tok, "zorven_")
}

// Authenticate, access token'i dogrular: imza, iss, aud, exp ve grant iptal durumu.
func (s *Server) Authenticate(r *http.Request, raw string) (Principal, error) {
	c, err := parseJWT(s.pub, raw)
	if err != nil {
		return Principal{}, err
	}
	now := s.cfg.Now()
	if c.Iss != s.Issuer(r) || c.Aud != s.Resource(r) || c.Exp <= now.Unix()-5 || c.Gid == "" || c.Sub == "" || c.Tid == "" {
		return Principal{}, ErrInvalidToken
	}
	g, err := s.grantCached(r.Context(), c.Gid, now)
	if err != nil || !g.Active() || g.UserID != c.Sub || g.TenantID != c.Tid || g.Resource != c.Aud {
		return Principal{}, ErrInvalidToken
	}
	scopes, unknown := ParseScopes(c.Scope)
	granted, _ := ParseScopes(g.Scope)
	if unknown != "" || !subsetOf(scopes, granted) {
		return Principal{}, ErrInvalidToken
	}
	return Principal{UserID: c.Sub, TenantID: c.Tid, GrantID: c.Gid, ClientID: c.Cid, ClientName: g.ClientName,
		Scopes: scopes, APIScopes: APIScopes(scopes)}, nil
}

func (s *Server) grantCached(ctx context.Context, id string, now time.Time) (Grant, error) {
	s.mu.Lock()
	if e, ok := s.cache[id]; ok && now.Sub(e.at) < grantCacheTTL {
		s.mu.Unlock()
		return e.g, nil
	}
	s.mu.Unlock()
	g, err := s.cfg.Store.GetGrant(ctx, id)
	if err != nil {
		return Grant{}, err
	}
	s.mu.Lock()
	if len(s.cache) > 4096 {
		s.cache = map[string]cachedGrant{}
	}
	s.cache[id] = cachedGrant{g: g, at: now}
	s.mu.Unlock()
	_ = s.cfg.Store.TouchGrant(ctx, id, now)
	return g, nil
}
