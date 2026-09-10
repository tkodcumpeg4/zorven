package pgstore

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"
	"github.com/tkodcumpeg4/zorven/server/store"
)

func (s *Store) GetSetting(ctx context.Context, key string) (string, error) {
	var v string
	err := s.pool.QueryRow(ctx, `SELECT value FROM settings WHERE key = $1`, key).Scan(&v)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", store.ErrNotFound
	}
	if err != nil {
		return "", err
	}
	return v, nil
}

// SetSetting, upsert yapar: ayni anahtar yeniden yazilabilmeli
// (or. admin anahtari sifirlama, oturum gizli anahtari).
func (s *Store) SetSetting(ctx context.Context, key, value string) error {
	_, err := s.pool.Exec(ctx,
		`INSERT INTO settings (key, value) VALUES ($1,$2)
		 ON CONFLICT (key) DO UPDATE SET value = EXCLUDED.value`, key, value)
	return err
}

// DeleteSetting, olmayan anahtar icin de hatasiz doner (idempotent silme) —
// sqlitestore ile ayni davranis.
func (s *Store) DeleteSetting(ctx context.Context, key string) error {
	_, err := s.pool.Exec(ctx, `DELETE FROM settings WHERE key = $1`, key)
	return err
}
