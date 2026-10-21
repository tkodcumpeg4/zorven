package pgstore

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/tkodcumpeg4/zorven/server/oauthas"
)

// OAuthStore, oauthas.Store'un Postgres uygulamasi (migration 0079).
// store.Store arayuzune DOKUNMAZ: ayri kucuk bir tur.
type OAuthStore struct{ pool *pgxpool.Pool }

var _ oauthas.Store = (*OAuthStore)(nil)

// OAuth, OAuth yetkilendirme sunucusu icin kalicilik deposunu doner.
func (s *Store) OAuth() *OAuthStore { return &OAuthStore{pool: s.pool} }

// ListUserOrgs, kullanicinin uye oldugu organizasyonlar (ad + rol).
func (s *Store) ListUserOrgs(ctx context.Context, userID string) ([]oauthas.Org, error) {
	rows, err := s.pool.Query(ctx,
		`SELECT m."organizationId", coalesce(o."name", ''), m."role"
		   FROM "member" m JOIN "organization" o ON o."id" = m."organizationId"
		  WHERE m."userId" = $1`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []oauthas.Org
	for rows.Next() {
		var o oauthas.Org
		if err := rows.Scan(&o.ID, &o.Name, &o.Role); err != nil {
			return nil, err
		}
		out = append(out, o)
	}
	return out, rows.Err()
}

func (o *OAuthStore) CreateClient(ctx context.Context, c oauthas.Client) error {
	uris, _ := json.Marshal(c.RedirectURIs)
	gts, _ := json.Marshal(c.GrantTypes)
	_, err := o.pool.Exec(ctx,
		`INSERT INTO oauth_clients (client_id, client_name, redirect_uris, grant_types, created_ip, created_at)
		 VALUES ($1,$2,$3,$4,$5,$6)`, c.ID, c.Name, uris, gts, c.CreatedIP, c.CreatedAt)
	return err
}

func (o *OAuthStore) GetClient(ctx context.Context, id string) (oauthas.Client, error) {
	var c oauthas.Client
	var uris, gts []byte
	err := o.pool.QueryRow(ctx,
		`SELECT client_id, client_name, redirect_uris, grant_types, created_ip, created_at
		   FROM oauth_clients WHERE client_id = $1`, id).
		Scan(&c.ID, &c.Name, &uris, &gts, &c.CreatedIP, &c.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return oauthas.Client{}, oauthas.ErrNotFound
	}
	if err != nil {
		return oauthas.Client{}, err
	}
	_ = json.Unmarshal(uris, &c.RedirectURIs)
	_ = json.Unmarshal(gts, &c.GrantTypes)
	return c, nil
}

func (o *OAuthStore) CountClients(ctx context.Context) (int, error) {
	var n int
	err := o.pool.QueryRow(ctx, `SELECT count(*) FROM oauth_clients`).Scan(&n)
	return n, err
}

func (o *OAuthStore) CreateCode(ctx context.Context, hash string, c oauthas.Code) error {
	_, err := o.pool.Exec(ctx,
		`INSERT INTO oauth_codes (code_hash, client_id, user_id, tenant_id, redirect_uri, scope, resource, code_challenge, expires_at)
		 VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9)`,
		hash, c.ClientID, c.UserID, c.TenantID, c.RedirectURI, c.Scope, c.Resource, c.CodeChallenge, c.ExpiresAt)
	return err
}

func (o *OAuthStore) ConsumeCode(ctx context.Context, hash string, now time.Time) (oauthas.Code, error) {
	var c oauthas.Code
	// Atomik: yalniz kullanilmamis satir kullanildi isaretlenir.
	err := o.pool.QueryRow(ctx,
		`UPDATE oauth_codes SET used_at = $2
		  WHERE code_hash = $1 AND used_at IS NULL AND expires_at >= $2
		 RETURNING client_id, user_id, tenant_id, redirect_uri, scope, resource, code_challenge, expires_at`,
		hash, now).Scan(&c.ClientID, &c.UserID, &c.TenantID, &c.RedirectURI, &c.Scope, &c.Resource, &c.CodeChallenge, &c.ExpiresAt)
	if err == nil {
		return c, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return oauthas.Code{}, err
	}
	var used *time.Time
	var exp time.Time
	err = o.pool.QueryRow(ctx, `SELECT used_at, expires_at FROM oauth_codes WHERE code_hash = $1`, hash).Scan(&used, &exp)
	if errors.Is(err, pgx.ErrNoRows) || (err == nil && used == nil) {
		return oauthas.Code{}, oauthas.ErrNotFound
	}
	if err != nil {
		return oauthas.Code{}, err
	}
	return oauthas.Code{}, oauthas.ErrUsed
}

func (o *OAuthStore) CreateGrant(ctx context.Context, g oauthas.Grant) error {
	_, err := o.pool.Exec(ctx,
		`INSERT INTO oauth_grants (id, user_id, tenant_id, client_id, resource, scope, created_at)
		 VALUES ($1,$2,$3,$4,$5,$6,$7)`, g.ID, g.UserID, g.TenantID, g.ClientID, g.Resource, g.Scope, g.CreatedAt)
	return err
}

const oauthGrantCols = `g.id, g.user_id, g.tenant_id, g.client_id, c.client_name, g.resource, g.scope, g.created_at, g.last_used_at, g.revoked_at`

func scanOAuthGrant(row pgx.Row) (oauthas.Grant, error) {
	var g oauthas.Grant
	err := row.Scan(&g.ID, &g.UserID, &g.TenantID, &g.ClientID, &g.ClientName, &g.Resource, &g.Scope, &g.CreatedAt, &g.LastUsedAt, &g.RevokedAt)
	return g, err
}

func (o *OAuthStore) GetGrant(ctx context.Context, id string) (oauthas.Grant, error) {
	g, err := scanOAuthGrant(o.pool.QueryRow(ctx,
		`SELECT `+oauthGrantCols+` FROM oauth_grants g JOIN oauth_clients c ON c.client_id = g.client_id WHERE g.id = $1`, id))
	if errors.Is(err, pgx.ErrNoRows) {
		return oauthas.Grant{}, oauthas.ErrNotFound
	}
	return g, err
}

func (o *OAuthStore) TouchGrant(ctx context.Context, id string, now time.Time) error {
	_, err := o.pool.Exec(ctx, `UPDATE oauth_grants SET last_used_at = $2 WHERE id = $1`, id, now)
	return err
}

func (o *OAuthStore) RevokeGrant(ctx context.Context, id string, now time.Time) error {
	tx, err := o.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx) //nolint:errcheck
	if _, err := tx.Exec(ctx, `UPDATE oauth_grants SET revoked_at = $2 WHERE id = $1 AND revoked_at IS NULL`, id, now); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `DELETE FROM oauth_refresh_tokens WHERE grant_id = $1`, id); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (o *OAuthStore) ListGrants(ctx context.Context, userID, tenantID string) ([]oauthas.Grant, error) {
	rows, err := o.pool.Query(ctx,
		`SELECT `+oauthGrantCols+` FROM oauth_grants g JOIN oauth_clients c ON c.client_id = g.client_id
		  WHERE g.revoked_at IS NULL AND ($1 = '' OR g.user_id = $1) AND ($2 = '' OR g.tenant_id = $2)
		  ORDER BY g.created_at DESC LIMIT 500`, userID, tenantID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []oauthas.Grant
	for rows.Next() {
		g, err := scanOAuthGrant(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, g)
	}
	return out, rows.Err()
}

func (o *OAuthStore) SaveRefresh(ctx context.Context, hash, grantID string, expires time.Time) error {
	_, err := o.pool.Exec(ctx,
		`INSERT INTO oauth_refresh_tokens (token_hash, grant_id, expires_at) VALUES ($1,$2,$3)`, hash, grantID, expires)
	return err
}

func (o *OAuthStore) RotateRefresh(ctx context.Context, hash string, now time.Time) (string, error) {
	var gid string
	err := o.pool.QueryRow(ctx,
		`UPDATE oauth_refresh_tokens SET used_at = $2
		  WHERE token_hash = $1 AND used_at IS NULL AND expires_at >= $2
		 RETURNING grant_id`, hash, now).Scan(&gid)
	if err == nil {
		return gid, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return "", err
	}
	var used *time.Time
	var exp time.Time
	err = o.pool.QueryRow(ctx, `SELECT grant_id, used_at, expires_at FROM oauth_refresh_tokens WHERE token_hash = $1`, hash).
		Scan(&gid, &used, &exp)
	if errors.Is(err, pgx.ErrNoRows) || (err == nil && used == nil) {
		return "", oauthas.ErrNotFound // yok ya da suresi dolmus
	}
	if err != nil {
		return "", err
	}
	return gid, oauthas.ErrUsed
}

func (o *OAuthStore) Prune(ctx context.Context, now time.Time) error {
	stmts := []string{
		`DELETE FROM oauth_codes WHERE expires_at < $1::timestamptz - interval '10 minutes'`,
		`DELETE FROM oauth_refresh_tokens WHERE expires_at < $1`,
		`DELETE FROM oauth_grants WHERE revoked_at IS NOT NULL AND revoked_at < $1::timestamptz - interval '30 days'`,
		// Kayit olup hic grant uretmeyen (terk edilmis) istemciler.
		`DELETE FROM oauth_clients c WHERE c.created_at < $1::timestamptz - interval '7 days'
		   AND NOT EXISTS (SELECT 1 FROM oauth_grants g WHERE g.client_id = c.client_id)`,
	}
	for _, q := range stmts {
		if _, err := o.pool.Exec(ctx, q, now); err != nil {
			return err
		}
	}
	return nil
}
