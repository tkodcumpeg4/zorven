package api

// Zorven CAPTCHA (Turnstile benzeri widget + dogrulama servisi).
//
// PUBLIC (kimlik yok; main.go dogrudan yonlendirir, IP basina hiz sinirli):
//
//	GET  /captcha/v1/api.js                 — widget betigi
//	GET  /api/v1/captcha/platform-config    — {enabled, sitekey}
//	POST /api/v1/captcha/challenge          — widget: PoW sorgusu
//	POST /api/v1/captcha/solve              — widget: cozum -> tek kullanimlik yanit
//	POST /api/v1/captcha/renew              — widget: gecis cerezi ile PoW'suz yanit
//	POST /api/v1/captcha/siteverify         — sitenin sunucusu: secret + response dogrulama
//
// KIRACI (oturum; yazma owner/admin, okuma member):
//
//	GET    /api/v1/captcha/sites
//	POST   /api/v1/captcha/sites
//	PATCH  /api/v1/captcha/sites/{id}
//	DELETE /api/v1/captcha/sites/{id}
//	POST   /api/v1/captcha/sites/{id}/rotate-secret
//
// Gizli anahtar (zcsec_...) yalniz olusturma/yenilemede BIR KEZ doner; DB'de
// yalniz SHA-256 ozeti saklanir. CORS: widget uclari Origin'i yansitir (kimlik
// bilgisi yok); izin kontrolu sitenin alan listesine gore govdede yapilir.

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/tkodcumpeg4/zorven/server/botguard"
	"github.com/tkodcumpeg4/zorven/server/captcha"
	"github.com/tkodcumpeg4/zorven/server/store"
)

// Public yollar (main.go anahtarlama icin kullanir).
const (
	CaptchaAssetPath       = "/captcha/v1/api.js"
	captchaAPIPrefix       = "/api/v1/captcha/"
	maxCaptchaSitesPerOrg  = 20
	captchaPublicBodyLimit = 16 << 10
)

// IsCaptchaPublicPath, main.go'nun kimliksiz yonlendirecegi CAPTCHA yolu mu.
// siteverify ayrica doner (IP hiz sinirina tabi tutulmamali: sunucudan sunucuya).
func IsCaptchaPublicPath(p string) (public, serverToServer bool) {
	switch p {
	case CaptchaAssetPath, captchaAPIPrefix + "challenge", captchaAPIPrefix + "solve",
		captchaAPIPrefix + "renew", captchaAPIPrefix + "platform-config":
		return true, false
	case captchaAPIPrefix + "siteverify":
		return true, true
	}
	return false, false
}

func (s *Server) registerCaptcha(mux *http.ServeMux) {
	mux.HandleFunc("GET "+CaptchaAssetPath, s.captchaAsset)
	mux.HandleFunc("GET /api/v1/captcha/platform-config", s.captchaPlatformConfig)
	for _, ep := range []string{"challenge", "solve", "renew"} {
		mux.HandleFunc("OPTIONS "+captchaAPIPrefix+ep, s.captchaPreflight)
	}
	mux.HandleFunc("POST /api/v1/captcha/challenge", s.captchaChallenge)
	mux.HandleFunc("POST /api/v1/captcha/solve", s.captchaSolve)
	mux.HandleFunc("POST /api/v1/captcha/renew", s.captchaRenew)
	mux.HandleFunc("POST /api/v1/captcha/siteverify", s.captchaSiteverify)

	mux.HandleFunc("GET /api/v1/captcha/sites", s.listCaptchaSites)
	mux.HandleFunc("POST /api/v1/captcha/sites", s.createCaptchaSite)
	mux.HandleFunc("PATCH /api/v1/captcha/sites/{id}", s.updateCaptchaSite)
	mux.HandleFunc("DELETE /api/v1/captcha/sites/{id}", s.deleteCaptchaSite)
	mux.HandleFunc("POST /api/v1/captcha/sites/{id}/rotate-secret", s.rotateCaptchaSecret)
}

// ---- widget varligi ----

var (
	captchaETagOnce sync.Once
	captchaETag     string
)

func (s *Server) captchaAsset(w http.ResponseWriter, r *http.Request) {
	captchaETagOnce.Do(func() {
		h := sha256.Sum256(captcha.WidgetJS)
		captchaETag = `"zcv1-` + hex.EncodeToString(h[:8]) + `"`
	})
	h := w.Header()
	h.Set("Content-Type", "application/javascript; charset=utf-8")
	h.Set("Cache-Control", "public, max-age=86400, stale-while-revalidate=604800")
	h.Set("ETag", captchaETag)
	h.Set("X-Content-Type-Options", "nosniff")
	h.Set("Access-Control-Allow-Origin", "*")
	h.Set("Cross-Origin-Resource-Policy", "cross-origin")
	if inm := r.Header.Get("If-None-Match"); inm != "" && strings.Contains(inm, captchaETag) {
		w.WriteHeader(http.StatusNotModified)
		return
	}
	_, _ = w.Write(captcha.WidgetJS)
}

// ---- yardimcilar ----

func (s *Server) captchaSvc(w http.ResponseWriter) (*captcha.Service, bool) {
	if s.Captcha == nil {
		writeJSONError(w, http.StatusNotImplemented, "not_supported", "captcha bu sunucuda etkin degil")
		return nil, false
	}
	return s.Captcha, true
}

func (s *Server) captchaStore(w http.ResponseWriter) (store.CaptchaStore, bool) {
	cs, ok := s.Store.(store.CaptchaStore)
	if !ok {
		writeJSONError(w, http.StatusNotImplemented, "not_supported", "captcha bu depoda desteklenmiyor")
		return nil, false
	}
	return cs, true
}

var (
	captchaLimOnce sync.Once
	captchaLim     *botguard.Limiter
)

// captchaAllow, IP onegi basina (IPv4 /24, IPv6 /64) token bucket siniri.
func (s *Server) captchaAllow(w http.ResponseWriter, r *http.Request, group string, perMin int) bool {
	captchaLimOnce.Do(func() { captchaLim = botguard.NewLimiter(50_000) })
	ok, wait := captchaLim.Allow(group+":"+botguard.IPPrefix(clientIP(r)), perMin, time.Minute, 1)
	if ok {
		return true
	}
	w.Header().Set("Retry-After", strconv.Itoa(int(wait.Seconds())+1))
	s.captchaCORS(w, r)
	writeJSONError(w, http.StatusTooManyRequests, "rate_limited", "cok fazla istek, lutfen biraz sonra tekrar deneyin")
	return false
}

// captchaCORS: kimlik bilgisi tasimayan widget uclari icin Origin'i yansitir.
// Yetkilendirme (alan listesi) govdede yapilir; reddedilen cevap da okunabilsin
// diye hata yanitlarinda da yansitilir.
func (s *Server) captchaCORS(w http.ResponseWriter, r *http.Request) {
	h := w.Header()
	h.Add("Vary", "Origin")
	if o := r.Header.Get("Origin"); o != "" && o != "null" {
		h.Set("Access-Control-Allow-Origin", o)
	}
	h.Set("Cache-Control", "no-store")
}

func (s *Server) captchaPreflight(w http.ResponseWriter, r *http.Request) {
	h := w.Header()
	h.Add("Vary", "Origin")
	if o := r.Header.Get("Origin"); o != "" && o != "null" {
		h.Set("Access-Control-Allow-Origin", o)
	}
	h.Set("Access-Control-Allow-Methods", "POST, OPTIONS")
	h.Set("Access-Control-Allow-Headers", "Content-Type")
	h.Set("Access-Control-Max-Age", "86400")
	w.WriteHeader(http.StatusNoContent)
}

// captchaDecode: Content-Type'a bakmadan JSON govdeyi cozer (widget preflight'siz
// text/plain gonderir). Bilinmeyen alanlar yok sayilir.
func captchaDecode(w http.ResponseWriter, r *http.Request, v any) bool {
	b, err := io.ReadAll(http.MaxBytesReader(w, r.Body, captchaPublicBodyLimit))
	if err != nil {
		writeJSONError(w, http.StatusRequestEntityTooLarge, "too_large", "govde cok buyuk")
		return false
	}
	if err := json.Unmarshal(b, v); err != nil {
		writeJSONError(w, http.StatusBadRequest, "invalid_json", "gecersiz JSON govde")
		return false
	}
	return true
}

func captchaReqCtx(r *http.Request) (host, ua string, acceptLang bool) {
	return captcha.OriginHost(r.Header.Get("Origin"), r.Header.Get("Referer")),
		r.UserAgent(), r.Header.Get("Accept-Language") != ""
}

func captchaErr(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, captcha.ErrUnknownSite):
		writeJSONError(w, http.StatusNotFound, "unknown_site", "bilinmeyen site anahtari")
	case errors.Is(err, captcha.ErrOriginNotAllow):
		writeJSONError(w, http.StatusForbidden, "origin_not_allowed", "bu alan adi sitenin izinli alan listesinde degil")
	case errors.Is(err, captcha.ErrOriginMismatch):
		writeJSONError(w, http.StatusForbidden, "origin_mismatch", "cozum sorgudan farkli bir alan adindan geldi")
	case errors.Is(err, captcha.ErrAutomated):
		writeJSONError(w, http.StatusForbidden, "automated", "otomasyon belirtileri algilandi")
	case errors.Is(err, captcha.ErrExpired):
		writeJSONError(w, http.StatusBadRequest, "expired", "jeton suresi doldu")
	case errors.Is(err, captcha.ErrReplay):
		writeJSONError(w, http.StatusBadRequest, "already_used", "jeton zaten kullanildi")
	case errors.Is(err, captcha.ErrPoW):
		writeJSONError(w, http.StatusBadRequest, "invalid_solution", "gecersiz cozum")
	case errors.Is(err, captcha.ErrToken):
		writeJSONError(w, http.StatusBadRequest, "invalid_token", "gecersiz jeton")
	default:
		writeJSONError(w, http.StatusInternalServerError, "internal", "beklenmeyen hata")
	}
}

// ---- public uclar ----

func (s *Server) captchaPlatformConfig(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "public, max-age=60")
	w.Header().Set("Access-Control-Allow-Origin", "*")
	if s.Captcha != nil && s.Captcha.PlatformEnabled() {
		writeJSON(w, http.StatusOK, map[string]any{"enabled": true, "sitekey": s.Captcha.PlatformSiteKey()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"enabled": false, "sitekey": ""})
}

func (s *Server) captchaChallenge(w http.ResponseWriter, r *http.Request) {
	s.captchaCORS(w, r)
	svc, ok := s.captchaSvc(w)
	if !ok || !s.captchaAllow(w, r, "ch", 60) {
		return
	}
	var body struct {
		SiteKey string           `json:"sitekey"`
		Signals *captcha.Signals `json:"signals"`
	}
	if !captchaDecode(w, r, &body) {
		return
	}
	if body.SiteKey == "" {
		writeJSONError(w, http.StatusBadRequest, "missing_fields", "sitekey zorunlu")
		return
	}
	host, ua, al := captchaReqCtx(r)
	res, err := svc.Challenge(r.Context(), captcha.ChallengeRequest{
		SiteKey: body.SiteKey, OriginHost: host, UserAgent: ua, AcceptLang: al, Signals: body.Signals})
	if err != nil {
		captchaErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, res)
}

func (s *Server) captchaSolve(w http.ResponseWriter, r *http.Request) {
	s.captchaCORS(w, r)
	svc, ok := s.captchaSvc(w)
	if !ok || !s.captchaAllow(w, r, "sv", 60) {
		return
	}
	var body struct {
		Token   string           `json:"token"`
		Counter json.Number      `json:"counter"`
		Signals *captcha.Signals `json:"signals"`
	}
	if !captchaDecode(w, r, &body) {
		return
	}
	if body.Token == "" || body.Counter == "" {
		writeJSONError(w, http.StatusBadRequest, "missing_fields", "token ve counter zorunlu")
		return
	}
	host, ua, al := captchaReqCtx(r)
	res, err := svc.Solve(r.Context(), captcha.SolveRequest{
		Token: body.Token, Counter: body.Counter.String(), Signals: body.Signals,
		OriginHost: host, UserAgent: ua, AcceptLang: al})
	if err != nil {
		captchaErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, res)
}

func (s *Server) captchaRenew(w http.ResponseWriter, r *http.Request) {
	s.captchaCORS(w, r)
	svc, ok := s.captchaSvc(w)
	if !ok || !s.captchaAllow(w, r, "rn", 120) {
		return
	}
	var body struct {
		SiteKey string `json:"sitekey"`
		Pass    string `json:"pass"`
	}
	if !captchaDecode(w, r, &body) {
		return
	}
	if body.SiteKey == "" || body.Pass == "" {
		writeJSONError(w, http.StatusBadRequest, "missing_fields", "sitekey ve pass zorunlu")
		return
	}
	host, _, _ := captchaReqCtx(r)
	res, err := svc.Renew(r.Context(), captcha.RenewRequest{SiteKey: body.SiteKey, Pass: body.Pass, OriginHost: host})
	if err != nil {
		captchaErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, res)
}

// captchaSiteverify: Turnstile uyumlu; form (x-www-form-urlencoded) ya da JSON.
// Basarisiz dogrulama da HTTP 200 + success:false doner; yalniz ic hata 500.
func (s *Server) captchaSiteverify(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	svc, ok := s.captchaSvc(w)
	if !ok {
		return
	}
	var secret, response, remoteIP string
	mt, _, _ := mime.ParseMediaType(r.Header.Get("Content-Type"))
	raw, err := io.ReadAll(http.MaxBytesReader(w, r.Body, captchaPublicBodyLimit))
	if err != nil {
		writeJSONError(w, http.StatusRequestEntityTooLarge, "too_large", "govde cok buyuk")
		return
	}
	if mt == "application/json" {
		var b struct {
			Secret   string `json:"secret"`
			Response string `json:"response"`
			RemoteIP string `json:"remoteip"`
		}
		_ = json.Unmarshal(raw, &b)
		secret, response, remoteIP = b.Secret, b.Response, b.RemoteIP
	} else {
		q, _ := url.ParseQuery(string(raw))
		secret, response, remoteIP = q.Get("secret"), q.Get("response"), q.Get("remoteip")
	}
	res := svc.Verify(r.Context(), secret, response, clientIP(r))
	_ = remoteIP // alinir ama dogrulamada kullanilmaz (IP baglamasi yok)
	status := http.StatusOK
	for _, c := range res.ErrorCodes {
		if c == captcha.CodeInternal {
			status = http.StatusInternalServerError
		}
	}
	writeJSON(w, status, res)
}

// ---- kiraci site yonetimi ----

type captchaSiteOut struct {
	ID         string         `json:"id"`
	Name       string         `json:"name"`
	SiteKey    string         `json:"site_key"`
	Domains    []string       `json:"domains"`
	Difficulty int            `json:"difficulty"`
	PassCookie captchaPassOut `json:"pass_cookie"`
	CreatedAt  time.Time      `json:"created_at"`
	UpdatedAt  time.Time      `json:"updated_at"`
	Stats      *captchaStats  `json:"stats,omitempty"`
	Secret     string         `json:"secret,omitempty"` // yalniz olusturma/yenilemede
}

type captchaPassOut struct {
	Enabled    bool `json:"enabled"`
	TTLMinutes int  `json:"ttl_minutes"`
}

type captchaTotals struct {
	Challenge    int64 `json:"challenge"`
	Solve        int64 `json:"solve"`
	VerifyOK     int64 `json:"verify_ok"`
	VerifyFail   int64 `json:"verify_fail"`
	Renewals     int64 `json:"renewals"`
	UniquePasses int64 `json:"unique_passes"`
}

type captchaStats struct {
	Today captchaTotals `json:"today"`
	Last7 captchaTotals `json:"last_7_days"`
}

func captchaOut(c store.CaptchaSite) captchaSiteOut {
	ttl := c.PassTTLMinutes
	if ttl == 0 {
		ttl = captcha.PassDefaultTTL
	}
	return captchaSiteOut{ID: c.ID, Name: c.Name, SiteKey: c.SiteKey, Domains: c.Domains,
		Difficulty: c.Difficulty, PassCookie: captchaPassOut{Enabled: c.PassEnabled, TTLMinutes: ttl},
		CreatedAt: c.CreatedAt, UpdatedAt: c.UpdatedAt}
}

func (t *captchaTotals) add(d store.CaptchaCounterDay) {
	t.Challenge += d.Challenge
	t.Solve += d.Solve
	t.VerifyOK += d.VerifyOK
	t.VerifyFail += d.VerifyFail
	t.Renewals += d.Renewals
	t.UniquePasses += d.UniquePasses
}

func (s *Server) captchaGate(w http.ResponseWriter, r *http.Request, write bool) (store.CaptchaStore, string, bool) {
	scope := ScopeTunnelsRead
	if write {
		scope = ScopeTunnelsWrite
	}
	if !requireScope(w, r, scope) {
		return nil, "", false
	}
	tenantID, ok := s.tenantFor(r)
	if !ok {
		writeJSONError(w, http.StatusInternalServerError, "no_tenant", "istek kiraci kapsami olmadan ulasti")
		return nil, "", false
	}
	cs, ok := s.captchaStore(w)
	if !ok {
		return nil, "", false
	}
	if write && !s.requirePrivileged(w, r, tenantID) {
		return nil, "", false
	}
	return cs, tenantID, true
}

func (s *Server) listCaptchaSites(w http.ResponseWriter, r *http.Request) {
	cs, tenantID, ok := s.captchaGate(w, r, false)
	if !ok {
		return
	}
	sites, err := cs.ListCaptchaSites(r.Context(), tenantID)
	if err != nil {
		s.fail(w, err)
		return
	}
	ids := make([]string, 0, len(sites))
	for _, c := range sites {
		ids = append(ids, c.ID)
	}
	now := time.Now().UTC()
	rows, err := cs.CaptchaCounters(r.Context(), ids, now.AddDate(0, 0, -6))
	if err != nil {
		s.fail(w, err)
		return
	}
	today := now.Format("2006-01-02")
	stats := map[string]*captchaStats{}
	for _, c := range sites {
		stats[c.ID] = &captchaStats{}
	}
	for _, d := range rows {
		st := stats[d.SiteID]
		if st == nil {
			continue
		}
		st.Last7.add(d)
		if d.Day.UTC().Format("2006-01-02") == today {
			st.Today.add(d)
		}
	}
	// Henuz veritabanina yazilmamis (<=30 sn) artislar bugune eklenir.
	if s.Captcha != nil {
		for id, p := range s.Captcha.PendingCounters() {
			if st := stats[id]; st != nil {
				d := store.CaptchaCounterDay{Challenge: p.Challenge, Solve: p.Solve, VerifyOK: p.VerifyOK,
					VerifyFail: p.VerifyFail, Renewals: p.Renewals, UniquePasses: p.UniquePasses}
				st.Today.add(d)
				st.Last7.add(d)
			}
		}
	}
	out := make([]captchaSiteOut, 0, len(sites))
	for _, c := range sites {
		o := captchaOut(c)
		o.Stats = stats[c.ID]
		out = append(out, o)
	}
	writeJSON(w, http.StatusOK, map[string]any{"sites": out})
}

type captchaPassIn struct {
	Enabled    *bool `json:"enabled"`
	TTLMinutes *int  `json:"ttl_minutes"`
}

// validPass: ttl 5-43200 dakika (verilmisse).
func validPass(p *captchaPassIn) string {
	if p != nil && p.TTLMinutes != nil && (*p.TTLMinutes < captcha.PassMinTTL || *p.TTLMinutes > captcha.PassMaxTTL) {
		return "pass_cookie.ttl_minutes 5 ile 43200 arasinda olmali"
	}
	return ""
}

func (s *Server) createCaptchaSite(w http.ResponseWriter, r *http.Request) {
	cs, tenantID, ok := s.captchaGate(w, r, true)
	if !ok {
		return
	}
	var body struct {
		Name       string         `json:"name"`
		Domains    []string       `json:"domains"`
		Difficulty int            `json:"difficulty"`
		PassCookie *captchaPassIn `json:"pass_cookie"`
	}
	if !decode(w, r, &body) {
		return
	}
	name := strings.TrimSpace(body.Name)
	if name == "" || len(name) > 100 {
		writeJSONError(w, http.StatusUnprocessableEntity, "missing_fields", "name zorunlu (en fazla 100 karakter)")
		return
	}
	if body.Difficulty != 0 && (body.Difficulty < captcha.MinDifficulty || body.Difficulty > captcha.MaxDifficulty) {
		writeJSONError(w, http.StatusUnprocessableEntity, "invalid_difficulty", "difficulty 14 ile 22 arasinda olmali")
		return
	}
	if m := validPass(body.PassCookie); m != "" {
		writeJSONError(w, http.StatusUnprocessableEntity, "invalid_pass_cookie", m)
		return
	}
	doms, err := captcha.NormalizeDomains(body.Domains)
	if err != nil {
		writeJSONError(w, http.StatusUnprocessableEntity, "invalid_domains", err.Error())
		return
	}
	existing, err := cs.ListCaptchaSites(r.Context(), tenantID)
	if err != nil {
		s.fail(w, err)
		return
	}
	if len(existing) >= maxCaptchaSitesPerOrg {
		writeJSONError(w, http.StatusUnprocessableEntity, "limit_reached", "bir organizasyon en fazla 20 CAPTCHA sitesi olusturabilir")
		return
	}
	secret := captcha.NewSecret()
	in := store.CaptchaSite{TenantID: tenantID, Name: name, SiteKey: captcha.NewSiteKey(),
		SecretHash: captcha.HashSecret(secret), Domains: doms, Difficulty: captcha.ClampDifficulty(body.Difficulty),
		PassTTLMinutes: captcha.PassDefaultTTL}
	if body.PassCookie != nil {
		if body.PassCookie.Enabled != nil {
			in.PassEnabled = *body.PassCookie.Enabled
		}
		if body.PassCookie.TTLMinutes != nil {
			in.PassTTLMinutes = *body.PassCookie.TTLMinutes
		}
	}
	site, err := cs.CreateCaptchaSite(r.Context(), in)
	if err != nil {
		s.fail(w, err)
		return
	}
	s.audit(r, "captcha_site.create", site.ID, site.Name)
	o := captchaOut(site)
	o.Secret = secret
	writeJSON(w, http.StatusCreated, o)
}

func (s *Server) updateCaptchaSite(w http.ResponseWriter, r *http.Request) {
	cs, tenantID, ok := s.captchaGate(w, r, true)
	if !ok {
		return
	}
	var body struct {
		Name       *string        `json:"name"`
		Domains    *[]string      `json:"domains"`
		Difficulty *int           `json:"difficulty"`
		PassCookie *captchaPassIn `json:"pass_cookie"`
	}
	if !decode(w, r, &body) {
		return
	}
	patch := store.CaptchaSitePatch{}
	if body.Name != nil {
		n := strings.TrimSpace(*body.Name)
		if n == "" || len(n) > 100 {
			writeJSONError(w, http.StatusUnprocessableEntity, "missing_fields", "name bos olamaz (en fazla 100 karakter)")
			return
		}
		patch.Name = &n
	}
	if body.Domains != nil {
		doms, err := captcha.NormalizeDomains(*body.Domains)
		if err != nil {
			writeJSONError(w, http.StatusUnprocessableEntity, "invalid_domains", err.Error())
			return
		}
		patch.Domains, patch.SetDomains = doms, true
	}
	if body.Difficulty != nil {
		if *body.Difficulty < captcha.MinDifficulty || *body.Difficulty > captcha.MaxDifficulty {
			writeJSONError(w, http.StatusUnprocessableEntity, "invalid_difficulty", "difficulty 14 ile 22 arasinda olmali")
			return
		}
		patch.Difficulty = body.Difficulty
	}
	if m := validPass(body.PassCookie); m != "" {
		writeJSONError(w, http.StatusUnprocessableEntity, "invalid_pass_cookie", m)
		return
	}
	if body.PassCookie != nil {
		patch.PassEnabled, patch.PassTTLMinutes = body.PassCookie.Enabled, body.PassCookie.TTLMinutes
	}
	site, err := cs.UpdateCaptchaSite(r.Context(), tenantID, r.PathValue("id"), patch)
	if err != nil {
		s.fail(w, err)
		return
	}
	if s.Captcha != nil {
		s.Captcha.Invalidate(site.SiteKey)
	}
	s.audit(r, "captcha_site.update", site.ID, site.Name)
	writeJSON(w, http.StatusOK, captchaOut(site))
}

func (s *Server) deleteCaptchaSite(w http.ResponseWriter, r *http.Request) {
	cs, tenantID, ok := s.captchaGate(w, r, true)
	if !ok {
		return
	}
	id := r.PathValue("id")
	cur, err := cs.GetCaptchaSite(r.Context(), tenantID, id)
	if err != nil {
		s.fail(w, err)
		return
	}
	if err := cs.DeleteCaptchaSite(r.Context(), tenantID, id); err != nil {
		s.fail(w, err)
		return
	}
	if s.Captcha != nil {
		s.Captcha.Invalidate(cur.SiteKey)
	}
	s.audit(r, "captcha_site.delete", id, cur.Name)
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) rotateCaptchaSecret(w http.ResponseWriter, r *http.Request) {
	cs, tenantID, ok := s.captchaGate(w, r, true)
	if !ok {
		return
	}
	secret := captcha.NewSecret()
	site, err := cs.RotateCaptchaSiteSecret(r.Context(), tenantID, r.PathValue("id"), captcha.HashSecret(secret))
	if err != nil {
		s.fail(w, err)
		return
	}
	s.audit(r, "captcha_site.rotate_secret", site.ID, site.Name)
	o := captchaOut(site)
	o.Secret = secret
	writeJSON(w, http.StatusOK, o)
}
