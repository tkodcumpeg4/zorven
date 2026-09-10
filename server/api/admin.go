// Package api, yonetim REST API'sini ve kimlik dogrulamasini barindirir.
package api

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/tkodcumpeg4/zorven/server/auth"
	"github.com/tkodcumpeg4/zorven/server/entitlements"
	"github.com/tkodcumpeg4/zorven/server/ratelimit"
	"github.com/tkodcumpeg4/zorven/server/session"
	"github.com/tkodcumpeg4/zorven/server/store"
)

// AdminKey, dogrulama icin gereken admin anahtari bilgisi.
// Tam anahtar yalnizca uretildigi anda bilinir; sonrasinda sadece hash saklanir.
type AdminKey struct {
	TokenID string
	Hash    string
}

// ResolveAdminKey, admin anahtarini belirler. Oncelik sirasi:
//
//  1. Acikca verilen anahtar (--admin-key bayragi veya ZORVEN_ADMIN_KEY)
//  2. Veritabaninda kayitli anahtar
//  3. Yeni uret, kaydet ve BIR KEZ yazdir
//
// Ucuncu durum bilincli bir tercih: kullanici hicbir sey yapilandirmadan
// sunucuyu baslatabilsin, anahtari bir kez gorsun. Anahtar DB'de yalnizca
// argon2 hash'i olarak durur; kaybedilirse yeniden uretilmelidir.
func ResolveAdminKey(ctx context.Context, st store.Store, explicit string, log *slog.Logger) (AdminKey, error) {
	if explicit != "" {
		id, secret, err := auth.Split(strings.TrimSpace(explicit))
		if err != nil {
			return AdminKey{}, fmt.Errorf("verilen admin anahtari gecersiz: %w", err)
		}
		hash, err := auth.HashSecret(secret)
		if err != nil {
			return AdminKey{}, err
		}
		log.Info("admin anahtari yapilandirmadan alindi (uretilmedi)")
		return AdminKey{TokenID: id, Hash: hash}, nil
	}

	id, errID := st.GetSetting(ctx, store.SettingAdminTokenID)
	hash, errHash := st.GetSetting(ctx, store.SettingAdminTokenHash)
	if errID == nil && errHash == nil {
		return AdminKey{TokenID: id, Hash: hash}, nil
	}
	if errID != nil && !errors.Is(errID, store.ErrNotFound) {
		return AdminKey{}, errID
	}

	full, newID, newHash, err := auth.GenerateAdmin()
	if err != nil {
		return AdminKey{}, err
	}
	if err := st.SetSetting(ctx, store.SettingAdminTokenID, newID); err != nil {
		return AdminKey{}, err
	}
	if err := st.SetSetting(ctx, store.SettingAdminTokenHash, newHash); err != nil {
		return AdminKey{}, err
	}

	// Anahtar YALNIZCA burada gorunur. Sunucu bundan sonra hash'ini bilir.
	fmt.Print("\n" +
		"  ┌──────────────────────────────────────────────────────────────────┐\n" +
		"  │  ADMIN ANAHTARI URETILDI — BU BIR KEZ GOSTERILIR                 │\n" +
		"  └──────────────────────────────────────────────────────────────────┘\n\n" +
		"  " + full + "\n\n" +
		"  Dashboard ve yonetim API'si bu anahtari kullanir:\n" +
		"    curl -H \"Authorization: Bearer " + full + "\" .../api/v1/clients\n\n" +
		"  Sunucu yalnizca argon2 hash'ini sakladi. Kaybederseniz veritabanindaki\n" +
		"  admin_token_* ayarlarini silip sunucuyu yeniden baslatin.\n\n")

	log.Warn("yeni admin anahtari uretildi — cikti kaydediliyorsa anahtar loglara girmis olabilir")
	return AdminKey{TokenID: newID, Hash: newHash}, nil
}

// Middleware, /api/v1/* uclarini admin anahtariyla korur.
type Middleware struct {
	Key          AdminKey
	Limiter      *ratelimit.Limiter
	Log          *slog.Logger
	Next         http.Handler
	Store        store.Store
	Entitlements entitlements.EntitlementService

	// Sessions, GitHub ile giren tarayicilarin oturum cerezini dogrular.
	// nil ise yalnizca admin anahtari kabul edilir.
	Sessions *session.Manager

	// BetterAuth, Better Auth oturumlarini Postgres uzerinden dogrular.
	BetterAuth *auth.BetterAuthVerifier

	// PublicPaths, kimlik dogrulamasi istemeyen yollar (or. /api/v1/health).
	PublicPaths map[string]bool
}

func (m *Middleware) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("X-Frame-Options", "DENY")
	w.Header().Set("Referrer-Policy", "strict-origin-when-cross-origin")

	if m.PublicPaths[r.URL.Path] {
		// Public uclar da (health, auth/config, github login/callback, logout)
		// IP basina hiz sinirlidir: kimlik dogrulamasiz oldugundan kotuye
		// kullanima acik olmamalari icin "her yere rate limit" ilkesi.
		if m.Limiter != nil && !m.Limiter.Allow(clientIP(r)) {
			writeJSONError(w, http.StatusTooManyRequests, "rate_limited",
				"cok fazla istek, lutfen biraz sonra tekrar deneyin")
			return
		}
		m.Next.ServeHTTP(w, r)
		return
	}
	// Terminal ve ekran WS'leri bearer basligi tasiyamaz (tarayici WS kisiti);
	// kendi tek-kullanimlik bilet mekanizmalariyla dogrulanirlar. Yalnizca
	// /terminal veya /screen ile biten yollar muaf; bilet gecersizse handler
	// 401 dondurur / baglantiyi kapatir.
	if strings.HasSuffix(r.URL.Path, "/terminal") || strings.HasSuffix(r.URL.Path, "/screen") {
		m.Next.ServeHTTP(w, r)
		return
	}
	if strings.HasPrefix(r.URL.Path, "/_cluster/") {
		m.Next.ServeHTTP(w, r)
		return
	}

	// 1. Better Auth oturum cerezi kontrolu (better-auth.session_token veya __Secure-better-auth.session_token)
	if m.BetterAuth != nil {
		token := ""
		if c, err := r.Cookie("better-auth.session_token"); err == nil && c.Value != "" {
			token = c.Value
		} else if c, err := r.Cookie("__Secure-better-auth.session_token"); err == nil && c.Value != "" {
			token = c.Value
		}
		if token != "" {
			// Better Auth cerezleri "<token>.<signature>" biciminde imzalanir;
			// veritabanindaki session.token alaninda ise nokta oncesindeki token saklanir.
			rawToken, _, _ := strings.Cut(token, ".")
			if sess, err := m.BetterAuth.VerifySession(r.Context(), rawToken); err == nil && sess != nil {
				activeTenant := sess.ActiveOrganizationID
				// Platform Admin YALNIZCA platform/default kiracisinda GERCEK bir
				// owner/admin uyeligi olan kullanicidir. Onceki mantik "activeTenant
				// == default (fallback ile dahi)" veya "herhangi bir org'un owner'i"
				// olanlari da admin sayarak yeni/public kullanicilari yanlislikla
				// Platform Admin yapiyordu. (Super-admin icin admin key yolu ayridir.)
				isPlat := activeTenant == store.DefaultTenantID && (sess.Role == "owner" || sess.Role == "admin")
				if isPlat {
					if reqTenant := r.Header.Get("X-Tenant-ID"); reqTenant != "" {
						activeTenant = reqTenant
					}
				}
				ctx := withTenant(r.Context(), activeTenant)
				if isPlat {
					ctx = withPlatformAdmin(ctx)
				}
				ctx = withUser(ctx, &AuthUser{
					ID:       sess.UserID,
					Email:    sess.Email,
					Name:     sess.Name,
					TenantID: activeTenant,
					Role:     sess.Role,
				})
				m.Next.ServeHTTP(w, r.WithContext(ctx))
				return
			}
		}
	}

	// 2. Legacy GitHub oturum cerezi kontrolu
	if m.Sessions != nil {
		if c, err := r.Cookie(session.CookieName); err == nil {
			if sess, err := m.Sessions.Verify(c.Value); err == nil {
				m.Next.ServeHTTP(w, r.WithContext(withTenant(r.Context(), sess.TenantID)))
				return
			}
		}
	}

	// 3. Authorization Bearer basligi kontrolu
	raw, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
	if ok && raw != "" {
		trimmed := strings.TrimSpace(raw)

		// 3a. Programatik REST API Tokeni kontrolu (zrv_api_...)
		if m.Store != nil && (strings.HasPrefix(trimmed, auth.PrefixAPI) || strings.HasPrefix(trimmed, "zorven_api_")) {
			id, secret, err := auth.Split(trimmed)
			if err != nil {
				writeJSONError(w, http.StatusUnauthorized, "invalid_token", "geçersiz api token biçimi")
				return
			}
			tok, err := m.Store.GetAPITokenByTokenID(r.Context(), id)
			if err != nil {
				auth.VerifyDummy(secret)
				writeJSONError(w, http.StatusUnauthorized, "invalid_token", "geçersiz veya iptal edilmiş api token")
				return
			}
			if tok.ExpiresAt != nil && tok.ExpiresAt.Before(time.Now().UTC()) {
				writeJSONError(w, http.StatusUnauthorized, "token_expired", "api token geçerlilik süresi dolmuş")
				return
			}
			valid, err := auth.Verify(secret, tok.TokenHash)
			if err != nil || !valid {
				writeJSONError(w, http.StatusUnauthorized, "invalid_token", "geçersiz api token")
				return
			}

			// Plan yetki kontrolü: FeatureAPIAccess (Pro, Team, Enterprise)
			if m.Entitlements != nil {
				if err := m.Entitlements.CheckFeature(r.Context(), tok.TenantID, entitlements.FeatureAPIAccess); err != nil {
					writeJSON(w, http.StatusForbidden, map[string]any{
						"code":  "feature_not_available",
						"error": "API erişim özelliği mevcut planınızda desteklenmiyor. Lütfen planınızı yükseltin.",
					})
					return
				}
			}

			// Son kullanım zamanını arka planda güncelle
			go func(tokID string) {
				_ = m.Store.TouchAPITokenLastUsed(context.Background(), tokID)
			}(tok.ID)

			ctx := withTenant(r.Context(), tok.TenantID)
			ctx = withAPIScopes(ctx, tok.Scopes)
			if tok.UserID != nil {
				ctx = withUser(ctx, &AuthUser{
					ID:       *tok.UserID,
					TenantID: tok.TenantID,
					Role:     "api_token",
				})
			}
			m.Next.ServeHTTP(w, r.WithContext(ctx))
			return
		}

		// 3b. Better Auth oturum token'i kontrolu
		if m.BetterAuth != nil && !strings.HasPrefix(trimmed, auth.PrefixAdmin) {
			rawToken, _, _ := strings.Cut(trimmed, ".")
			if sess, err := m.BetterAuth.VerifySession(r.Context(), rawToken); err == nil && sess != nil {
				activeTenant := sess.ActiveOrganizationID
				// Platform Admin YALNIZCA platform/default kiracisinda GERCEK bir
				// owner/admin uyeligi olan kullanicidir. Onceki mantik "activeTenant
				// == default (fallback ile dahi)" veya "herhangi bir org'un owner'i"
				// olanlari da admin sayarak yeni/public kullanicilari yanlislikla
				// Platform Admin yapiyordu. (Super-admin icin admin key yolu ayridir.)
				isPlat := activeTenant == store.DefaultTenantID && (sess.Role == "owner" || sess.Role == "admin")
				if isPlat {
					if reqTenant := r.Header.Get("X-Tenant-ID"); reqTenant != "" {
						activeTenant = reqTenant
					}
				}
				ctx := withTenant(r.Context(), activeTenant)
				if isPlat {
					ctx = withPlatformAdmin(ctx)
				}
				ctx = withUser(ctx, &AuthUser{
					ID:       sess.UserID,
					Email:    sess.Email,
					Name:     sess.Name,
					TenantID: activeTenant,
					Role:     sess.Role,
				})
				m.Next.ServeHTTP(w, r.WithContext(ctx))
				return
			}
		}
	}

	// Hiz siniri argon2'den ONCE — istemci auth'unda oldugu gibi,
	// cop anahtarlarla CPU tuketilmesini onler.
	if m.Limiter != nil && !m.Limiter.Allow(clientIP(r)) {
		writeJSONError(w, http.StatusTooManyRequests, "rate_limited",
			"cok fazla kimlik dogrulama denemesi")
		return
	}

	if !ok || raw == "" {
		writeJSONError(w, http.StatusUnauthorized, "unauthorized",
			"Authorization: Bearer <admin-key> veya gecerli oturum cerezi gerekli")
		return
	}

	id, secret, err := auth.Split(strings.TrimSpace(raw))
	if err != nil || id != m.Key.TokenID {
		// tokenID eslesmese bile sahte dogrulama yap: aksi halde gecerli
		// tokenID yanit suresinden ayirt edilebilirdi.
		auth.VerifyDummy(secret)
		writeJSONError(w, http.StatusUnauthorized, "unauthorized", "gecersiz admin anahtari")
		return
	}

	valid, err := auth.Verify(secret, m.Key.Hash)
	if err != nil || !valid {
		writeJSONError(w, http.StatusUnauthorized, "unauthorized", "gecersiz admin anahtari")
		return
	}

	// Admin anahtari = PLATFORM SUPER-ADMIN.
	//
	// Yonetim uclarinda VARSAYILAN kiraci kapsaminda calisir — boylece
	// tek-sahipli kurulum, CLI ve mevcut dashboard akisi hic bozulmadan devam
	// eder. Ayrica /api/v1/admin/* uclarini acar (bkz. isPlatformAdmin).
	adminTenant := store.DefaultTenantID
	if reqTenant := r.Header.Get("X-Tenant-ID"); reqTenant != "" {
		adminTenant = reqTenant
	}
	ctx := withPlatformAdmin(withTenant(r.Context(), adminTenant))
	m.Next.ServeHTTP(w, r.WithContext(ctx))
}
