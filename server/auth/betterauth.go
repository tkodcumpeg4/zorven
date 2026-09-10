package auth

import (
	"context"
	"database/sql"
	"errors"
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

// InvalidateAll, onbellegi tamamen temizler.
func (v *BetterAuthVerifier) InvalidateAll() {
	v.cache.Range(func(key, value any) bool {
		v.cache.Delete(key)
		return true
	})
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
