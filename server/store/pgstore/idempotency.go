package pgstore

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/tkodcumpeg4/zorven/server/store"
)

var _ store.IdempotencyStore = (*Store)(nil)

func (s *Store) BeginIdempotency(ctx context.Context, tenantID, key string, requestSHA []byte) (store.IdempotencyRecord, bool, error) {
	tag, err := s.pool.Exec(ctx,
		`INSERT INTO idempotency_keys (tenant_id, key, request_sha) VALUES ($1, $2, $3)
		 ON CONFLICT (tenant_id, key) DO NOTHING`, tenantID, key, requestSHA)
	if err != nil {
		return store.IdempotencyRecord{}, false, err
	}
	if tag.RowsAffected() == 1 {
		return store.IdempotencyRecord{RequestSHA: requestSHA}, true, nil
	}
	// Cokmus bir surecten kalan eski "suruyor" kaydini devral.
	tag, err = s.pool.Exec(ctx,
		`UPDATE idempotency_keys SET created_at = now(), request_sha = $3
		  WHERE tenant_id = $1 AND key = $2 AND status = 0 AND created_at < $4`,
		tenantID, key, requestSHA, time.Now().Add(-store.IdempotencyStaleAfter))
	if err != nil {
		return store.IdempotencyRecord{}, false, err
	}
	if tag.RowsAffected() == 1 {
		return store.IdempotencyRecord{RequestSHA: requestSHA}, true, nil
	}
	var rec store.IdempotencyRecord
	err = s.pool.QueryRow(ctx,
		`SELECT status, COALESCE(response, ''::bytea), content_type, request_sha
		   FROM idempotency_keys WHERE tenant_id = $1 AND key = $2`, tenantID, key).
		Scan(&rec.Status, &rec.Response, &rec.ContentType, &rec.RequestSHA)
	if errors.Is(err, pgx.ErrNoRows) {
		// Arada silindi (5xx): yeniden ayirmayi dene.
		return s.BeginIdempotency(ctx, tenantID, key, requestSHA)
	}
	return rec, false, err
}

func (s *Store) CompleteIdempotency(ctx context.Context, tenantID, key string, status int, response []byte, contentType string) error {
	_, err := s.pool.Exec(ctx,
		`UPDATE idempotency_keys SET status = $3, response = $4, content_type = $5
		  WHERE tenant_id = $1 AND key = $2`, tenantID, key, status, response, contentType)
	return err
}

func (s *Store) DeleteIdempotency(ctx context.Context, tenantID, key string) error {
	_, err := s.pool.Exec(ctx, `DELETE FROM idempotency_keys WHERE tenant_id = $1 AND key = $2`, tenantID, key)
	return err
}

func (s *Store) PruneIdempotency(ctx context.Context, before time.Time) (int64, error) {
	tag, err := s.pool.Exec(ctx, `DELETE FROM idempotency_keys WHERE created_at < $1`, before)
	if err != nil {
		return 0, err
	}
	return tag.RowsAffected(), nil
}
