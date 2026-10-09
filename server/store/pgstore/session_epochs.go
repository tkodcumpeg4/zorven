package pgstore

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"
)

// GetLegacyMemberRole, GitHub kullanicisinin tenant_members'taki rolunu doner;
// uyelik (veya kullanici) yoksa ("", nil).
func (s *Store) GetLegacyMemberRole(ctx context.Context, tenantID, userID string) (string, error) {
	var role string
	err := s.pool.QueryRow(ctx,
		`SELECT role FROM tenant_members WHERE tenant_id = $1 AND user_id = $2`,
		tenantID, userID).Scan(&role)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", nil
	}
	return role, err
}

// GetSessionEpoch, ozne icin iptal sayacini doner (satir yoksa 0).
func (s *Store) GetSessionEpoch(ctx context.Context, subject string) (int64, error) {
	var ep int64
	err := s.pool.QueryRow(ctx,
		`SELECT epoch FROM session_epochs WHERE subject = $1`, subject).Scan(&ep)
	if errors.Is(err, pgx.ErrNoRows) {
		return 0, nil
	}
	return ep, err
}

// BumpSessionEpoch, sayaci atomik olarak artirir (yoksa 1 ile olusturur).
func (s *Store) BumpSessionEpoch(ctx context.Context, subject string) error {
	_, err := s.pool.Exec(ctx,
		`INSERT INTO session_epochs (subject, epoch) VALUES ($1, 1)
		 ON CONFLICT (subject) DO UPDATE SET epoch = session_epochs.epoch + 1, updated_at = now()`,
		subject)
	return err
}
