package ingress

import (
	"crypto/hmac"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/tkodcumpeg4/zorven/server/accesslog"
	"github.com/tkodcumpeg4/zorven/server/auth"
	"github.com/tkodcumpeg4/zorven/server/ratelimit"
	"github.com/tkodcumpeg4/zorven/server/store"
	"github.com/tkodcumpeg4/zorven/server/visitorauth"
	"github.com/tkodcumpeg4/zorven/shared/protocol"
)

// Basic Auth ziyaretci giris formu + oturum cerezi.
//
// Tarayicilar yerel Basic Auth penceresi yerine markali bir form gorur. Form
// /_zvb/login'e POST eder; basarida host-kapsamli, HMAC imzali _zvb_session
// cerezi yazilir. curl/API istemcileri Authorization: Basic ile gecmeye devam eder.

const (
	basicCookie = "_zvb_session"
	basicTTL    = 12 * time.Hour

	// Basarisiz deneme siniri: IP + tunel basina 10 dakikada 10.
	basicFailBurst  = 10
	basicFailWindow = 10 * time.Minute

	maxLoginBody = 8 << 10
)

type basicConfig struct {
	Username     string `json:"username"`
	PasswordHash string `json:"password_hash"`
}

func parseBasicConfig(raw []byte) basicConfig {
	var c basicConfig
	_ = json.Unmarshal(raw, &c)
	return c
}

type basicClaims struct {
	Tunnel string `json:"t"`
	Host   string `json:"h"`
	FP     string `json:"fp"`
	Exp    int64  `json:"exp"`
	// IatMs, oturumun verilis zamani (unix milisaniye); web door oturum iptalinde
	// kullanilir. Eski cerezlerde yoktur: verilis = Exp - basicTTL.
	IatMs int64 `json:"iatms,omitempty"`
}

// issuedAt, oturumun verilis zamanini doner.
func (c basicClaims) issuedAt() time.Time {
	if c.IatMs > 0 {
		return time.UnixMilli(c.IatMs)
	}
	return time.Unix(c.Exp, 0).Add(-basicTTL)
}

// passFingerprint, saklanan parola hash'inin kisa parmak izi. Parola degisince
// hash degisir, boylece eski oturumlar gecersiz olur.
func passFingerprint(cfg basicConfig) string {
	sum := sha256.Sum256([]byte(cfg.Username + "\x00" + cfg.PasswordHash))
	return hex.EncodeToString(sum[:8])
}

// basicMAC, visitorauth jetonlarindan alan-ayrimli (domain-separated) HMAC.
func (h *Handler) basicMAC(body string) []byte {
	mac := hmac.New(sha256.New, h.BasicSecret)
	mac.Write([]byte("zvb-session|"))
	mac.Write([]byte(body))
	return mac.Sum(nil)
}

func (h *Handler) signBasic(c basicClaims) string {
	payload, _ := json.Marshal(c)
	body := base64.RawURLEncoding.EncodeToString(payload)
	return body + "." + base64.RawURLEncoding.EncodeToString(h.basicMAC(body))
}

func (h *Handler) verifyBasic(tok string) (basicClaims, bool) {
	var c basicClaims
	body, sig, ok := strings.Cut(tok, ".")
	if !ok || body == "" || sig == "" {
		return c, false
	}
	got, err := base64.RawURLEncoding.DecodeString(sig)
	if err != nil || subtle.ConstantTimeCompare(h.basicMAC(body), got) != 1 {
		return c, false
	}
	payload, err := base64.RawURLEncoding.DecodeString(body)
	if err != nil || json.Unmarshal(payload, &c) != nil {
		return c, false
	}
	return c, true
}

// basicFormEnabled, imza sirri varsa true. Yoksa eski davranis (yerel Basic penceresi).
func (h *Handler) basicFormEnabled() bool { return len(h.BasicSecret) > 0 }

func (h *Handler) basicCookieOK(r *http.Request, tun store.HostRoute, cfg basicConfig) bool {
	_, ok := h.basicCookieIssued(r, tun, cfg)
	return ok
}

// basicCookieIssued, gecerli Basic oturum cerezinin verilis zamanini doner.
func (h *Handler) basicCookieIssued(r *http.Request, tun store.HostRoute, cfg basicConfig) (time.Time, bool) {
	if !h.basicFormEnabled() {
		return time.Time{}, false
	}
	c, err := r.Cookie(basicCookie)
	if err != nil {
		return time.Time{}, false
	}
	claims, ok := h.verifyBasic(c.Value)
	if !ok || claims.Exp < time.Now().Unix() {
		return time.Time{}, false
	}
	if claims.Tunnel == tun.TunnelID &&
		strings.EqualFold(claims.Host, normalizeHost(r.Host)) &&
		subtle.ConstantTimeCompare([]byte(claims.FP), []byte(passFingerprint(cfg))) == 1 {
		return claims.issuedAt(), true
	}
	return time.Time{}, false
}

func (h *Handler) setBasicCookie(w http.ResponseWriter, r *http.Request, tun store.HostRoute, cfg basicConfig) {
	now := time.Now()
	exp := now.Add(basicTTL)
	http.SetCookie(w, &http.Cookie{
		Name: basicCookie,
		Value: h.signBasic(basicClaims{
			Tunnel: tun.TunnelID, Host: normalizeHost(r.Host),
			FP: passFingerprint(cfg), Exp: exp.Unix(), IatMs: now.UnixMilli(),
		}),
		Path:     "/",
		Expires:  exp,
		HttpOnly: true,
		Secure:   true,
		SameSite: http.SameSiteLaxMode,
	})
}

// checkBasicCreds, kullanici adini sabit zamanli, parolayi argon2 ile dogrular.
// Kullanici adi yanlis olsa da argon2 calisir (zamanlama farki sizmasin).
func checkBasicCreds(cfg basicConfig, user, pass string) bool {
	if cfg.Username == "" || cfg.PasswordHash == "" {
		return false
	}
	userOK := subtle.ConstantTimeCompare([]byte(user), []byte(cfg.Username)) == 1
	if !userOK {
		auth.VerifyDummy(pass)
		return false
	}
	valid, _ := auth.Verify(pass, cfg.PasswordHash)
	return valid
}

// --- basarisiz deneme siniri ------------------------------------------------

func (h *Handler) basicLimiter() *ratelimit.Limiter {
	h.basicRLOnce.Do(func() {
		if h.BasicLimiter == nil {
			h.BasicLimiter = ratelimit.New(float64(basicFailBurst)/basicFailWindow.Seconds(), basicFailBurst)
		}
	})
	return h.BasicLimiter
}

func basicLimitKey(tunnelID string, r *http.Request) string { return tunnelID + "|" + clientIP(r) }

// basicBlocked, kota bittiyse true (jeton harcamaz).
func (h *Handler) basicBlocked(key string) bool { return h.basicLimiter().Remaining(key) < 1 }

// basicFailed, basarisiz denemeyi kotadan dusurur.
func (h *Handler) basicFailed(key string) { h.basicLimiter().Allow(key) }

// --- yanitlar ---------------------------------------------------------------

// safeRDPath, yalniz ayni-host goreli yola izin verir (acik yonlendirme engeli).
func safeRDPath(rd string) string {
	rd = visitorauth.SafeRedirectPath(rd)
	if strings.HasPrefix(rd, "/_zvb/") || strings.HasPrefix(rd, "/_zva/") {
		return "/"
	}
	return rd
}

func basicPageData(r *http.Request, rd, user string, failed bool) accessPageData {
	lang := pickLang(r)
	t := pageTexts[lang]
	d := accessPageData{
		Lang: lang, Code: protocol.CodeAuthRequired, Kind: pageBasic,
		Heading: t.protectedTitle, Message: t.basicHint,
		RD: rd, User: user,
		LabelUser: t.userLabel, LabelPass: t.passLabel, SubmitText: t.submit,
	}
	if failed {
		d.LoginError = t.badCreds
	}
	return d
}

func serveLimitedPage(w http.ResponseWriter, r *http.Request) {
	lang := pickLang(r)
	t := pageTexts[lang]
	w.Header().Set("Retry-After", strconv.Itoa(int(basicFailWindow.Seconds())))
	renderAccessPage(w, r, http.StatusTooManyRequests, accessPageData{
		Lang: lang, Code: protocol.CodeRateLimited, Kind: pageLimited,
		Heading: t.limitedTitle, Message: t.limitedMsg,
	})
}

// serveBasicLimited, hiz siniri yanitini tarayiciya sayfa, digerlerine JSON/duz verir.
func serveBasicLimited(w http.ResponseWriter, r *http.Request, asPage bool) {
	if asPage {
		serveLimitedPage(w, r)
		return
	}
	w.Header().Set("Retry-After", strconv.Itoa(int(basicFailWindow.Seconds())))
	writeError(w, r, http.StatusTooManyRequests, protocol.CodeRateLimited,
		"Cok fazla basarisiz giris denemesi. Lutfen sonra tekrar deneyin.")
}

// enforceBasic, mode=basic denetimi. true = gec.
func (h *Handler) enforceBasic(w http.ResponseWriter, r *http.Request, tun store.HostRoute) bool {
	cfg := parseBasicConfig(tun.AccessConfig)
	if h.basicCookieOK(r, tun, cfg) {
		return true
	}
	failedHeader := false
	if user, pass, ok := r.BasicAuth(); ok {
		key := basicLimitKey(tun.TunnelID, r)
		if h.basicBlocked(key) {
			h.recordAccess(tun, r, accesslog.MethodBasic, "", user, false, accesslog.ReasonRateLimited)
			serveBasicLimited(w, r, wantsHTMLPage(r))
			return false
		}
		if checkBasicCreds(cfg, user, pass) {
			return true
		}
		h.basicFailed(key)
		h.recordAccess(tun, r, accesslog.MethodBasic, "", user, false, accesslog.ReasonBadCredentials)
		failedHeader = true
	}
	if h.basicFormEnabled() && wantsHTMLPage(r) {
		// Tarayici: yerel pencere yerine form. WWW-Authenticate KOYULMAZ.
		renderAccessPage(w, r, http.StatusUnauthorized,
			basicPageData(r, safeRDPath(r.URL.RequestURI()), "", failedHeader))
		return false
	}
	w.Header().Set("WWW-Authenticate", `Basic realm="Zorven"`)
	writeError(w, r, http.StatusUnauthorized, protocol.CodeAuthRequired,
		"Bu tunel parola korumalidir.")
	return false
}

// serveBasicEndpoint, tunel host'undaki /_zvb/* uclarini ele alir. Bu yollar
// ASLA istemci uygulamasina iletilmez.
func (h *Handler) serveBasicEndpoint(w http.ResponseWriter, r *http.Request, tun store.HostRoute) {
	switch r.URL.Path {
	case "/_zvb/login":
		h.handleBasicLogin(w, r, tun)
	case "/_zvb/logout":
		http.SetCookie(w, &http.Cookie{
			Name: basicCookie, Value: "", Path: "/", MaxAge: -1, Expires: time.Unix(0, 0),
			HttpOnly: true, Secure: true, SameSite: http.SameSiteLaxMode,
		})
		w.Header().Set("Cache-Control", "no-store")
		http.Redirect(w, r, safeRDPath(r.URL.Query().Get("rd")), http.StatusSeeOther)
	default:
		writeError(w, r, http.StatusNotFound, protocol.CodeTunnelNotFound, "Bulunamadi.")
	}
}

func (h *Handler) handleBasicLogin(w http.ResponseWriter, r *http.Request, tun store.HostRoute) {
	if r.Method != http.MethodPost {
		// GET /_zvb/login: formu gosteren sayfaya don.
		http.Redirect(w, r, safeRDPath(r.URL.Query().Get("rd")), http.StatusSeeOther)
		return
	}
	// Cross-site login CSRF'ine karsi: Origin varsa ayni host olmali.
	if o := r.Header.Get("Origin"); o != "" && o != "null" {
		if u, err := url.Parse(o); err != nil || !strings.EqualFold(normalizeHost(u.Host), normalizeHost(r.Host)) {
			writeError(w, r, http.StatusForbidden, protocol.CodeAccessDenied, "Gecersiz istek kaynagi.")
			return
		}
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxLoginBody)
	if err := r.ParseForm(); err != nil {
		writeError(w, r, http.StatusBadRequest, protocol.CodeAuthRequired, "Gecersiz form.")
		return
	}
	user, pass := r.PostForm.Get("user"), r.PostForm.Get("pass")
	rd := safeRDPath(r.PostForm.Get("rd"))

	// Hedef tunel: rd yolunun cozuldugu tunel (yol-tabanli yonlendirmede /_zvb/login
	// varsayilan tunele dusebilir; cerez ise rd'nin tunelini kapsamali).
	target := tun
	if u, err := url.Parse(rd); err == nil {
		if t, ok := h.Router.LookupPath(r.Host, u.Path); ok {
			target = t
		}
	}
	if !h.basicFormEnabled() || !target.Enabled || !target.AccessEnabled || target.AccessMode != "basic" {
		writeError(w, r, http.StatusNotFound, protocol.CodeTunnelNotFound, "Bulunamadi.")
		return
	}
	cfg := parseBasicConfig(target.AccessConfig)
	key := basicLimitKey(target.TunnelID, r)
	ident := truncateIdentity(user)
	if h.basicBlocked(key) {
		h.recordAccess(target, r, accesslog.MethodBasic, "", ident, false, accesslog.ReasonRateLimited)
		serveLimitedPage(w, r)
		return
	}
	if !checkBasicCreds(cfg, user, pass) {
		h.basicFailed(key)
		h.recordAccess(target, r, accesslog.MethodBasic, "", ident, false, accesslog.ReasonBadCredentials)
		renderAccessPage(w, r, http.StatusUnauthorized, basicPageData(r, rd, user, true))
		return
	}
	h.setBasicCookie(w, r, target, cfg)
	h.recordAccess(target, r, accesslog.MethodBasic, "", ident, true, accesslog.ReasonOK)
	w.Header().Set("Cache-Control", "no-store")
	http.Redirect(w, r, rd, http.StatusSeeOther)
}

func truncateIdentity(s string) string {
	if len(s) > 200 {
		s = s[:200]
	}
	return s
}

// recordAccess, erisim olayini kuyruga koyar (bloklamaz). Parola ASLA yazilmaz.
func (h *Handler) recordAccess(tun store.HostRoute, r *http.Request, method, provider, identity string, success bool, reason string) {
	if h.AccessEvents == nil {
		return
	}
	h.AccessEvents.Enqueue(accesslog.Event{
		TenantID: tun.TenantID, TunnelID: tun.TunnelID, Hostname: normalizeHost(r.Host),
		Method: method, Provider: provider, Identity: truncateIdentity(identity),
		Success: success, Reason: reason,
		ClientIP: clientIP(r), UserAgent: r.UserAgent(),
	})
}

// stripAuthCookies, Cookie basliklarindan Zorven oturum cerezlerini cikarir; bunlar
// backend uygulamasina ASLA gitmemeli.
func stripAuthCookies(vals []string) []string {
	var out []string
	for _, v := range vals {
		var keep []string
		for _, part := range strings.Split(v, ";") {
			p := strings.TrimSpace(part)
			if p == "" {
				continue
			}
			name, _, _ := strings.Cut(p, "=")
			if name == basicCookie || name == "_zva_session" {
				continue
			}
			keep = append(keep, p)
		}
		if len(keep) > 0 {
			out = append(out, strings.Join(keep, "; "))
		}
	}
	return out
}
