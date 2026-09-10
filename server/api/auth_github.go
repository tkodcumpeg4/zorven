package api

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/tkodcumpeg4/zorven/server/session"
	"github.com/tkodcumpeg4/zorven/server/store"
)

// GitHub OAuth ile giris.
//
// Akis (tarayici odakli):
//   1. Kullanici "GitHub ile giris" -> GET /api/v1/auth/github/login
//   2. Sunucu CSRF "state" uretir, cerezine yazar, GitHub'a yonlendirir.
//   3. GitHub kullaniciyi geri yollar: GET /api/v1/auth/github/callback?code&state
//   4. Sunucu state'i dogrular, code'u access_token ile takas eder, kullaniciyi
//      ceker, izinli listede mi diye bakar; ise yararsa imzali oturum cerezi
//      yazip dashboard'a ("/") yonlendirir.
//
// NOT: callback URL'sini TARAYICI cagirir (sunucuya disaridan erisim gerekmez),
// bu yuzden LAN/self-signed ortamda da calisir. Yalnizca token takasi ve kullanici
// cekme sunucudan GitHub'a giden cagrilardir (internet gerekir).

const (
	githubAuthorizeURL = "https://github.com/login/oauth/authorize"
	githubTokenURL     = "https://github.com/login/oauth/access_token"
	githubUserURL      = "https://api.github.com/user"

	oauthStateCookie = "zorven_oauth_state"
	sessionTTL       = 7 * 24 * time.Hour
	stateTTL         = 10 * time.Minute
	callbackPath     = "/api/v1/auth/github/callback"
)

// githubClient, disari giden OAuth cagrilari icin kisa zaman asimli istemci.
var githubClient = &http.Client{Timeout: 15 * time.Second}

// Enabled, GitHub girisinin yapilandirilip yapilandirilmadigini soyler.
// Uc alan da (client id/secret + en az bir izinli kullanici) dolu olmali.
func (g GitHubAuth) Enabled() bool {
	if g.ClientID == "" || g.ClientSecret == "" {
		return false
	}
	// Allow listesi VEYA acik kayit gerekir. Ikisi de yoksa giris kapalidir:
	// kimsenin giremeyecegi bir sunucu, kazara herkese acilmis olandan iyidir.
	return len(g.Allow) > 0 || g.OpenSignup
}

// authConfig, dashboard'a hangi giris yontemlerinin mevcut oldugunu bildirir.
func (s *Server) authConfig(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{
		"github_enabled": s.GitHub.Enabled(),
	})
}

// redirectURI, GitHub'a verilecek callback adresini kurar. BaseURL verilmisse
// onu kullanir (birden fazla hostname varsa sabit tutmak icin), yoksa gelen
// istekten (scheme + host) turetir.
func (s *Server) redirectURI(r *http.Request) string {
	if s.GitHub.BaseURL != "" {
		return strings.TrimRight(s.GitHub.BaseURL, "/") + callbackPath
	}
	scheme := "http"
	if r.TLS != nil {
		scheme = "https"
	}
	return scheme + "://" + r.Host + callbackPath
}

func isSecure(r *http.Request) bool { return r.TLS != nil }

// githubLogin, CSRF state'i uretir ve kullaniciyi GitHub yetkilendirmesine yollar.
func (s *Server) githubLogin(w http.ResponseWriter, r *http.Request) {
	if !s.GitHub.Enabled() {
		http.Redirect(w, r, "/?auth_error=disabled", http.StatusSeeOther)
		return
	}

	// Rastgele state: callback'te cerezdekiyle eslesmeli (CSRF korumasi).
	buf := make([]byte, 16)
	if _, err := rand.Read(buf); err != nil {
		http.Redirect(w, r, "/?auth_error=server", http.StatusSeeOther)
		return
	}
	state := hex.EncodeToString(buf)

	http.SetCookie(w, &http.Cookie{
		Name:     oauthStateCookie,
		Value:    state,
		Path:     "/api/v1/auth",
		MaxAge:   int(stateTTL.Seconds()),
		HttpOnly: true,
		Secure:   isSecure(r),
		// Lax: GitHub'dan geri donen ust-duzey GET yonlendirmesinde cerez gonderilir.
		SameSite: http.SameSiteLaxMode,
	})

	q := url.Values{}
	q.Set("client_id", s.GitHub.ClientID)
	q.Set("redirect_uri", s.redirectURI(r))
	q.Set("scope", "read:user")
	q.Set("state", state)
	q.Set("allow_signup", "false")
	http.Redirect(w, r, githubAuthorizeURL+"?"+q.Encode(), http.StatusSeeOther)
}

// githubCallback, GitHub'in geri yolladigi code'u dogrular ve oturum acar.
func (s *Server) githubCallback(w http.ResponseWriter, r *http.Request) {
	if !s.GitHub.Enabled() {
		http.Redirect(w, r, "/?auth_error=disabled", http.StatusSeeOther)
		return
	}

	// 1. State dogrula (CSRF).
	stateCookie, err := r.Cookie(oauthStateCookie)
	qsState := r.URL.Query().Get("state")
	if err != nil || qsState == "" || stateCookie.Value != qsState {
		http.Redirect(w, r, "/?auth_error=state", http.StatusSeeOther)
		return
	}
	// State cerezini hemen temizle (tek kullanimlik).
	http.SetCookie(w, &http.Cookie{Name: oauthStateCookie, Path: "/api/v1/auth", MaxAge: -1})

	code := r.URL.Query().Get("code")
	if code == "" {
		http.Redirect(w, r, "/?auth_error=code", http.StatusSeeOther)
		return
	}

	// 2. code -> access_token takasi.
	token, err := s.exchangeCode(r.Context(), code, s.redirectURI(r))
	if err != nil {
		s.Logger.Warn("github token takasi basarisiz", "hata", err)
		http.Redirect(w, r, "/?auth_error=exchange", http.StatusSeeOther)
		return
	}

	// 3. Kullaniciyi cek (sayisal id + login).
	ghID, login, err := s.fetchGithubUser(r.Context(), token)
	if err != nil {
		s.Logger.Warn("github kullanici bilgisi alinamadi", "hata", err)
		http.Redirect(w, r, "/?auth_error=user", http.StatusSeeOther)
		return
	}

	// 4. Izinli mi? Allow listesi VARSA baglayicidir; yoksa acik kayit
	//    gecerlidir (Enabled() ikisinden biri olmadan girisi zaten acmaz).
	if len(s.GitHub.Allow) > 0 && !s.GitHub.Allow[strings.ToLower(login)] {
		s.Logger.Warn("izinsiz github kullanicisi giris denedi", "login", login)
		http.Redirect(w, r, "/?auth_error=forbidden", http.StatusSeeOther)
		return
	}

	// 5. Kullaniciyi kaydet ve kiracisini bul/olustur.
	u, err := s.Store.UpsertUserByGitHubID(r.Context(), ghID, login)
	if err != nil {
		s.Logger.Error("kullanici kaydedilemedi", "hata", err)
		http.Redirect(w, r, "/?auth_error=server", http.StatusSeeOther)
		return
	}
	tenant, err := s.resolveTenantForUser(r.Context(), u)
	if err != nil {
		s.Logger.Error("kiraci cozulemedi", "hata", err)
		http.Redirect(w, r, "/?auth_error=server", http.StatusSeeOther)
		return
	}

	// 6. Oturum cerezini yaz (KIRACI dahil) ve dashboard'a don.
	cookieVal, err := s.Sessions.Issue(session.Session{
		UserID: u.ID, TenantID: tenant.ID, Login: login, Method: "github",
	}, sessionTTL)
	if err != nil {
		http.Redirect(w, r, "/?auth_error=server", http.StatusSeeOther)
		return
	}
	http.SetCookie(w, &http.Cookie{
		Name:     session.CookieName,
		Value:    cookieVal,
		Path:     "/",
		MaxAge:   int(sessionTTL.Seconds()),
		HttpOnly: true,
		Secure:   isSecure(r),
		SameSite: http.SameSiteLaxMode,
	})
	s.Logger.Info("github ile giris yapildi", "login", login, "tenant", tenant.Slug)
	http.Redirect(w, r, "/", http.StatusSeeOther)
}

// logout, oturum cerezini siler.
func (s *Server) logout(w http.ResponseWriter, r *http.Request) {
	http.SetCookie(w, &http.Cookie{
		Name:     session.CookieName,
		Value:    "",
		Path:     "/",
		MaxAge:   -1,
		HttpOnly: true,
		Secure:   isSecure(r),
		SameSite: http.SameSiteLaxMode,
	})
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// exchangeCode, yetkilendirme kodunu access_token ile takas eder.
func (s *Server) exchangeCode(ctx context.Context, code, redirectURI string) (string, error) {
	form := url.Values{}
	form.Set("client_id", s.GitHub.ClientID)
	form.Set("client_secret", s.GitHub.ClientSecret)
	form.Set("code", code)
	form.Set("redirect_uri", redirectURI)

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, githubTokenURL,
		strings.NewReader(form.Encode()))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")

	resp, err := githubClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<16))
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("token ucu %d dondu", resp.StatusCode)
	}

	var out struct {
		AccessToken string `json:"access_token"`
		Error       string `json:"error"`
	}
	if err := json.Unmarshal(body, &out); err != nil {
		return "", err
	}
	if out.AccessToken == "" {
		return "", fmt.Errorf("access_token bos (%s)", out.Error)
	}
	return out.AccessToken, nil
}

// fetchGithubUser, access_token ile kullanici id'sini ve adini ceker.
//
// id NEDEN GEREKLI: kisi GitHub kullanici adini degistirebilir. Hesabi sayisal
// id ile takip etmezsek ad degisiminde YENI bir kiraci acilir ve kisi kendi
// istemci/tunellerini kaybederdi.
func (s *Server) fetchGithubUser(ctx context.Context, token string) (int64, string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, githubUserURL, nil)
	if err != nil {
		return 0, "", err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Accept", "application/vnd.github+json")

	resp, err := githubClient.Do(req)
	if err != nil {
		return 0, "", err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<16))
	if resp.StatusCode != http.StatusOK {
		return 0, "", fmt.Errorf("kullanici ucu %d dondu", resp.StatusCode)
	}

	var out struct {
		ID    int64  `json:"id"`
		Login string `json:"login"`
	}
	if err := json.Unmarshal(body, &out); err != nil {
		return 0, "", err
	}
	if out.ID == 0 || out.Login == "" {
		return 0, "", fmt.Errorf("github kullanici bilgisi eksik")
	}
	return out.ID, out.Login, nil
}

// resolveTenantForUser, kullanicinin kiracisini bulur; yoksa KISISEL bir kiraci
// acar ve kullaniciyi sahibi yapar.
//
// Slug GitHub kullanici adindan turetilir; alinmissa sonuna kisa bir ek gelir.
func (s *Server) resolveTenantForUser(ctx context.Context, u store.User) (store.Tenant, error) {
	t, err := s.Store.GetTenantForUser(ctx, u.ID)
	if err == nil {
		return t, nil
	}
	if !errors.Is(err, store.ErrTenantNotFound) {
		return store.Tenant{}, err
	}

	slug := slugify(u.GitHubLogin)
	if _, err := s.Store.GetTenantBySlug(ctx, slug); err == nil {
		slug = slug + "-" + randomHex(3) // cakisma: kisa ek
	} else if !errors.Is(err, store.ErrTenantNotFound) {
		return store.Tenant{}, err
	}

	t, err = s.Store.CreateTenant(ctx, slug)
	if err != nil {
		return store.Tenant{}, err
	}
	if err := s.Store.AddTenantMember(ctx, t.ID, u.ID, store.RoleOwner); err != nil {
		return store.Tenant{}, err
	}
	s.Logger.Info("yeni kiraci acildi", "tenant", t.ID, "slug", t.Slug, "user", u.GitHubLogin)
	return t, nil
}

// slugify, GitHub kullanici adini subdomain'e uygun bir slug'a cevirir:
// yalnizca [a-z0-9-] birakir, bas/son tireleri kirpar.
func slugify(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	var b strings.Builder
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
		case r == '-' || r == '_':
			b.WriteByte('-')
		}
	}
	out := strings.Trim(b.String(), "-")
	if out == "" {
		out = "kiraci"
	}
	if len(out) > 40 {
		out = strings.Trim(out[:40], "-")
	}
	return out
}
