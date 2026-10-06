package pgstore

import (
	"context"
	"embed"
	"fmt"
	"sort"

	"github.com/jackc/pgx/v5/pgxpool"
)

//go:embed migrations/*.sql
var migrationFS embed.FS

// migrationLockKey, migration'lari siraya sokan advisory lock anahtari
// (sabit, uygulamaya ozgu rastgele bir int64).
const migrationLockKey int64 = 0x7a6f7276656e01 // "zorven" + 01

// migrate, uygulanmamis migration'lari sirayla calistirir.
//
// Harici migration araci BILEREK kullanilmiyor: tek binary dagitimi hedefimiz,
// gomulu SQL + kucuk bir runner bunu bozmuyor.
func migrate(ctx context.Context, pool *pgxpool.Pool) error {
	// Ayni veritabanina ayni anda baglanan birden cok surec (cluster'da iki dugum
	// ayni anda acilirsa; paralel testler) migration'lari ESZAMANLI calistirirsa
	// CREATE TABLE/TYPE yarisi "duplicate key ... pg_type_typname_nsp_index"
	// hatasiyla acilisi bozar. Oturum seviyesinde advisory lock ile siraya sokulur:
	// ikinci surec bekler, sonra zaten uygulanmis migration'lari atlar.
	conn, err := pool.Acquire(ctx)
	if err != nil {
		return fmt.Errorf("migration baglantisi alinamadi: %w", err)
	}
	defer conn.Release()
	if _, err := conn.Exec(ctx, `SELECT pg_advisory_lock($1)`, migrationLockKey); err != nil {
		return fmt.Errorf("migration kilidi alinamadi: %w", err)
	}
	defer conn.Exec(context.Background(), `SELECT pg_advisory_unlock($1)`, migrationLockKey) //nolint:errcheck

	if _, err := pool.Exec(ctx,
		`CREATE TABLE IF NOT EXISTS schema_migrations (
			version int PRIMARY KEY, applied_at timestamptz NOT NULL DEFAULT now())`); err != nil {
		return fmt.Errorf("schema_migrations olusturulamadi: %w", err)
	}

	entries, err := migrationFS.ReadDir("migrations")
	if err != nil {
		return err
	}
	names := make([]string, 0, len(entries))
	for _, e := range entries {
		names = append(names, e.Name())
	}
	sort.Strings(names) // 0001_, 0002_ ... dosya adi sirasi = uygulama sirasi

	for _, name := range names {
		var version int
		if _, err := fmt.Sscanf(name, "%04d_", &version); err != nil {
			return fmt.Errorf("gecersiz migration adi %q: %w", name, err)
		}

		var exists bool
		if err := pool.QueryRow(ctx,
			`SELECT EXISTS(SELECT 1 FROM schema_migrations WHERE version=$1)`, version).
			Scan(&exists); err != nil {
			return err
		}
		if exists {
			continue
		}

		sqlBytes, err := migrationFS.ReadFile("migrations/" + name)
		if err != nil {
			return err
		}
		if _, err := pool.Exec(ctx, string(sqlBytes)); err != nil {
			return fmt.Errorf("migration %s uygulanamadi: %w", name, err)
		}
		if _, err := pool.Exec(ctx,
			`INSERT INTO schema_migrations (version) VALUES ($1)`, version); err != nil {
			return err
		}
	}
	return nil
}
