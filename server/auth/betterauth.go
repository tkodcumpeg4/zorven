package auth

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"errors"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

var ErrSessionNotFound = errors.New("oturum bulunamadi veya suresi dolmus")

// SessionUser, Better Auth oturumundan cozulmus kullanici ve organizasyon bilgileri.
type SessionUser struct {
	SessionID            string
	UserID               string
	Email                string
	Name                 string
	ActiveOrganizationID string
	Role                 string
	ExpiresAt            time.Time
}

type cachedSession struct {
	user      *SessionUser
	expiresAt time.Time
}

// BetterAuthVerifier, Better Auth tarafindan Postgres'e yazilan oturum tablosunu
// sorgulayarak oturumlari dogrular ve kisa sureli in-memory cache tutar.
type BetterAuthVerifier struct {
	pool  *pgxpool.Pool
	cache sync.Map // token (string) -> cachedSession
	ttl   time.Duration
	// secret, Better Auth cerez imza anahtari (BETTER_AUTH_SECRET). Bos ise
	// imza dogrulanamaz: cerez eski davranisla (imzasiz token) kabul edilir,
	// Bearer ile oturum token'i HIC kabul edilmez.
	secret []byte
}

// SetSecret, Better Auth cerez imza anahtarini ayarlar (bkz. CookieToken).
func (v *BetterAuthVerifier) SetSecret(secret string) {
	v.secret = []byte(secret)
}

// HasSecret, imza dogrulamasinin etkin olup olmadigini soyler.
func (v *BetterAuthVerifier) HasSecret() bool { return len(v.secret) > 0 }

// VerifySignedValue, Better Auth'un imzali deger bicimini ("<deger>.<imza>",
// imza = HMAC-SHA256(secret, deger) standart base64, cerezde URL kodlu)
// dogrular ve imzasiz degeri doner.
func VerifySignedValue(signed string, secret []byte) (string, bool) {
	if v, err := url.PathUnescape(signed); err == nil {
		signed = v
	}
	i := strings.LastIndexByte(signed, '.')
	if i <= 0 || len(secret) == 0 {
		return "", false
	}
	val, sig := signed[:i], signed[i+1:]
	want, err := base64.StdEncoding.DecodeString(sig)
	if err != nil {
		return "", false
	}
	mac := hmac.New(sha256.New, secret)
	mac.Write([]byte(val))
	if !hmac.Equal(mac.Sum(nil), want) {
		return "", false
	}
	return val, true
}

// CookieToken, Better Auth oturum cerezinin degerinden DB session.token'ini
// cikarir. Anahtar tanimliysa imza ZORUNLU dogrulanir (gecersiz imza -> "");
// tanimli degilse (gelistirme) imzasiz token kabul edilir.
func (v *BetterAuthVerifier) CookieToken(value string) string {
	if len(v.secret) == 0 {
		if u, err := url.PathUnescape(value); err == nil {
			value = u
		}
		tok, _, _ := strings.Cut(value, ".")
		return tok
	}
	tok, _ := VerifySignedValue(value, v.secret)
	return tok
}

// BearerToken, "Authorization: Bearer <imzali-oturum-degeri>" icin token'i
// doner. HAM session.token kabul edilmez (DB'ye okuma erisimi oturum ele
// gecirmeye donusmesin); imzali deger (Better Auth bearer eklentisi) gerekir.
// Anahtar tanimli degilse "".
func (v *BetterAuthVerifier) BearerToken(value string) string {
	tok, _ := VerifySignedValue(value, v.secret)
	return tok
}

// NewBetterAuthVerifier, yeni bir BetterAuth oturum dogrulayici olusturur.
func NewBetterAuthVerifier(pool *pgxpool.Pool, ttl time.Duration) *BetterAuthVerifier {
	if ttl <= 0 {
		ttl = 30 * time.Second
	}
	return &BetterAuthVerifier{
		pool: pool,
		ttl:  ttl,
	}
}

// InvalidateUser, bir kullanicinin TUM onbellekteki oturumlarini siler
// (uyelik/rol degisince eski kiraci-rol bilgisi 30 sn boyunca kullanilmasin).
func (v *BetterAuthVerifier) InvalidateUser(userID string) {
	if userID == "" {
		return
	}
	v.cache.Range(func(key, value any) bool {
		if c, ok := value.(cachedSession); ok && c.user != nil && c.user.UserID == userID {
			v.cache.Delete(key)
		}
		return true
	})
}

// RequestSessionToken, istekteki Better Auth oturum token'ini (imzasiz kisim)
// cerezden veya Bearer basligindan cikarir. Yoksa "".
func RequestSessionToken(r *http.Request) string {
	raw := ""
	for _, name := range []string{"better-auth.session_token", "__Secure-better-auth.session_token"} {
		if c, err := r.Cookie(name); err == nil && c.Value != "" {
			raw = c.Value
			break
		}
	}
	if raw == "" {
		if h := r.Header.Get("Authorization"); strings.HasPrefix(h, "Bearer ") {
			raw = strings.TrimSpace(strings.TrimPrefix(h, "Bearer "))
		}
	}
	if v, err := url.QueryUnescape(raw); err == nil {
		raw = v
	}
	tok, _, _ := strings.Cut(raw, ".")
	return tok
}

// InvalidateAll, onbellegi tamamen temizler.
func (v *BetterAuthVerifier) InvalidateAll() {
	v.cache.Range(func(key, value any) bool {
		v.cache.Delete(key)
		return true
	})
}

// UserIDForToken, oturum token'inin kullanicisini doner (once onbellek, sonra
// DB; suresi dolmus oturumlar da sayilir). Bulunamazsa "". Auth proxy'si
// hesap degisikliklerinde kullanicinin TUM onbellekli oturumlarini temizlemek
// icin kullanir.
func (v *BetterAuthVerifier) UserIDForToken(ctx context.Context, token string) string {
	if token == "" {
		return ""
	}
	if val, ok := v.cache.Load(token); ok {
		if c, ok := val.(cachedSession); ok && c.user != nil {
			return c.user.UserID
		}
	}
	if v.pool == nil {
		return ""
	}
	var uid string
	if err := v.pool.QueryRow(ctx, `SELECT "userId" FROM "session" WHERE "token" = $1`, token).Scan(&uid); err != nil {
		return ""
	}
	return uid
}

// InvalidateSession, belirli bir token'i onbellekten siler.
func (v *BetterAuthVerifier) InvalidateSession(token string) {
	v.cache.Delete(token)
}

// VerifySession, verilen Better Auth session token'ini dogrular.
// Gecerliyse kullanici ve aktif organizasyon bilgisini doner.
func (v *BetterAuthVerifier) VerifySession(ctx context.Context, token string) (*SessionUser, error) {
	if token == "" {
		return nil, ErrSessionNotFound
	}

	// 1. In-memory cache kontrolu (DB yukunu azaltir)
	if val, ok := v.cache.Load(token); ok {
		c := val.(cachedSession)
		if time.Now().Before(c.expiresAt) && time.Now().Before(c.user.ExpiresAt) {
			return c.user, nil
		}
		v.cache.Delete(token)
	}

	if v.pool == nil {
		return nil, ErrSessionNotFound
	}

	// 2. DB'den oturum ve kullanici bilgilerini cek
	var sess SessionUser
	var activeOrg sql.NullString
	query := `
		SELECT s."id", s."userId", s."activeOrganizationId", s."expiresAt",
		       u."email", u."name"
		FROM "session" s
		JOIN "user" u ON s."userId" = u."id"
		WHERE s."token" = $1 AND s."expiresAt" > now()
	`
	err := v.pool.QueryRow(ctx, query, token).Scan(
		&sess.SessionID,
		&sess.UserID,
		&activeOrg,
		&sess.ExpiresAt,
		&sess.Email,
		&sess.Name,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrSessionNotFound
		}
		return nil, err
	}

	if activeOrg.Valid && activeOrg.String != "" {
		sess.ActiveOrganizationID = activeOrg.String
	} else {
		// Aktif organizasyon secilmemisse, kullanicinin ilk organizasyonunu aktif yap
		var firstOrg string
		err := v.pool.QueryRow(ctx,
			`SELECT "organizationId" FROM "member" WHERE "userId" = $1 ORDER BY "createdAt" ASC LIMIT 1`,
			sess.UserID,
		).Scan(&firstOrg)
		if err == nil && firstOrg != "" {
			sess.ActiveOrganizationID = firstOrg
		}
	}

	// Eger hala bir organizasyon bulunamadiysa, default kiraciya ata
	if sess.ActiveOrganizationID == "" {
		sess.ActiveOrganizationID = "ten_default"
	}

	// Rol tespiti
	if sess.ActiveOrganizationID != "" {
		var role string
		_ = v.pool.QueryRow(ctx,
			`SELECT "role" FROM "member" WHERE "organizationId" = $1 AND "userId" = $2`,
			sess.ActiveOrganizationID, sess.UserID,
		).Scan(&role)
		sess.Role = role
	}

	// 3. Cache'e yaz
	cacheUntil := time.Now().Add(v.ttl)
	if sess.ExpiresAt.Before(cacheUntil) {
		cacheUntil = sess.ExpiresAt
	}
	v.cache.Store(token, cachedSession{
		user:      &sess,
		expiresAt: cacheUntil,
	})

	return &sess, nil
}
