// Package visitorauth, tünel ARKASINDAKI servise erişmek isteyen dış
// ziyaretçileri (tünel son-kullanıcılarını) Google/GitHub ile doğrular.
//
// Panel oturumundan (Better Auth) tamamen bağımsızdır: panel oturumu tünel
// sahibi içindir; buradaki oturum ise tünelin allowed_emails listesindeki
// ziyaretçiler içindir.
//
// Mimari (tek kayıtlı redirect_uri ile cross-domain cookie-bounce):
//
//	1. Ziyaretçi  https://api.musteri.com/gizli  adresine gelir (mode=oauth).
//	2. ingress oturum çerezi yoksa  /_zva/start?p=google&rd=/gizli  adresine 302 atar.
//	3. /_zva/start (TÜNEL host'unda) imzalı state üretir ve sağlayıcıya yönlendirir;
//	   redirect_uri SABİT kontrol host'udur (ör. https://app.zorven.app/_zva/callback).
//	4. Sağlayıcı  app.zorven.app/_zva/callback  adresine döner. Burada code→token→email
//	   çözülür, state'teki tünel host'unun politikası (allowed_emails) denetlenir,
//	   imzalı bir "grant" üretilip  https://<tünel-host>/_zva/finish?g=...  adresine 302 atılır.
//	5. /_zva/finish (TÜNEL host'unda) grant'i doğrular, HOST-KAPSAMLI oturum çerezi
//	   (_zva_session) yazar ve rd'ye döner.
//	6. Sonraki isteklerde ingress çerezi doğrular ve allowed_emails'e göre geçirir.
//
// İmza: HMAC-SHA256, base64url(payload) + "." + base64url(mac). Sunucu tarafı
// durum tutulmaz; state ve grant kendi kendini doğrular (kısa TTL + nonce).
package visitorauth

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"
)

const (
	sessionCookie = "_zva_session"
	sessionTTL    = 12 * time.Hour
	stateTTL      = 10 * time.Minute
	grantTTL      = 2 * time.Minute
)

// Provider, bir OAuth2 sağlayıcısının uç noktalarını tanımlar.
type Provider struct {
	Name         string
	ClientID     string
	ClientSecret string
	AuthURL      string
	TokenURL     string
	UserInfoURL  string
	Scopes       string
}

// PolicyLookup, bir tünel host'unun erişim politikasını döner. Manager bunu
// callback'te allowed_emails denetimi için kullanır (Router.Lookup ile beslenir).
type PolicyLookup func(host string) (mode string, config []byte, enabled bool, ok bool)

// Manager, ziyaretçi OAuth akışını yürütür.
type Manager struct {
	secret      []byte
	controlHost string // callback host'u (kayıtlı redirect_uri), ör. "app.zorven.app"
	providers   map[string]*Provider
	lookup      PolicyLookup
	client      *http.Client
	log         *slog.Logger

	// OnEvent, giris sonucu olaylarini (basari / izinsiz e-posta) bildirir.
	// nil olabilir. Istek yolunu bloklamamalidir.
	OnEvent func(Event)
}

// Event, bir ziyaretci OAuth giris sonucudur (erisim istatistikleri icin).
type Event struct {
	Host      string // tunel host'u
	Provider  string
	Email     string
	Success   bool
	Reason    string // ok | email_not_allowed
	ClientIP  string
	UserAgent string
}

func (m *Manager) emit(r *http.Request, host, provider, email string, success bool, reason string) {
	if m == nil || m.OnEvent == nil {
		return
	}
	ip := r.RemoteAddr
	if i := strings.LastIndex(ip, ":"); i > 0 {
		ip = strings.Trim(ip[:i], "[]")
	}
	m.OnEvent(Event{Host: host, Provider: provider, Email: email, Success: success,
		Reason: reason, ClientIP: ip, UserAgent: r.UserAgent()})
}

// New, yapılandırılmış sağlayıcılarla bir Manager üretir. Hiç sağlayıcı yoksa
// (creds boş) yine de döner ama Enabled()=false olur.
func New(secret []byte, controlHost string, providers map[string]*Provider, lookup PolicyLookup, log *slog.Logger) *Manager {
	active := map[string]*Provider{}
	for name, p := range providers {
		if p != nil && p.ClientID != "" && p.ClientSecret != "" {
			active[name] = p
		}
	}
	return &Manager{
		secret:      secret,
		controlHost: strings.ToLower(strings.TrimSpace(controlHost)),
		providers:   active,
		lookup:      lookup,
		client:      &http.Client{Timeout: 15 * time.Second},
		log:         log,
	}
}

// Enabled, en az bir sağlayıcı yapılandırılmış ve imza sırrı varsa true döner.
func (m *Manager) Enabled() bool {
	return m != nil && len(m.secret) > 0 && len(m.providers) > 0
}

// ProviderNames, yapılandırılmış sağlayıcı adlarını döner (panel/hata mesajları için).
func (m *Manager) ProviderNames() []string {
	if m == nil {
		return nil
	}
	out := make([]string, 0, len(m.providers))
	for name := range m.providers {
		out = append(out, name)
	}
	sort.Strings(out)
	return out
}

// --- İmza yardımcıları ------------------------------------------------------

func (m *Manager) signToken(v any) string {
	payload, _ := json.Marshal(v)
	body := base64.RawURLEncoding.EncodeToString(payload)
	mac := hmac.New(sha256.New, m.secret)
	mac.Write([]byte(body))
	sig := base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
	return body + "." + sig
}

func (m *Manager) verifyToken(tok string, out any) bool {
	body, sig, found := strings.Cut(tok, ".")
	if !found || body == "" || sig == "" {
		return false
	}
	mac := hmac.New(sha256.New, m.secret)
	mac.Write([]byte(body))
	want := mac.Sum(nil)
	got, err := base64.RawURLEncoding.DecodeString(sig)
	if err != nil || subtle.ConstantTimeCompare(want, got) != 1 {
		return false
	}
	payload, err := base64.RawURLEncoding.DecodeString(body)
	if err != nil {
		return false
	}
	return json.Unmarshal(payload, out) == nil
}

type sessionClaims struct {
	Email string `json:"email"`
	Host  string `json:"host"`
	Exp   int64  `json:"exp"`
	// IatMs, oturumun verilis zamani (unix milisaniye). Kapi (web door) oturum
	// iptalinde kullanilir. Eski cerezlerde yoktur: verilis = Exp - sessionTTL.
	IatMs int64 `json:"iatms,omitempty"`
}

// issuedAt, oturumun verilis zamanini doner (IatMs yoksa Exp - TTL).
func (c sessionClaims) issuedAt() time.Time {
	if c.IatMs > 0 {
		return time.UnixMilli(c.IatMs)
	}
	return time.Unix(c.Exp, 0).Add(-sessionTTL)
}

func newSessionClaims(email, host string) sessionClaims {
	now := time.Now()
	return sessionClaims{Email: email, Host: host, Exp: now.Add(sessionTTL).Unix(), IatMs: now.UnixMilli()}
}

type stateClaims struct {
	Host     string `json:"host"`
	Provider string `json:"p"`
	RD       string `json:"rd"`
	Exp      int64  `json:"exp"`
}

type grantClaims struct {
	Provider string `json:"p,omitempty"`
	// Allowed=false: e-posta doğrulandı ama izin listesinde değil. Oturum yine de
	// kurulur ki ziyaretçi tünel host'unda markalı "erişim reddedildi" sayfasını
	// görüp farklı hesapla giriş yapabilsin; ingress her istekte izin listesini
	// yeniden denetler, yani bu oturum erişim SAĞLAMAZ.
	Allowed bool   `json:"ok"`
	Email   string `json:"email"`
	Host    string `json:"host"`
	RD      string `json:"rd"`
	Exp     int64  `json:"exp"`
}

// --- Oturum denetimi (ingress hot-path) -------------------------------------

// SessionEmail, isteğin geçerli bir ziyaretçi oturum çerezi taşıyıp taşımadığını
// döner. Çerez host'a bağlıdır (başka tünelde yeniden kullanılamaz).
func (m *Manager) SessionEmail(r *http.Request) (string, bool) {
	email, _, ok := m.SessionInfo(r)
	return email, ok
}

// SessionInfo, SessionEmail gibi gecerli oturumun e-postasini ve VERILIS zamanini
// doner. Web door, grant iptalinden once verilmis oturumlari gecersiz saymak icin
// kullanir.
func (m *Manager) SessionInfo(r *http.Request) (email string, issued time.Time, ok bool) {
	c, err := r.Cookie(sessionCookie)
	if err != nil {
		return "", time.Time{}, false
	}
	var claims sessionClaims
	if !m.verifyToken(c.Value, &claims) {
		return "", time.Time{}, false
	}
	if claims.Exp < time.Now().Unix() {
		return "", time.Time{}, false
	}
	if !strings.EqualFold(claims.Host, hostOnly(r.Host)) {
		return "", time.Time{}, false
	}
	return claims.Email, claims.issuedAt(), true
}

// IssueSessionValueAt, IssueSessionValue gibi ama verilis zamani verilir (testler).
func (m *Manager) IssueSessionValueAt(host, email string, at time.Time) string {
	return m.signToken(sessionClaims{Email: email, Host: strings.ToLower(host), Exp: at.Add(sessionTTL).Unix(), IatMs: at.UnixMilli()})
}

// IssueSessionValue, host için imzalı bir _zva_session çerez DEĞERİ üretir
// (testler ve araçlar için; normal akış /_zva/finish üzerinden ilerler).
func (m *Manager) IssueSessionValue(host, email string) string {
	return m.signToken(newSessionClaims(email, strings.ToLower(host)))
}

// EmailAllowed, e-postanın izin listesine göre geçip geçmediğini döner. Liste
// boşsa "doğrulanmış herkes" kabul edilir (yalnızca oturum açmış olmak yeter).
func EmailAllowed(email string, allowed []string) bool {
	if len(allowed) == 0 {
		return true
	}
	email = strings.ToLower(strings.TrimSpace(email))
	for _, a := range allowed {
		if strings.ToLower(strings.TrimSpace(a)) == email {
			return true
		}
	}
	return false
}

// --- Tünel host uçları: /_zva/start, /_zva/finish --------------------------

// HandlesPath, verilen yolun bu Manager tarafından ele alınan bir ziyaretçi-auth
// ucu olup olmadığını söyler (ingress interception için).
func HandlesPath(path string) bool {
	return strings.HasPrefix(path, "/_zva/")
}

// ServeTunnelEndpoint, tünel host'undaki /_zva/start ve /_zva/finish uçlarını
// ele alır. Bu uçlar erişim denetiminden MUAFTIR (kimlik doğrulamanın kendisidir).
func (m *Manager) ServeTunnelEndpoint(w http.ResponseWriter, r *http.Request) {
	switch r.URL.Path {
	case "/_zva/start":
		m.handleStart(w, r)
	case "/_zva/finish":
		m.handleFinish(w, r)
	case "/_zva/logout":
		m.handleLogout(w, r)
	default:
		http.NotFound(w, r)
	}
}

// StartURL, ziyaretçiyi giriş akışına sokan yerel yolu üretir (ingress bunu
// oturum yokken 302 hedefi olarak kullanır).
func StartURL(providers []string, rd string) string {
	p := "google"
	if len(providers) > 0 && providers[0] != "" {
		p = providers[0]
	}
	q := url.Values{}
	q.Set("p", p)
	if rd != "" {
		q.Set("rd", rd)
	}
	return "/_zva/start?" + q.Encode()
}

func (m *Manager) handleStart(w http.ResponseWriter, r *http.Request) {
	pName := r.URL.Query().Get("p")
	if pName == "" {
		pName = "google"
	}
	prov, ok := m.providers[pName]
	if !ok {
		http.Error(w, "OAuth sağlayıcı yapılandırılmamış: "+pName, http.StatusBadGateway)
		return
	}
	rd := safeRD(r.URL.Query().Get("rd"))
	host := hostOnly(r.Host)

	state := m.signToken(stateClaims{
		Host: host, Provider: pName, RD: rd, Exp: time.Now().Add(stateTTL).Unix(),
	})

	q := url.Values{}
	q.Set("client_id", prov.ClientID)
	q.Set("redirect_uri", m.callbackURL())
	q.Set("response_type", "code")
	q.Set("scope", prov.Scopes)
	q.Set("state", state)
	if pName == "google" {
		q.Set("access_type", "online")
		q.Set("prompt", "select_account")
	}
	http.Redirect(w, r, prov.AuthURL+"?"+q.Encode(), http.StatusFound)
}

// handleLogout, ziyaretçi oturum çerezini siler ve rd'ye (güvenli yol) döner.
// Giriş sayfasındaki "farklı hesapla giriş" bağlantısı için kullanılır.
func (m *Manager) handleLogout(w http.ResponseWriter, r *http.Request) {
	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookie,
		Value:    "",
		Path:     "/",
		MaxAge:   -1,
		Expires:  time.Unix(0, 0),
		HttpOnly: true,
		Secure:   true,
		SameSite: http.SameSiteLaxMode,
	})
	w.Header().Set("Cache-Control", "no-store")
	http.Redirect(w, r, safeRD(r.URL.Query().Get("rd")), http.StatusSeeOther)
}

func (m *Manager) handleFinish(w http.ResponseWriter, r *http.Request) {
	var g grantClaims
	if !m.verifyToken(r.URL.Query().Get("g"), &g) || g.Exp < time.Now().Unix() {
		http.Error(w, "Geçersiz veya süresi dolmuş oturum jetonu.", http.StatusBadRequest)
		return
	}
	if !strings.EqualFold(g.Host, hostOnly(r.Host)) {
		http.Error(w, "Oturum jetonu bu host için geçerli değil.", http.StatusBadRequest)
		return
	}
	sess := m.signToken(newSessionClaims(g.Email, g.Host))
	// Yalnızca izinli e-postalar "ok" sayılır; izinsiz olan callback'te zaten
	// email_not_allowed olarak kaydedildi (bkz. HandleCallback).
	if g.Allowed {
		m.emit(r, g.Host, g.Provider, g.Email, true, "ok")
	}
	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookie,
		Value:    sess,
		Path:     "/",
		Expires:  time.Now().Add(sessionTTL),
		HttpOnly: true,
		Secure:   true,
		SameSite: http.SameSiteLaxMode,
	})
	http.Redirect(w, r, safeRD(g.RD), http.StatusFound)
}

// --- Kontrol host ucu: /_zva/callback --------------------------------------

// HandleCallback, sağlayıcının döndüğü SABİT kontrol-host ucudur. code→email
// çözer, tünel politikasını denetler, imzalı grant üretip tünel host'una döner.
func (m *Manager) HandleCallback(w http.ResponseWriter, r *http.Request) {
	if errParam := r.URL.Query().Get("error"); errParam != "" {
		http.Error(w, "OAuth sağlayıcı hatası: "+errParam, http.StatusForbidden)
		return
	}
	var st stateClaims
	if !m.verifyToken(r.URL.Query().Get("state"), &st) || st.Exp < time.Now().Unix() {
		http.Error(w, "Geçersiz veya süresi dolmuş state.", http.StatusBadRequest)
		return
	}
	prov, ok := m.providers[st.Provider]
	if !ok {
		http.Error(w, "Bilinmeyen sağlayıcı.", http.StatusBadRequest)
		return
	}
	code := r.URL.Query().Get("code")
	if code == "" {
		http.Error(w, "Yetkilendirme kodu yok.", http.StatusBadRequest)
		return
	}

	email, err := m.exchange(r.Context(), prov, code)
	if err != nil {
		m.log.Warn("ziyaretçi OAuth token değişimi başarısız", "provider", prov.Name, "hata", err)
		http.Error(w, "Kimlik doğrulanamadı.", http.StatusBadGateway)
		return
	}

	// Tünel politikasını doğrula: mode hâlâ oauth ve etkin mi, e-posta izinli mi.
	mode, cfg, enabled, found := m.lookup(st.Host)
	if !found || !enabled || mode != "oauth" {
		http.Error(w, "Bu host artık OAuth ile korunmuyor.", http.StatusForbidden)
		return
	}
	var pc struct {
		AllowedEmails []string `json:"allowed_emails"`
	}
	_ = json.Unmarshal(cfg, &pc)
	allowed := EmailAllowed(email, pc.AllowedEmails)
	if !allowed {
		if m.log != nil {
			m.log.Info("ziyaretçi reddedildi (izin listesinde yok)", "email", email, "host", st.Host)
		}
		m.emit(r, st.Host, st.Provider, email, false, "email_not_allowed")
	}

	grant := m.signToken(grantClaims{
		Provider: st.Provider, Allowed: allowed,
		Email: email, Host: st.Host, RD: st.RD, Exp: time.Now().Add(grantTTL).Unix(),
	})
	dest := "https://" + st.Host + "/_zva/finish?g=" + url.QueryEscape(grant)
	http.Redirect(w, r, dest, http.StatusFound)
}

// exchange, yetkilendirme kodunu access token'a çevirir ve kullanıcının
// e-postasını döner.
func (m *Manager) exchange(ctx context.Context, prov *Provider, code string) (string, error) {
	form := url.Values{}
	form.Set("client_id", prov.ClientID)
	form.Set("client_secret", prov.ClientSecret)
	form.Set("code", code)
	form.Set("redirect_uri", m.callbackURL())
	form.Set("grant_type", "authorization_code")

	req, _ := http.NewRequestWithContext(ctx, http.MethodPost, prov.TokenURL, strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")

	resp, err := m.client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("token ucu %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	var tok struct {
		AccessToken string `json:"access_token"`
	}
	if err := json.Unmarshal(body, &tok); err != nil || tok.AccessToken == "" {
		return "", fmt.Errorf("access_token çözülemedi")
	}
	return m.fetchEmail(ctx, prov, tok.AccessToken)
}

func (m *Manager) fetchEmail(ctx context.Context, prov *Provider, accessToken string) (string, error) {
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, prov.UserInfoURL, nil)
	req.Header.Set("Authorization", "Bearer "+accessToken)
	req.Header.Set("Accept", "application/json")
	resp, err := m.client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("userinfo %d", resp.StatusCode)
	}
	var info struct {
		Email         string `json:"email"`
		VerifiedEmail *bool  `json:"verified_email"`
		EmailVerified *bool  `json:"email_verified"`
	}
	_ = json.Unmarshal(body, &info)
	if info.Email != "" {
		return strings.ToLower(info.Email), nil
	}
	// GitHub: birincil e-posta /user/emails ucundadır.
	if prov.Name == "github" {
		return m.fetchGitHubEmail(ctx, accessToken)
	}
	return "", fmt.Errorf("e-posta bulunamadı")
}

func (m *Manager) fetchGitHubEmail(ctx context.Context, accessToken string) (string, error) {
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, "https://api.github.com/user/emails", nil)
	req.Header.Set("Authorization", "Bearer "+accessToken)
	req.Header.Set("Accept", "application/vnd.github+json")
	resp, err := m.client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	var emails []struct {
		Email    string `json:"email"`
		Primary  bool   `json:"primary"`
		Verified bool   `json:"verified"`
	}
	if err := json.Unmarshal(body, &emails); err != nil {
		return "", err
	}
	for _, e := range emails {
		if e.Primary && e.Verified {
			return strings.ToLower(e.Email), nil
		}
	}
	for _, e := range emails {
		if e.Verified {
			return strings.ToLower(e.Email), nil
		}
	}
	return "", fmt.Errorf("doğrulanmış GitHub e-postası yok")
}

// --- yardımcılar ------------------------------------------------------------

func (m *Manager) callbackURL() string {
	return "https://" + m.controlHost + "/_zva/callback"
}

// hostOnly, "host:port" ise portu atar.
func hostOnly(h string) string {
	if i := strings.LastIndex(h, ":"); i > 0 && !strings.Contains(h[i:], "]") {
		h = h[:i]
	}
	return strings.ToLower(h)
}

// safeRD, açık yönlendirmeyi (open redirect) engeller: yalnızca aynı host'ta
// mutlak yol (/...) kabul edilir; aksi halde köke düşer.
func safeRD(rd string) string {
	if rd == "" || !strings.HasPrefix(rd, "/") || strings.HasPrefix(rd, "//") {
		return "/"
	}
	return rd
}
