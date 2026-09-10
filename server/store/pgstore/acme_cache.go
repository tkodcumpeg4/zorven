package pgstore

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"golang.org/x/crypto/acme/autocert"
)

// GetACMEData, autocert.Cache Get uygulamasidir.
// Eger anahtar yoksa autocert.ErrCacheMiss doner.
func (s *Store) GetACMEData(ctx context.Context, key string) ([]byte, error) {
	var data []byte
	err := s.pool.QueryRow(ctx,
		`SELECT data FROM acme_cache WHERE key = $1`, key).Scan(&data)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, autocert.ErrCacheMiss
		}
		return nil, fmt.Errorf("acme_cache okuma hatasi: %w", err)
	}
	return data, nil
}

// PutACMEData, autocert.Cache Put uygulamasidir.
func (s *Store) PutACMEData(ctx context.Context, key string, data []byte) error {
	_, err := s.pool.Exec(ctx,
		`INSERT INTO acme_cache (key, data, updated_at)
		 VALUES ($1, $2, $3)
		 ON CONFLICT (key) DO UPDATE SET data = EXCLUDED.data, updated_at = EXCLUDED.updated_at`,
		key, data, time.Now().UTC())
	if err != nil {
		return fmt.Errorf("acme_cache yazma hatasi: %w", err)
	}
	return nil
}

// DeleteACMEData, autocert.Cache Delete uygulamasidir.
func (s *Store) DeleteACMEData(ctx context.Context, key string) error {
	_, err := s.pool.Exec(ctx, `DELETE FROM acme_cache WHERE key = $1`, key)
	if err != nil {
		return fmt.Errorf("acme_cache silme hatasi: %w", err)
	}
	return nil
}

// ACMECacheWrapper, autocert.Cache arayuzunu sarmalar.
type ACMECacheWrapper struct {
	store *Store
}

func (s *Store) AutocertCache() autocert.Cache {
	return &ACMECacheWrapper{store: s}
}

func (w *ACMECacheWrapper) Get(ctx context.Context, key string) ([]byte, error) {
	return w.store.GetACMEData(ctx, key)
}

func (w *ACMECacheWrapper) Put(ctx context.Context, key string, data []byte) error {
	return w.store.PutACMEData(ctx, key, data)
}

func (w *ACMECacheWrapper) Delete(ctx context.Context, key string) error {
	return w.store.DeleteACMEData(ctx, key)
}
