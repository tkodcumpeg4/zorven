// Package api, yonetim REST API'sini ve kimlik dogrulamasini barindirir.
package api

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/tkodcumpeg4/zorven/server/auth"
	"github.com/tkodcumpeg4/zorven/server/entitlements"
	"github.com/tkodcumpeg4/zorven/server/obs"
	"github.com/tkodcumpeg4/zorven/server/ratelimit"
	"github.com/tkodcumpeg4/zorven/server/session"
	"github.com/tkodcumpeg4/zorven/server/store"
	"github.com/tkodcumpeg4/zorven/server/store/pgstore"
)

// isDefaultTenantOwner, kullanicinin platform (ten_default) kiracisinda owner/admin
// uyeligi olup olmadigini soyler. Platform Admin durumu BUNUNLA belirlenir: boylece
// admin "Yonetime Gec" ile baska bir org'a gecse bile yetkisini KORUR ve geri
// donebilir. Aksi halde isPlat yalnizca "aktif org == default" iken dogru oldugu
// icin, admin baska org'a gecince platform yetkisini kaybedip geri donemiyordu
// (tek-yonlu tuzak). ten_default disindaki org sahipleri platform admin OLMAZ.
func (m *Middleware) isDefaultTenantOwner(ctx context.Context, userID string) bool {
	if userID == "" || m.Store == nil {
		return false
	}
	pgSt, ok := m.Store.(*pgstore.Store)
	if !ok || pgSt.Pool() == nil {
		return false
	}
	var role string
	err := pgSt.Pool().QueryRow(ctx,
		`SELECT "role" FROM "member" WHERE "organizationId" = $1 AND "userId" = $2 LIMIT 1`,
		store.DefaultTenantID, userID).Scan(&role)
	return err == nil && (role == "owner" || role == "admin")
}

// memberTenant, platform admin OLMAYAN bir oturumun hangi kiracida ve hangi
// rolle calisacagini belirler. Oturumdaki aktif org'da gercek bir uyelik yoksa
// (ekipten cikarilmis ama oturumu o org'da kalmis; ya da hic uyeligi olmayip
// ten_default'a dusmus kullanici) o kiraciya ERISEMEZ: en eski uyeligine
// dusurulur. Hic uyeligi yoksa ok=false (istek reddedilir).
// NOT: sess onbellekteki paylasilan struct'tir; DEGISTIRILMEZ.
func (m *Middleware) memberTenant(ctx context.Context, sess *auth.SessionUser) (tenant, role string, ok bool) {
	if sess.Role != "" {
		return sess.ActiveOrganizationID, sess.Role, true
	}
	if m.Store == nil {
		return "", "", false
	}
	org, r, err := m.Store.FirstMembership(ctx, sess.UserID)
	if err != nil || org == "" {
		return "", "", false
	}
	return org, r, true
}

// unknownTenant, X-Tenant-ID ile secilen kiracinin varligini dogrular. Yoksa
// 404 yazar ve true doner; onceden yazma uclari FK hatasiyla 500 donuyordu.
// DB hatasinda istegi engellemez (asil uc hatayi zaten raporlar).
func (m *Middleware) unknownTenant(w http.ResponseWriter, r *http.Request, id string) bool {
	if m.Store == nil || id == store.DefaultTenantID {
		return false
	}
	if _, err := m.Store.GetTenant(r.Context(), id); errors.Is(err, store.ErrTenantNotFound) || errors.Is(err, store.ErrNotFound) {
		writeJSONError(w, http.StatusNotFound, "tenant_not_found", "X-Tenant-ID ile belirtilen kiraci bulunamadi")
		return true
	}
	return false
}

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
	// Anahtar stdout'a/loglara YAZILMAZ (Docker/journal'a sizardi): 0600 izinli
	// bir dosyaya yazilir, yalniz yolu loglanir. Dosya yazilamazsa anahtar DB'ye
	// kaydedilmeden hata doner (anahtar kaybolmasin).
	path, err := writeGeneratedAdminKey(full)
	if err != nil {
		return AdminKey{}, fmt.Errorf("uretilen admin anahtari dosyaya yazilamadi (ZORVEN_ADMIN_KEY ile anahtari kendiniz verebilirsiniz): %w", err)
	}
	if err := st.SetSetting(ctx, store.SettingAdminTokenID, newID); err != nil {
		return AdminKey{}, err
	}
	if err := st.SetSetting(ctx, store.SettingAdminTokenHash, newHash); err != nil {
		return AdminKey{}, err
	}
	log.Warn("yeni admin anahtari uretildi; 0600 izinli dosyaya yazildi, okuyup guvenli yere tasiyin ve dosyayi silin", "dosya", path)
	return AdminKey{TokenID: newID, Hash: newHash}, nil
}

// writeGeneratedAdminKey, uretilen admin anahtarini 0600 izinli dosyaya yazar ve
// yolunu doner. Yol: ZORVEN_ADMIN_KEY_FILE, yoksa calisma dizininde
// zorven-admin-key.txt.
func writeGeneratedAdminKey(full string) (string, error) {
	path := strings.TrimSpace(os.Getenv("ZORVEN_ADMIN_KEY_FILE"))
	if path == "" {
		path = "zorven-admin-key.txt"
	}
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o600)
	if err != nil {
		return "", err
	}
	if _, err := f.WriteString(full + "\n"); err != nil {
		f.Close()
		return "", err
	}
	if err := f.Close(); err != nil {
		return "", err
	}
	if abs, err := filepath.Abs(path); err == nil {
		path = abs
	}
	return path, nil
}

// Middleware, /api/v1/* uclarini admin anahtariyla korur.
type Middleware struct {
	Key          AdminKey
	Limiter      *ratelimit.Limiter
	Log          *slog.Logger
	Next         http.Handler
	Store        store.Store
	Entitlements entitlements.EntitlementService

	// APILimiter, programatik API token'lari icin kiraci basina hiz siniri.
	// nil ise API rate limit uygulanmaz (yalnizca X-RateLimit basligi yazilmaz).
	APILimiter *ratelimit.Limiter

	// Sessions, GitHub ile giren tarayicilarin oturum cerezini dogrular.
	// nil ise yalnizca admin anahtari kabul edilir.
	Sessions *session.Manager

	// BetterAuth, Better Auth oturumlarini Postgres uzerinden dogrular.
	BetterAuth *auth.BetterAuthVerifier

	// PublicPaths, kimlik dogrulamasi istemeyen yollar (or. /api/v1/health).
	PublicPaths map[string]bool

	// TrustedHosts, cerezli yazma isteklerinde kabul edilen Origin host'lari
	// (bkz. csrf.go TrustedOriginHosts). Ayni origin ve loopback her zaman gecer.
	TrustedHosts []string

	// PlatformDomain, Origin denetiminde uretim/gelistirme ayrimi icindir:
	// uretim domaininde loopback Origin ve http Origin reddedilir (csrf.go).
	PlatformDomain string

	// FailLimiter, argon2 gerektiren BASARISIZ kimlik denemelerini (bilinmeyen
	// token, yanlis sir) istemci IP'si basina sinirlar. Yalniz basarisizlik
	// jeton harcar; basarili istekler etkilenmez. Kota bitince istek argon2
	// calismadan 429 alir. nil ise varsayilan (failBurst / failRate) kurulur.
	FailLimiter *ratelimit.Limiter
	failOnce    sync.Once
}

// Basarisiz deneme siniri varsayilanlari: IP basina 20 deneme, sonra ~6 sn'de
// bir. Gecerli token onbellekten gectiginden normal kullanim buna takilmaz.
const (
	failBurst = 20
	failRate  = 1.0 / 6
)

func (m *Middleware) failLimiter() *ratelimit.Limiter {
	m.failOnce.Do(func() {
		if m.FailLimiter == nil {
			m.FailLimiter = ratelimit.New(failRate, failBurst)
		}
	})
	return m.FailLimiter
}

// rejectUnknown, depoda bulunmayan tokenID icin 401 yazar. Bilinmeyen tokenID
// kesin basarisizliktir: kota argon2'den ONCE harcanir, kota bittiyse sahte
// argon2 hic calismaz (429). Kota varken sahte dogrulama gecerli tokenID'li
// yolla ayni maliyeti uretir (zamanlama esitlemesi).
func (m *Middleware) rejectUnknown(w http.ResponseWriter, r *http.Request, kind, secret, code, msg string) {
	if !m.noteAuthFail(r, kind, auth.ReasonUnknown) {
		m.writeAuthLimited(w)
		return
	}
	if errors.Is(auth.VerifyDummyContext(r.Context(), secret), auth.ErrBusy) {
		m.writeAuthBusy(w, r, kind)
		return
	}
	writeJSONError(w, http.StatusUnauthorized, code, msg)
}

// verifyKnown, kaydi depodan okunmus bir token'in sirrini dogrular; gecerliyse
// true. false donerse yanit yazilmistir (401, 429 ya da 503).
//
// Sira onemli: once onbellek (argon2'siz), sonra IP basina basarisiz deneme
// siniri, en son argon2. Boylece ayni IP'deki bir sel, yakinda dogrulanmis
// gecerli istemciyi kilitlemez; onbellekte olmayan her deneme ise sinirdan gecer.
func (m *Middleware) verifyKnown(w http.ResponseWriter, r *http.Request, kind, scope, secret, hash, code, msg string) bool {
	if auth.CachedOK(scope, secret, hash) {
		return true
	}
	if m.authBlocked(w, r, kind) {
		return false
	}
	valid, err := auth.VerifyCached(r.Context(), scope, secret, hash)
	if errors.Is(err, auth.ErrBusy) {
		m.writeAuthBusy(w, r, kind)
		return false
	}
	if err != nil || !valid {
		if !m.noteAuthFail(r, kind, auth.ReasonInvalid) {
			m.writeAuthLimited(w)
			return false
		}
		writeJSONError(w, http.StatusUnauthorized, code, msg)
		return false
	}
	return true
}

// authBlocked, IP'nin basarisiz deneme kotasi bittiyse 429 yazar ve true doner.
// Jeton harcamaz; argon2'den ONCE cagrilir.
func (m *Middleware) authBlocked(w http.ResponseWriter, r *http.Request, kind string) bool {
	ip := clientIP(r)
	if m.failLimiter().Remaining(ip) >= 1 {
		return false
	}
	auth.ReportFailure(m.Log, kind, auth.ReasonRateLimited, ip)
	m.writeAuthLimited(w)
	return true
}

// noteAuthFail, argon2 gerektiren bir basarisizligi kaydeder (jeton harcar)
// ve izini birakir. Kota zaten bittiyse false doner: cagiran argon2
// calistirmadan 429 yazmalidir. Eszamanli bir seldeki fazla istekler de
// boylece argon2'ye ulasamaz (Remaining kontrolu tek basina yarisa acikti).
func (m *Middleware) noteAuthFail(r *http.Request, kind, reason string) bool {
	ip := clientIP(r)
	if !m.failLimiter().Allow(ip) {
		auth.ReportFailure(m.Log, kind, auth.ReasonRateLimited, ip)
		return false
	}
	auth.ReportFailure(m.Log, kind, reason, ip)
	return true
}

func (m *Middleware) writeAuthLimited(w http.ResponseWriter) {
	w.Header().Set("Retry-After", strconv.Itoa(int(m.failLimiter().RetryAfter().Seconds())))
	writeJSONError(w, http.StatusTooManyRequests, "rate_limited",
		"cok fazla basarisiz kimlik dogrulama denemesi, lutfen biraz sonra tekrar deneyin")
}

// transientStatus, gecici kimlik dogrulama hatalarinin (depo kapali, argon2
// kapisi dolu) durum kodu. Genelde 503. Ag oturumu ucunda 429: mevcut
// istemciler (client/wgnet) 503'u "sunucu Zorven Net sunmuyor" diye KALICI
// hata sayip duruyor; 429 + Retry-After ile geri cekilip yeniden denerler.
func transientStatus(r *http.Request) int {
	if r.URL.Path == netSessionPath {
		return http.StatusTooManyRequests
	}
	return http.StatusServiceUnavailable
}

// writeStoreUnavailable, kimlik dogrulama sirasinda depo hatasini (DB kapali,
// havuz zaman asimi) 401 yerine 503 olarak yazar: 401 istemciye "token
// gecersiz" der ve bazi istemcileri kalici durdurur. Hata ornekli loglanir.
func (m *Middleware) writeStoreUnavailable(w http.ResponseWriter, r *http.Request, kind string, err error) {
	if r.Context().Err() == nil {
		auth.ReportStoreError(m.Log, kind, clientIP(r), err)
	}
	w.Header().Set("Retry-After", "5")
	writeJSONError(w, transientStatus(r), "store_unavailable",
		"kimlik dogrulama su an yapilamiyor (depo erisilemez), lutfen tekrar deneyin")
}

// writeAuthBusy, argon2 kapisi doluyken (auth.ErrBusy) 503 yazar.
func (m *Middleware) writeAuthBusy(w http.ResponseWriter, r *http.Request, kind string) {
	auth.ReportFailure(m.Log, kind, auth.ReasonBusy, clientIP(r))
	w.Header().Set("Retry-After", "2")
	writeJSONError(w, transientStatus(r), "auth_busy",
		"sunucu yogun, kimlik dogrulama yapilamadi; lutfen tekrar deneyin")
}

// entitlementDenied, CheckFeature hatasinin gercek bir plan reddi mi (403)
// yoksa altyapi hatasi mi (503) oldugunu ayirir. Abonelik/plan kaydi yoksa
// da ret sayilir (mevcut davranis); DB hatasi "planiniz desteklemiyor"
// olarak raporlanmamali.
//
// Yalniz tip/sentinel bakilir: CheckFeature plan satiri yokken artik
// *EntitlementError doner, GetPlan ise store.ErrNotFound (P4). Eskiden
// "plan bulunamadi" METNI de eslesiyordu; metin degisirse ya da baska bir
// hata ayni metni tasirsa siniflama sessizce kayardi.
func entitlementDenied(err error) bool {
	var ee *entitlements.EntitlementError
	return errors.As(err, &ee) || errors.Is(err, store.ErrNotFound) ||
		errors.Is(err, store.ErrTenantNotFound)
}

func (m *Middleware) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("X-Frame-Options", "DENY")
	w.Header().Set("Referrer-Policy", "strict-origin-when-cross-origin")

	// Istek kimligi: ust katman (obs.InstrumentAPI) zaten yazdiysa ona dokunma;
	// yoksa gelen deger yalniz ^[A-Za-z0-9._-]{1,64}$ ise korunur, degilse uretilir.
	// Eskiden dogrulamasiz/sinirsiz yankilaniyordu (A5 #7). s.fail ve 5xx govdesi
	// kimligi bu yanit basligindan okur.
	if w.Header().Get(obs.HeaderRequestID) == "" {
		rid := r.Header.Get(obs.HeaderRequestID)
		if !obs.ValidRequestID(rid) {
			rid = newRequestID()
		}
		w.Header().Set(obs.HeaderRequestID, rid)
	}

	if m.PublicPaths[r.URL.Path] {
		// Public uclar da (health, auth/config, github login/callback, logout)
		// IP basina hiz sinirlidir: kimlik dogrulamasiz oldugundan kotuye
		// kullanima acik olmamalari icin "her yere rate limit" ilkesi.
		if m.Limiter != nil && !m.Limiter.Allow(clientIP(r)) {
			writeJSONError(w, http.StatusTooManyRequests, "rate_limited",
				"cok fazla istek, lutfen biraz sonra tekrar deneyin")
			return
		}
		if (r.URL.Path == healthPath || r.URL.Path == statusPath) && hasCredentials(r) {
			// Kimlikli saglik istegi: gecerliyse surum/istemci sayisi da doner;
			// gecersizse sessizce kimliksiz (yalniz status) yanita dusulur.
			if m.healthProbe(w, r) {
				return
			}
		}
		m.next(w, r)
		return
	}
	// Terminal ve ekran WS'leri bearer basligi tasiyamaz (tarayici WS kisiti);
	// kendi tek-kullanimlik bilet mekanizmalariyla dogrulanirlar. Yalnizca
	// /terminal veya /screen ile biten yollar muaf; bilet gecersizse handler
	// 401 dondurur / baglantiyi kapatir.
	if isTicketWSRoute(r) {
		m.next(w, r)
		return
	}

	m.serveAuthenticated(w, r)
}

// serveAuthenticated, kimlik dogrulama zinciri (oturum cerezi, API token, admin anahtari).
func (m *Middleware) serveAuthenticated(w http.ResponseWriter, r *http.Request) {
	// 0. /mcp'nin surec-ici OAuth kimligi (oauth_principal.go).
	if m.serveOAuthPrincipal(w, r) {
		return
	}

	// 1. Better Auth oturum cerezi kontrolu (HTTPS: __Host-zorven.session_token; yerel: better-auth.session_token)
	if m.BetterAuth != nil {
		token := auth.SessionCookieValue(r)
		if token != "" {
			// Better Auth cerezleri "<token>.<signature>" biciminde imzalanir;
			// veritabanindaki session.token alaninda nokta oncesindeki token saklanir.
			// Imza BETTER_AUTH_SECRET ile dogrulanir (anahtar tanimliysa zorunlu).
			rawToken := m.BetterAuth.CookieToken(token)
			if sess, err := m.BetterAuth.VerifySession(r.Context(), rawToken); err == nil && sess != nil {
				if !m.csrfCheck(w, r) {
					return
				}
				activeTenant := sess.ActiveOrganizationID
				// Platform Admin YALNIZCA platform/default kiracisinda GERCEK bir
				// owner/admin uyeligi olan kullanicidir. Onceki mantik "activeTenant
				// == default (fallback ile dahi)" veya "herhangi bir org'un owner'i"
				// olanlari da admin sayarak yeni/public kullanicilari yanlislikla
				// Platform Admin yapiyordu. (Super-admin icin admin key yolu ayridir.)
				isPlat := activeTenant == store.DefaultTenantID && (sess.Role == "owner" || sess.Role == "admin")
				if !isPlat {
					// Aktif org default degilse (or. baska bir org'a "Yonetime Gec"
					// ile gecilmisse) kullanici yine de ten_default sahibiyse
					// platform admindir; boylece impersonation geri donulebilir.
					isPlat = m.isDefaultTenantOwner(r.Context(), sess.UserID)
				}
				role := sess.Role
				if isPlat {
					if reqTenant := r.Header.Get("X-Tenant-ID"); reqTenant != "" {
						if m.unknownTenant(w, r, reqTenant) {
							return
						}
						activeTenant = reqTenant
					}
				} else {
					t, mr, ok := m.memberTenant(r.Context(), sess)
					if !ok {
						writeJSONError(w, http.StatusForbidden, "no_organization",
							"hesabiniz hicbir organizasyonun uyesi degil")
						return
					}
					if !knownRole(mr) {
						writeJSONError(w, http.StatusForbidden, "forbidden",
							"rolunuz bu islem icin yetkili degil")
						return
					}
					activeTenant, role = t, mr
				}
				ctx := withTenant(r.Context(), activeTenant)
				ctx = m.resolveProject(ctx, r, activeTenant)
				if isPlat {
					ctx = withPlatformAdmin(ctx)
				}
				ctx = withUser(ctx, &AuthUser{
					ID:       sess.UserID,
					Email:    sess.Email,
					Name:     sess.Name,
					TenantID: activeTenant,
					Role:     role,
				})
				m.next(w, r.WithContext(ctx))
				return
			}
		}
	}

	// 2. Legacy GitHub oturum cerezi kontrolu
	if m.Sessions != nil {
		if c, err := r.Cookie(session.CookieName); err == nil {
			if sess, err := m.Sessions.Verify(c.Value); err == nil {
				if !m.csrfCheck(w, r) {
					return
				}
				user, ok := m.resolveLegacySession(r.Context(), sess)
				if !ok {
					writeJSONError(w, http.StatusUnauthorized, "session_revoked",
						"oturum gecersiz veya iptal edilmis; yeniden giris yapin")
					return
				}
				ctx := withTenant(r.Context(), sess.TenantID)
				ctx = m.resolveProject(ctx, r, sess.TenantID)
				ctx = withUser(ctx, user)
				m.next(w, r.WithContext(ctx))
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
			const kind = auth.KindAPIToken
			id, secret, err := auth.Split(trimmed)
			if err != nil {
				// Bicimsiz token argon2 calistirmaz (zamanlama esitlemesi gerekmez:
				// bicim hicbir tokenID'nin varligini ele vermez); kota harcamaz.
				auth.ReportFailure(m.Log, kind, auth.ReasonMalformed, clientIP(r))
				writeJSONError(w, http.StatusUnauthorized, "invalid_token", "geçersiz api token biçimi")
				return
			}
			tok, err := m.Store.GetAPITokenByTokenID(r.Context(), id)
			if err != nil {
				if !errors.Is(err, store.ErrNotFound) {
					m.writeStoreUnavailable(w, r, kind, err)
					return
				}
				m.rejectUnknown(w, r, kind, secret, "invalid_token", "geçersiz veya iptal edilmiş api token")
				return
			}
			if tok.ExpiresAt != nil && tok.ExpiresAt.Before(time.Now().UTC()) {
				auth.ReportFailure(m.Log, kind, auth.ReasonExpired, clientIP(r))
				writeJSONError(w, http.StatusUnauthorized, "token_expired", "api token geçerlilik süresi dolmuş")
				return
			}
			// Kayit her istekte depodan okundu (iptal aninda gecerli); onbellek
			// yalniz ayni sir+hash icin argon2 adimini atlar.
			if !m.verifyKnown(w, r, kind, "api:"+tok.ID, secret, tok.TokenHash, "invalid_token", "geçersiz api token") {
				return
			}

			// Plan yetki kontrolü: FeatureAPIAccess (Pro, Team, Enterprise)
			if m.Entitlements != nil {
				if err := m.Entitlements.CheckFeature(r.Context(), tok.TenantID, entitlements.FeatureAPIAccess); err != nil {
					if !entitlementDenied(err) {
						m.writeStoreUnavailable(w, r, kind, err)
						return
					}
					writeJSONError(w, http.StatusForbidden, "feature_not_available",
						"API erişim özelliği mevcut planınızda desteklenmiyor. Lütfen planınızı yükseltin.")
					return
				}
			}

			// Son kullanım zamanını arka planda güncelle
			go func(tokID string) {
				_ = m.Store.TouchAPITokenLastUsed(context.Background(), tokID)
			}(tok.ID)

			// Yuzey kisiti: token'lar YALNIZCA public kaynaklara erisebilir
			// (mail/team/admin/terminal panel-icidir). Allowlist disi -> 403.
			if !tokenMayAccess(r.Method, r.URL.Path) {
				writeJSONError(w, http.StatusForbidden, "endpoint_not_available_for_token",
					"bu uc programatik API token'lari icin kullanilamaz; panelden erisin")
				return
			}

			// Kiraci basina API hiz siniri + X-RateLimit basliklari.
			if m.APILimiter != nil {
				w.Header().Set("X-RateLimit-Limit", strconv.Itoa(m.APILimiter.Burst()))
				if !m.APILimiter.Allow(tok.TenantID) {
					w.Header().Set("X-RateLimit-Remaining", "0")
					w.Header().Set("Retry-After", strconv.Itoa(int(m.APILimiter.RetryAfter().Seconds())))
					writeJSONError(w, http.StatusTooManyRequests, "rate_limited",
						"API hiz sinirina ulasildi, lutfen biraz sonra tekrar deneyin")
					return
				}
				w.Header().Set("X-RateLimit-Remaining", strconv.Itoa(m.APILimiter.Remaining(tok.TenantID)))
			}

			ctx := withTenant(r.Context(), tok.TenantID)
			ctx = m.resolveProject(ctx, r, tok.TenantID)
			ctx = withAPIScopes(ctx, tok.Scopes)
			if tok.UserID != nil {
				ctx = withUser(ctx, &AuthUser{
					ID:       *tok.UserID,
					TenantID: tok.TenantID,
					Role:     "api_token",
				})
			}
			m.next(w, r.WithContext(ctx))
			return
		}

		// 3a'. Istemci (ajan) token'i (zrv_live_...): YALNIZ `zorven access` ag
		// oturumu ucu (GET /api/v1/net/session). Baska hicbir uc bu token'la
		// acilmaz; plan kapisi yoktur (paylasim/erisim tum planlarda).
		if m.Store != nil && (strings.HasPrefix(trimmed, auth.PrefixClient) || strings.HasPrefix(trimmed, auth.PrefixLegacyClient)) {
			if m.serveClientNetSession(w, r, trimmed) {
				return
			}
		}

		// 3b. Better Auth oturum token'i kontrolu
		if m.BetterAuth != nil && !strings.HasPrefix(trimmed, auth.PrefixAdmin) {
			// Yalniz IMZALI deger (Better Auth bearer eklentisi); ham DB token'i degil.
			rawToken := m.BetterAuth.BearerToken(trimmed)
			if sess, err := m.BetterAuth.VerifySession(r.Context(), rawToken); err == nil && sess != nil {
				activeTenant := sess.ActiveOrganizationID
				// Platform Admin YALNIZCA platform/default kiracisinda GERCEK bir
				// owner/admin uyeligi olan kullanicidir. Onceki mantik "activeTenant
				// == default (fallback ile dahi)" veya "herhangi bir org'un owner'i"
				// olanlari da admin sayarak yeni/public kullanicilari yanlislikla
				// Platform Admin yapiyordu. (Super-admin icin admin key yolu ayridir.)
				isPlat := activeTenant == store.DefaultTenantID && (sess.Role == "owner" || sess.Role == "admin")
				if !isPlat {
					// Aktif org default degilse (or. baska bir org'a "Yonetime Gec"
					// ile gecilmisse) kullanici yine de ten_default sahibiyse
					// platform admindir; boylece impersonation geri donulebilir.
					isPlat = m.isDefaultTenantOwner(r.Context(), sess.UserID)
				}
				role := sess.Role
				if isPlat {
					if reqTenant := r.Header.Get("X-Tenant-ID"); reqTenant != "" {
						if m.unknownTenant(w, r, reqTenant) {
							return
						}
						activeTenant = reqTenant
					}
				} else {
					t, mr, ok := m.memberTenant(r.Context(), sess)
					if !ok {
						writeJSONError(w, http.StatusForbidden, "no_organization",
							"hesabiniz hicbir organizasyonun uyesi degil")
						return
					}
					if !knownRole(mr) {
						writeJSONError(w, http.StatusForbidden, "forbidden",
							"rolunuz bu islem icin yetkili degil")
						return
					}
					activeTenant, role = t, mr
				}
				ctx := withTenant(r.Context(), activeTenant)
				ctx = m.resolveProject(ctx, r, activeTenant)
				if isPlat {
					ctx = withPlatformAdmin(ctx)
				}
				ctx = withUser(ctx, &AuthUser{
					ID:       sess.UserID,
					Email:    sess.Email,
					Name:     sess.Name,
					TenantID: activeTenant,
					Role:     role,
				})
				m.next(w, r.WithContext(ctx))
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

	const kind = auth.KindAdminKey
	id, secret, err := auth.Split(strings.TrimSpace(raw))
	if err != nil {
		// Bicimsiz deger (or. "Bearer garbage", suresi dolmus oturum token'i)
		// argon2 calistirmaz: bicim hicbir tokenID'yi ele vermez. Onceden burada
		// da sahte argon2 calisiyordu (64 eszamanli istekte 2.5 GB).
		auth.ReportFailure(m.Log, kind, auth.ReasonMalformed, clientIP(r))
		writeJSONError(w, http.StatusUnauthorized, "unauthorized", "gecersiz admin anahtari")
		return
	}
	if id != m.Key.TokenID {
		// tokenID eslesmese bile sahte dogrulama yap: aksi halde gecerli
		// tokenID yanit suresinden ayirt edilebilirdi.
		m.rejectUnknown(w, r, kind, secret, "unauthorized", "gecersiz admin anahtari")
		return
	}
	if !m.verifyKnown(w, r, kind, "admin:"+id, secret, m.Key.Hash, "unauthorized", "gecersiz admin anahtari") {
		return
	}

	// Admin anahtari = PLATFORM SUPER-ADMIN.
	//
	// Yonetim uclarinda VARSAYILAN kiraci kapsaminda calisir — boylece
	// tek-sahipli kurulum, CLI ve mevcut dashboard akisi hic bozulmadan devam
	// eder. Ayrica /api/v1/admin/* uclarini acar (bkz. isPlatformAdmin).
	adminTenant := store.DefaultTenantID
	if reqTenant := r.Header.Get("X-Tenant-ID"); reqTenant != "" {
		if m.unknownTenant(w, r, reqTenant) {
			return
		}
		adminTenant = reqTenant
	}
	ctx := withPlatformAdmin(withTenant(r.Context(), adminTenant))
	ctx = m.resolveProject(ctx, r, adminTenant)
	m.next(w, r.WithContext(ctx))
}

// resolveProject, kiracı için X-Zorven-Project başlığını çözümler veya default projeye düşer (FAZ 0 / F00).
// next, kimligi dogrulanmis istegi asil isleyiciye verir. X-Zorven-Project
// basligi bilinmeyen bir proje gosteriyorsa yazma istegi reddedilir.
func (m *Middleware) next(w http.ResponseWriter, r *http.Request) {
	if bad, ok := r.Context().Value(ctxKeyProjectInvalid).(string); ok && bad != "" {
		switch r.Method {
		case http.MethodGet, http.MethodHead, http.MethodOptions:
		default:
			writeJSONError(w, http.StatusBadRequest, "project_not_found",
				"X-Zorven-Project basligindaki proje bulunamadi: "+bad)
			return
		}
	}
	m.Next.ServeHTTP(w, r)
}

func (m *Middleware) resolveProject(ctx context.Context, r *http.Request, tenantID string) context.Context {
	if m.Store == nil || tenantID == "" {
		return ctx
	}
	targetProj := strings.TrimSpace(r.Header.Get("X-Zorven-Project"))
	if targetProj != "" {
		// Önce slug olarak dene
		if p, err := m.Store.GetProjectBySlug(ctx, tenantID, targetProj); err == nil {
			return withProject(ctx, p.ID)
		}
		// Ardından ID olarak dene
		if p, err := m.Store.GetProjectByID(ctx, tenantID, targetProj); err == nil {
			return withProject(ctx, p.ID)
		}
		// Bulunamayan proje: yazma istekleri varsayilan projeye SESSIZCE
		// dusmez (yanlis projeye yazardi); m.next 400 doner. Okumalar
		// geriye uyumluluk icin varsayilana duser.
		ctx = context.WithValue(ctx, ctxKeyProjectInvalid, targetProj)
	}
	// Fallback: default proje
	if def, err := m.Store.GetDefaultProject(ctx, tenantID); err == nil {
		return withProject(ctx, def.ID)
	}
	return ctx
}

// isTicketWSRoute, yalnizca gercek WS uclarinin tam desenine uyar:
// GET /api/v1/clients/{id}/terminal ve GET /api/v1/clients/{id}/screen
// ({id} tek, bos olmayan yol parcasi).
func isTicketWSRoute(r *http.Request) bool {
	if r.Method != http.MethodGet {
		return false
	}
	rest, ok := strings.CutPrefix(r.URL.Path, "/api/v1/clients/")
	if !ok {
		return false
	}
	id, tail, ok := strings.Cut(rest, "/")
	if !ok || id == "" {
		return false
	}
	return tail == "terminal" || tail == "screen"
}

const healthPath = "/api/v1/health"

// statusPath, durum ucu; health ile ayni desen: kimlikliyse ayrintilar doner.
const statusPath = "/api/v1/status"

// hasCredentials, istegin kimlik bilgisi tasiyip tasimadigina bakar (dogrulamaz).
func hasCredentials(r *http.Request) bool {
	if strings.HasPrefix(r.Header.Get("Authorization"), "Bearer ") {
		return true
	}
	for _, c := range r.Cookies() {
		if (c.Name == auth.HostSessionCookie || c.Name == "better-auth.session_token") || c.Name == session.CookieName {
			return true
		}
	}
	return false
}

// probeWriter, healthProbe icin yaniti tamponlar.
type probeWriter struct {
	h      http.Header
	status int
	body   bytes.Buffer
}

func (p *probeWriter) Header() http.Header { return p.h }
func (p *probeWriter) WriteHeader(c int) {
	if p.status == 0 {
		p.status = c
	}
}
func (p *probeWriter) Write(b []byte) (int, error) {
	if p.status == 0 {
		p.status = http.StatusOK
	}
	return p.body.Write(b)
}

// healthProbe, kimlik dogrulama zincirini tamponlu calistirir; basariliysa
// (200) yaniti gercek yazara kopyalar ve true doner. Basarisizsa hicbir sey
// yazmaz (cagiran kimliksiz yanita duser).
func (m *Middleware) healthProbe(w http.ResponseWriter, r *http.Request) bool {
	pw := &probeWriter{h: http.Header{}}
	m.serveAuthenticated(pw, r)
	if pw.status != http.StatusOK {
		return false
	}
	for k, v := range pw.h {
		w.Header()[k] = v
	}
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(pw.body.Bytes())
	return true
}

// netSessionPath, istemci token'inin acabildigi tek uc.
const netSessionPath = "/api/v1/net/session"

// serveClientNetSession: istemci token'ini yalniz ag oturumu ucu icin dogrular.
// true donerse yanit verilmis ya da istek ilerletilmistir.
func (m *Middleware) serveClientNetSession(w http.ResponseWriter, r *http.Request, token string) bool {
	if r.Method != http.MethodGet || r.URL.Path != netSessionPath {
		writeJSONError(w, http.StatusForbidden, "endpoint_not_available_for_token",
			"istemci token'i yalnizca ag oturumu (zorven access) icin kullanilabilir")
		return true
	}
	if m.Limiter != nil && !m.Limiter.Allow(clientIP(r)) {
		writeJSONError(w, http.StatusTooManyRequests, "rate_limited",
			"cok fazla kimlik dogrulama denemesi")
		return true
	}
	const kind = auth.KindClientToken
	id, secret, err := auth.Split(token)
	if err != nil {
		auth.ReportFailure(m.Log, kind, auth.ReasonMalformed, clientIP(r))
		writeJSONError(w, http.StatusUnauthorized, "invalid_token", "gecersiz istemci token'i")
		return true
	}
	c, err := m.Store.GetClientByTokenID(r.Context(), id)
	if err != nil {
		if !errors.Is(err, store.ErrNotFound) {
			m.writeStoreUnavailable(w, r, kind, err)
			return true
		}
		m.rejectUnknown(w, r, kind, secret, "invalid_token", "gecersiz istemci token'i")
		return true
	}
	if !m.verifyKnown(w, r, kind, "client:"+c.ID, secret, c.TokenHash, "invalid_token", "gecersiz istemci token'i") {
		return true
	}
	ctx := withTenant(r.Context(), c.TenantID)
	ctx = withClientPrincipal(ctx, clientPrincipal{ClientID: c.ID, UserID: c.UserID, Name: c.Name})
	m.next(w, r.WithContext(ctx))
	return true
}
