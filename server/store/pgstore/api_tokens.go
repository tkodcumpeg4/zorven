package pgstore

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/tkodcumpeg4/zorven/server/store"
)

const apiTokenCols = `id, tenant_id, user_id, name, token_id, token_hash, token_prefix, scopes, last_used_at, expires_at, revoked_at, created_at`

func (s *Store) CreateAPIToken(ctx context.Context, tenantID string, userID *string, name, tokenID, tokenHash, prefix string, scopes []string, expiresAt *time.Time) (store.APIToken, error) {
	if prefix == "" {
		prefix = "zrv_api_"
	}
	if len(scopes) == 0 {
		scopes = []string{"read", "write"}
	}
	tok := store.APIToken{
		ID:          newID("tok"),
		TenantID:    tenantID,
		UserID:      userID,
		Name:        name,
		TokenID:     tokenID,
		TokenHash:   tokenHash,
		TokenPrefix: prefix,
		Scopes:      scopes,
		ExpiresAt:   expiresAt,
		CreatedAt:   time.Now().UTC(),
	}

	_, err := s.pool.Exec(ctx,
		`INSERT INTO api_tokens (id, tenant_id, user_id, name, token_id, token_hash, token_prefix, scopes, expires_at, created_at)
		 VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)`,
		tok.ID, tok.TenantID, tok.UserID, tok.Name, tok.TokenID, tok.TokenHash, tok.TokenPrefix, tok.Scopes, tok.ExpiresAt, tok.CreatedAt)
	if err != nil {
		return store.APIToken{}, fmt.Errorf("api token olusturulamadi: %w", err)
	}
	return tok, nil
}

func (s *Store) ListAPITokens(ctx context.Context, tenantID string) ([]store.APIToken, error) {
	rows, err := s.pool.Query(ctx,
		`SELECT `+apiTokenCols+`
		 FROM api_tokens
		 WHERE tenant_id = $1 AND revoked_at IS NULL
		 ORDER BY created_at DESC`, tenantID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var tokens []store.APIToken
	for rows.Next() {
		var t store.APIToken
		if err := rows.Scan(
			&t.ID, &t.TenantID, &t.UserID, &t.Name, &t.TokenID, &t.TokenHash,
			&t.TokenPrefix, &t.Scopes, &t.LastUsedAt, &t.ExpiresAt, &t.RevokedAt, &t.CreatedAt,
		); err != nil {
			return nil, err
		}
		tokens = append(tokens, t)
	}
	if tokens == nil {
		tokens = []store.APIToken{}
	}
	return tokens, rows.Err()
}

func (s *Store) GetAPITokenByTokenID(ctx context.Context, tokenID string) (store.APIToken, error) {
	var t store.APIToken
	err := s.pool.QueryRow(ctx,
		`SELECT `+apiTokenCols+`
		 FROM api_tokens
		 WHERE token_id = $1 AND revoked_at IS NULL`, tokenID).
		Scan(&t.ID, &t.TenantID, &t.UserID, &t.Name, &t.TokenID, &t.TokenHash,
			&t.TokenPrefix, &t.Scopes, &t.LastUsedAt, &t.ExpiresAt, &t.RevokedAt, &t.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return store.APIToken{}, store.ErrNotFound
	}
	if err != nil {
		return store.APIToken{}, err
	}
	return t, nil
}

func (s *Store) RevokeAPIToken(ctx context.Context, tenantID, id string) error {
	now := time.Now().UTC()
	ct, err := s.pool.Exec(ctx,
		`UPDATE api_tokens
		 SET revoked_at = $1
		 WHERE id = $2 AND tenant_id = $3 AND revoked_at IS NULL`, now, id, tenantID)
	if err != nil {
		return err
	}
	if ct.RowsAffected() == 0 {
		return store.ErrNotFound
	}
	return nil
}

func (s *Store) TouchAPITokenLastUsed(ctx context.Context, id string) error {
	now := time.Now().UTC()
	_, err := s.pool.Exec(ctx,
		`UPDATE api_tokens SET last_used_at = $1 WHERE id = $2`, now, id)
	return err
}
