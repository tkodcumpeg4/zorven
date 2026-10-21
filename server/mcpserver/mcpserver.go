// Package mcpserver, Zorven'in uzak MCP sunucusunu (streamable HTTP, /mcp) kurar.
//
// Tasarim (docs/mcp.md): arac mantigi shared/mcptools'ta; burada yalnizca
//   - bearer (API token) dogrulamasi + WWW-Authenticate,
//   - Origin/CORS,
//   - kimlik dogrulamasi basarisizliklarina hiz siniri,
//   - her arac cagrisi icin denetim kaydi (govde YOK),
//   - arka uc: API'nin kimlik zincirine (api.Middleware) SUREC-ICI sentetik istek
//
// vardir. Sentetik istek, cagiranin Authorization basligini aynen tasir; yani
// token yuzeyi (tokenPublicRoutes), kapsam, plan/kota, kiraci hiz siniri ve
// denetim mantigi REST ile BIREBIR ayni calisir, sapma riski yoktur.
//
// Kabul edilen kimlikler: API token (zrv_api_...) ve Zorven OAuth AS'inin
// verdigi access token'lar (JWT; Config.OAuth). OAuth kimligi sentetik isteye
// Authorization BASLIGI olarak degil, surec-ici context degeri
// (oauthas.Principal) olarak eklenir; api.Middleware onu API token ile ayni
// kurallarla (uyelik, plan, yuzey, kapsam, hiz siniri) isler. Admin anahtari ve
// tarayici oturumu bilerek reddedilir.
package mcpserver

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/tkodcumpeg4/zorven/server/auth"
	"github.com/tkodcumpeg4/zorven/server/oauthas"
	"github.com/tkodcumpeg4/zorven/server/ratelimit"
	"github.com/tkodcumpeg4/zorven/shared/mcptools"
)

// Yol, MCP uc noktasi.
const Path = "/mcp"

const (
	identityTTL     = 30 * time.Second
	identityMaxKeys = 2048
	maxBodyBytes    = 1 << 20
)

// Config, MCP uc noktasinin bagimliliklari.
type Config struct {
	// API, kimlik dogrulama zincirli API isleyicisi (api.Middleware). ZORUNLU.
	API http.Handler
	// OriginTrusted, tarayici Origin'inin guvenilir olup olmadigi. nil ise
	// Origin tasiyan HER istek reddedilir (yalnizca CLI/sunucu istemcileri).
	OriginTrusted func(*http.Request) bool
	// Audit, her arac cagrisi icin denetim kaydi yazar (nil olabilir).
	Audit func(tenantID, actor, ip, action, target, detail string)
	// FailLimiter, basarisiz kimlik denemelerini IP basina sinirlar (nil olabilir).
	FailLimiter *ratelimit.Limiter
	// ClientIP, hiz siniri/denetim icin istemci IP'si; nil ise RemoteAddr.
	ClientIP func(*http.Request) string
	Version  string
	Logger   *slog.Logger
	// OAuth, Zorven OAuth AS'i (access token dogrulama + PRM adresi). nil ise
	// yalnizca API token kabul edilir.
	OAuth TokenVerifier
}

// TokenVerifier, OAuth access token'larini dogrulayan kaynak-tarafi arayuzu
// (oauthas.Server uygular).
type TokenVerifier interface {
	Authenticate(r *http.Request, raw string) (oauthas.Principal, error)
	ResourceMetadataURL(r *http.Request) string
}

type identity struct {
	tenantID  string
	actor     string
	scopes    []string
	expires   time.Time
	principal *oauthas.Principal // OAuth ise dolu
}

// Handler, /mcp isleyicisi.
type Handler struct {
	cfg    Config
	sdk    *mcp.StreamableHTTPHandler
	schema *mcp.SchemaCache

	mu    sync.Mutex
	cache map[string]identity
}

type ctxKey struct{}

// New, isleyiciyi kurar.
func New(cfg Config) *Handler {
	if cfg.Logger == nil {
		cfg.Logger = slog.Default()
	}
	h := &Handler{cfg: cfg, schema: mcp.NewSchemaCache(), cache: map[string]identity{}}
	h.sdk = mcp.NewStreamableHTTPHandler(h.serverFor, &mcp.StreamableHTTPOptions{
		Stateless:    true,
		JSONResponse: true,
		// Yerel koruma kapali: istekler zaten yalnizca kontrol host'larina yonlenir
		// ve Origin + bearer burada ayrica denetlenir. Acik olsaydi, ters vekil
		// arkasinda (127.0.0.1'den gelen) gercek Host'lu istekler reddedilirdi.
		DisableLocalhostProtection: true,
		MaxRequestBodyBytes:        maxBodyBytes,
	})
	return h
}

func (h *Handler) ip(r *http.Request) string {
	if h.cfg.ClientIP != nil {
		return h.cfg.ClientIP(r)
	}
	return r.RemoteAddr
}

// ServeHTTP, istegi dogrular ve MCP isleyicisine devreder.
func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")

	// Origin: DNS yeniden baglama / capraz site saldirisi onlemi (MCP spec).
	// Origin'siz istekler (CLI, sunucu istemcileri) gecer.
	if origin := r.Header.Get("Origin"); origin != "" {
		if h.cfg.OriginTrusted == nil || !h.cfg.OriginTrusted(r) {
			writeErr(w, http.StatusForbidden, "forbidden_origin", "bu Origin MCP uc noktasina erisemez")
			return
		}
		hd := w.Header()
		hd.Set("Access-Control-Allow-Origin", origin)
		hd.Add("Vary", "Origin")
		hd.Set("Access-Control-Expose-Headers", "WWW-Authenticate, Mcp-Session-Id, Retry-After")
		if r.Method == http.MethodOptions {
			hd.Set("Access-Control-Allow-Methods", "POST, GET, DELETE, OPTIONS")
			hd.Set("Access-Control-Allow-Headers", "Authorization, Content-Type, Accept, Mcp-Session-Id, Mcp-Protocol-Version, Last-Event-ID, X-Zorven-Project")
			hd.Set("Access-Control-Max-Age", "600")
			w.WriteHeader(http.StatusNoContent)
			return
		}
	} else if r.Method == http.MethodOptions {
		w.Header().Set("Allow", "POST, OPTIONS")
		w.WriteHeader(http.StatusNoContent)
		return
	}

	id, ok := h.authenticate(w, r)
	if !ok {
		return
	}
	r = r.WithContext(context.WithValue(r.Context(), ctxKey{}, id))
	h.sdk.ServeHTTP(w, r)
}

func bearer(r *http.Request) string {
	f := strings.Fields(r.Header.Get("Authorization"))
	if len(f) == 2 && strings.EqualFold(f[0], "bearer") {
		return f[1]
	}
	return ""
}

// challenge, 401 icin WWW-Authenticate yazar. OAuth AS etkinse RFC 9728
// resource_metadata adresi her zaman eklenir (MCP spec: istemci buradan
// yetkilendirme sunucusunu kesfeder).
func (h *Handler) challenge(w http.ResponseWriter, r *http.Request, errCode, desc string) {
	v := `Bearer realm="zorven"`
	if h.cfg.OAuth != nil {
		v += `, resource_metadata="` + h.cfg.OAuth.ResourceMetadataURL(r) + `"`
	}
	if errCode != "" {
		v += `, error="` + errCode + `"`
		if desc != "" {
			v += `, error_description="` + desc + `"`
		}
	}
	w.Header().Set("WWW-Authenticate", v)
}

// failed, basarisiz bir kimlik denemesini sayar; sinir asildiysa 429 yazar.
func (h *Handler) failed(w http.ResponseWriter, r *http.Request) bool {
	if h.cfg.FailLimiter != nil && !h.cfg.FailLimiter.Allow(h.ip(r)) {
		w.Header().Set("Retry-After", "30")
		writeErr(w, http.StatusTooManyRequests, "rate_limited", "cok fazla basarisiz kimlik denemesi")
		return true
	}
	return false
}

// authenticate, bearer'i dogrular ve kimligi (kiraci, kapsamlar) cikarir.
func (h *Handler) authenticate(w http.ResponseWriter, r *http.Request) (identity, bool) {
	tok := bearer(r)
	if tok == "" {
		if h.failed(w, r) {
			return identity{}, false
		}
		h.challenge(w, r, "", "")
		writeErr(w, http.StatusUnauthorized, "unauthorized", "Authorization: Bearer <token> gerekli")
		return identity{}, false
	}

	var prin *oauthas.Principal
	isAPI := strings.HasPrefix(tok, auth.PrefixAPI) || strings.HasPrefix(tok, "zorven_api_")
	switch {
	case isAPI:
	case h.cfg.OAuth != nil && oauthas.LooksLikeAccessToken(tok):
		p, err := h.cfg.OAuth.Authenticate(r, tok)
		if err != nil {
			if h.failed(w, r) {
				return identity{}, false
			}
			h.challenge(w, r, "invalid_token", "gecersiz, suresi dolmus veya iptal edilmis access token")
			writeErr(w, http.StatusUnauthorized, "invalid_token", "gecersiz, suresi dolmus veya iptal edilmis access token")
			return identity{}, false
		}
		prin = &p
	default:
		// Istemci/admin anahtari ve oturum token'lari reddedilir.
		if h.failed(w, r) {
			return identity{}, false
		}
		h.challenge(w, r, "invalid_token", "yalnizca API token veya OAuth access token kabul edilir")
		writeErr(w, http.StatusUnauthorized, "invalid_token", "MCP yalnizca API token (zrv_api_...) veya OAuth access token kabul eder")
		return identity{}, false
	}

	project := strings.TrimSpace(r.Header.Get("X-Zorven-Project"))
	keySrc := tok + "\x00" + project
	if prin != nil {
		// OAuth: JWT her istekte dogrulandi (iptal dahil); /me sonucu grant+kapsam
		// bazinda kisa sure onbellekte.
		keySrc = "oauth\x00" + prin.GrantID + "\x00" + strings.Join(prin.Scopes, " ") + "\x00" + project
	}
	sum := sha256.Sum256([]byte(keySrc))
	key := hex.EncodeToString(sum[:])
	if id, ok := h.cached(key); ok {
		id.principal = prin
		return id, true
	}

	// Onbellek yok: API zincirinden GET /me ile dogrula + kapsamlari ogren.
	be := &inprocBackend{api: h.cfg.API, auth: "Bearer " + tok, remote: r.RemoteAddr, host: r.Host,
		project: project, principal: prin}
	resp, err := be.Do(r.Context(), mcptools.Request{Method: http.MethodGet, Path: "/api/v1/me"})
	if err != nil {
		writeErr(w, http.StatusBadGateway, "upstream_error", "kimlik dogrulanamadi")
		return identity{}, false
	}
	switch {
	case resp.Status == http.StatusUnauthorized:
		if h.failed(w, r) {
			return identity{}, false
		}
		h.challenge(w, r, "invalid_token", "gecersiz veya suresi dolmus token")
		writeErr(w, http.StatusUnauthorized, "invalid_token", "gecersiz, iptal edilmis veya suresi dolmus token")
		return identity{}, false
	case resp.Status != http.StatusOK:
		// 403 (plan), 429 (kiraci siniri) ... API'nin yanitini aynen ilet.
		if ra := resp.Header.Get("Retry-After"); ra != "" {
			w.Header().Set("Retry-After", ra)
		}
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.WriteHeader(resp.Status)
		_, _ = w.Write(resp.Body)
		return identity{}, false
	}
	var me map[string]any
	if err := json.Unmarshal(resp.Body, &me); err != nil {
		writeErr(w, http.StatusBadGateway, "upstream_error", "kimlik yaniti cozulemedi")
		return identity{}, false
	}
	scopes := mcptools.ScopesFromMe(me)
	if scopes == nil {
		// Token yoluyla /me her zaman scopes doner; yoksa guvenli taraf: hicbiri.
		scopes = []string{}
	}
	id := identity{scopes: scopes, actor: "mcp:api-token", expires: time.Now().Add(identityTTL)}
	if prin != nil {
		id.actor = "mcp:oauth:" + prin.ClientName + ":" + prin.UserID
	}
	if t, ok := me["tenant"].(map[string]any); ok {
		id.tenantID, _ = t["id"].(string)
	}
	if u, ok := me["user"].(map[string]any); ok && prin == nil {
		if e, _ := u["email"].(string); e != "" {
			id.actor = "mcp:" + e
		} else if uid, _ := u["id"].(string); uid != "" {
			id.actor = "mcp:" + uid
		}
	}
	h.store(key, id)
	id.principal = prin
	return id, true
}

func (h *Handler) cached(key string) (identity, bool) {
	h.mu.Lock()
	defer h.mu.Unlock()
	id, ok := h.cache[key]
	if !ok || time.Now().After(id.expires) {
		delete(h.cache, key)
		return identity{}, false
	}
	return id, true
}

func (h *Handler) store(key string, id identity) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if len(h.cache) >= identityMaxKeys {
		now := time.Now()
		for k, v := range h.cache {
			if now.After(v.expires) {
				delete(h.cache, k)
			}
		}
		if len(h.cache) >= identityMaxKeys {
			h.cache = map[string]identity{}
		}
	}
	h.cache[key] = id
}

// serverFor, istek basina kapsama gore kisilmis bir MCP sunucusu kurar.
func (h *Handler) serverFor(r *http.Request) *mcp.Server {
	id, ok := r.Context().Value(ctxKey{}).(identity)
	if !ok {
		return nil
	}
	be := &inprocBackend{api: h.cfg.API, auth: "Bearer " + bearer(r), remote: r.RemoteAddr, host: r.Host,
		project: strings.TrimSpace(r.Header.Get("X-Zorven-Project")), principal: id.principal}
	ip := h.ip(r)
	return mcptools.NewServer(be, mcptools.Options{
		Version:     h.cfg.Version,
		Scopes:      id.scopes,
		SchemaCache: h.schema,
		OnCall: func(tool string, status int, err error) {
			if h.cfg.Audit == nil {
				return
			}
			result := "ok"
			if err != nil {
				result = "error"
			}
			// Yalnizca arac adi + durum: girdi/cikti ASLA yazilmaz.
			h.cfg.Audit(id.tenantID, id.actor, ip, "mcp.tool", tool,
				fmt.Sprintf("via=mcp status=%d result=%s", status, result))
		},
	})
}

func writeErr(w http.ResponseWriter, status int, code, msg string) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]any{"error": map[string]string{"code": code, "message": msg}})
}

// --- surec-ici arka uc -----------------------------------------------------

// inprocBackend, mcptools.Backend'in API zincirine surec-ici sentetik istek
// atan uygulamasi.
type inprocBackend struct {
	api     http.Handler
	auth    string
	remote  string
	host    string
	project string // X-Zorven-Project varsayilani (arac project_id vermediyse)
	// principal doluysa istek OAuth kimligiyle atilir: Authorization basligi
	// EKLENMEZ, kimlik surec-ici context degeri olarak tasinir.
	principal *oauthas.Principal
}

func (b *inprocBackend) Do(ctx context.Context, r mcptools.Request) (mcptools.Response, error) {
	target := r.Path
	if len(r.Query) > 0 {
		target += "?" + r.Query.Encode()
	}
	var body io.Reader
	if r.Body != nil {
		raw, err := json.Marshal(r.Body)
		if err != nil {
			return mcptools.Response{}, err
		}
		body = bytes.NewReader(raw)
	}
	u, err := url.ParseRequestURI(target)
	if err != nil {
		return mcptools.Response{}, err
	}
	if b.principal != nil {
		ctx = oauthas.WithPrincipal(ctx, *b.principal)
	}
	req, err := http.NewRequestWithContext(ctx, r.Method, u.String(), body)
	if err != nil {
		return mcptools.Response{}, err
	}
	req.Host = b.host
	req.RemoteAddr = b.remote
	if b.principal == nil {
		req.Header.Set("Authorization", b.auth)
	}
	req.Header.Set("Accept", "application/json")
	if r.Body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	project := r.Project
	if project == "" {
		project = b.project
	}
	if project != "" {
		req.Header.Set("X-Zorven-Project", project)
	}
	rec := &recorder{h: http.Header{}}
	b.api.ServeHTTP(rec, req)
	if rec.status == 0 {
		rec.status = http.StatusOK
	}
	return mcptools.Response{Status: rec.status, Body: rec.buf.Bytes(), Header: rec.h}, nil
}

// maxInprocBody, surec-ici yanit tamponu tavani.
const maxInprocBody = 8 << 20

type recorder struct {
	h      http.Header
	status int
	buf    bytes.Buffer
}

func (r *recorder) Header() http.Header { return r.h }
func (r *recorder) WriteHeader(c int) {
	if r.status == 0 {
		r.status = c
	}
}
func (r *recorder) Write(p []byte) (int, error) {
	if r.status == 0 {
		r.status = http.StatusOK
	}
	if r.buf.Len()+len(p) > maxInprocBody {
		return 0, io.ErrShortWrite
	}
	return r.buf.Write(p)
}
func (r *recorder) Flush() {}
