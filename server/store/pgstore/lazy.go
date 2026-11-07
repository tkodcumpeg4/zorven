package pgstore

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// OpenLazy, Open gibi havuzu kurar ama Ping ve migration YAPMAZ: DB o an
// erisilemese de Store doner (sorgular hata verir, havuz tekrar baglanmayi dener).
// Edge dugumunun son gecerli yonlendirme goruntusuyle DB'siz acilabilmesi icindir
// (Faz 0A). Cagiran, DB donunce sema icin MigrateWhenReady'yi calistirmalidir.
func OpenLazy(ctx context.Context, dsn string) (*Store, error) {
	cfg, err := poolConfig(dsn, os.Getenv)
	if err != nil {
		return nil, fmt.Errorf("postgres DSN gecersiz: %w", err)
	}
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		return nil, fmt.Errorf("postgres havuzu acilamadi: %w", err)
	}
	return &Store{pool: pool}, nil
}

// MigrateWhenReady, DB erisilir olana dek 5 sn arayla dener, sonra migration'lari
// calistirir (advisory lock'lu, tekrar guvenli). Bloklar; goroutine olarak cagrilir.
func (s *Store) MigrateWhenReady(ctx context.Context, log *slog.Logger) {
	for {
		pctx, cancel := context.WithTimeout(ctx, 5*time.Second)
		err := s.pool.Ping(pctx)
		cancel()
		if err == nil {
			if merr := migrate(ctx, s.pool); merr != nil {
				log.Error("DB dondu ama migration calistirilamadi", "hata", merr)
			} else {
				log.Info("DB erisilir, migration'lar tamam")
				return
			}
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(5 * time.Second):
		}
	}
}

// PoolStats, pgx havuz durumu (metrikler icin): acik, bosta, kullanimda, en fazla, bekleme sayaci.
func (s *Store) PoolStats() (total, idle, acquired, max int32, emptyAcquire int64) {
	st := s.pool.Stat()
	return st.TotalConns(), st.IdleConns(), st.AcquiredConns(), st.MaxConns(), st.EmptyAcquireCount()
}
