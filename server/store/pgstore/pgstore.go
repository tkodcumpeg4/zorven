// Package pgstore, store.Store arayuzunun Postgres implementasyonudur.
//
// Surucu: github.com/jackc/pgx/v5 — SAF GO, cgo yok. Bu, sqlitestore'daki
// modernc.org/sqlite tercihiyle ayni gerekceye dayanir: tek binary dagitimi ve
// cross-compile bozulmamali.
//
// Bu asamada TENANCY YOK: arayuz sqlitestore ile birebir ayni sozlesmeyi
// karsilar, davranis degismez. Kiracilik ayri bir fazda eklenecek.
package pgstore

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"os"
	"strconv"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/tkodcumpeg4/zorven/server/internal/envnum"
	"github.com/tkodcumpeg4/zorven/server/store"
)

type Store struct {
	pool *pgxpool.Pool
}

// Arayuz uyumu DERLEME ZAMANINDA kanitlanir: bir metot eksik kalirsa
// veya imza kayarsa build kirilir, testlerin kosmasini beklemeye gerek kalmaz.
var _ store.Store = (*Store)(nil)

// Ping, veritabani baglantisinin canli olup olmadigini kontrol eder (status page).
func (s *Store) Ping(ctx context.Context) error {
	return s.pool.Ping(ctx)
}

// Open, baglanti havuzunu acar ve bekleyen migration'lari uygular.
func Open(ctx context.Context, dsn string) (*Store, error) {
	return open(ctx, dsn, true)
}

// OpenNoMigrate, migration CALISTIRMADAN baglanir (edge rolu: sema degisikligi
// yalniz birincil dugumde uygulanir).
func OpenNoMigrate(ctx context.Context, dsn string) (*Store, error) {
	return open(ctx, dsn, false)
}

func open(ctx context.Context, dsn string, runMigrations bool) (*Store, error) {
	cfg, err := poolConfig(dsn, os.Getenv)
	if err != nil {
		return nil, fmt.Errorf("postgres DSN gecersiz: %w", err)
	}
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		return nil, fmt.Errorf("postgres havuzu acilamadi: %w", err)
	}
	// Ping: DSN sozdizimsel olarak dogru ama sunucu erisilemez olabilir;
	// hatayi burada verip yariyolda kalmis bir sunucu baslatmayi onleriz.
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("postgres'e baglanilamadi: %w", err)
	}
	if runMigrations {
		if err := migrate(ctx, pool); err != nil {
			pool.Close()
			return nil, err
		}
	}
	return &Store{pool: pool}, nil
}

// Havuz varsayilanlari. pgxpool'un kendi varsayilani max(4, NumCPU) 2 cekirdekli
// VDS'te 4 baglantidir; yavas bir sorgu tum paneli/ingress API'yi bloke ederdi.
const (
	defaultMaxConns         = 20
	defaultStatementTimeout = 15000 // ms
	idleInTxTimeoutMS       = 30000
	// longOpTimeout, retention/backfill gibi bilincli uzun islemlerin tavani.
	longOpTimeout = 10 * time.Minute
)

// poolConfig, DSN + ortam degiskenlerinden havuz ayarini uretir:
// ZORVEN_DB_MAX_CONNS (vars. 20), ZORVEN_DB_STATEMENT_TIMEOUT_MS (vars. 15000; 0 = kapali).
func poolConfig(dsn string, get func(string) string) (*pgxpool.Config, error) {
	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		return nil, err
	}
	// Ust sinir: Postgres max_connections (vars. 100) diger servislerle paylasiliyor;
	// yazim hatasiyla dev bir havuz baglanti tukenmesine yol acmasin.
	cfg.MaxConns = int32(envnum.Int(get, "ZORVEN_DB_MAX_CONNS", defaultMaxConns, 1, 500))
	cfg.MinConns = 2
	if cfg.MinConns > cfg.MaxConns {
		cfg.MinConns = cfg.MaxConns
	}
	cfg.MaxConnIdleTime = 5 * time.Minute
	cfg.MaxConnLifetime = time.Hour
	cfg.HealthCheckPeriod = 30 * time.Second
	cfg.ConnConfig.ConnectTimeout = 5 * time.Second
	if cfg.ConnConfig.RuntimeParams == nil {
		cfg.ConnConfig.RuntimeParams = map[string]string{}
	}
	rp := cfg.ConnConfig.RuntimeParams
	// 0 = kapali; ust sinir 1 saat (longOpTimeout'tan uzun tek ifade beklenmez).
	rp["statement_timeout"] = strconv.Itoa(envnum.Int(get, "ZORVEN_DB_STATEMENT_TIMEOUT_MS", defaultStatementTimeout, 0, 3600000))
	rp["idle_in_transaction_session_timeout"] = strconv.Itoa(idleInTxTimeoutMS)
	rp["application_name"] = "zorven"
	// O4: JIT kapali. Uygulamanin sorgulari kisa OLTP sorgulari; kucuk tablolarda
	// istatistik bayatlayinca planlayici satir tahminini milyonlara cikarip JIT'i
	// tetikliyordu (yonlendirme tablosu yuklemesi 0,85 ms -> 0,8-4,6 sn). Uzun analitik
	// sorgu yok; JIT'in kazanci yok, gecikme riski var. Sunucu tarafi ayar degil,
	// yalniz bu havuzun oturumlari icin gecerli (diger servisleri etkilemez).
	rp["jit"] = "off"
	return cfg, nil
}

// execLong, bilincli uzun suren tek bir ifadeyi kendi islemi icinde
// statement_timeout'u gevsetilmis (SET LOCAL) olarak calistirir.
func (s *Store) execLong(ctx context.Context, q string, args ...any) (int64, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback(ctx) //nolint:errcheck
	if err := relaxTimeout(ctx, tx); err != nil {
		return 0, err
	}
	tag, err := tx.Exec(ctx, q, args...)
	if err != nil {
		return 0, err
	}
	if err := tx.Commit(ctx); err != nil {
		return 0, err
	}
	return tag.RowsAffected(), nil
}

type txExecer interface {
	Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
}

// relaxTimeout, islem icin statement_timeout'u longOpTimeout'a yukseltir.
func relaxTimeout(ctx context.Context, tx txExecer) error {
	_, err := tx.Exec(ctx, fmt.Sprintf("SET LOCAL statement_timeout = %d", longOpTimeout.Milliseconds()))
	return err
}

func (s *Store) Close() error { s.pool.Close(); return nil }

// Pool, dogrudan Postgres baglanti havuzunu dondurur.
func (s *Store) Pool() *pgxpool.Pool { return s.pool }

// newID, "<prefix>_<16 hex>" biciminde id uretir (sqlitestore ile ayni desen).
func newID(prefix string) string {
	b := make([]byte, 8)
	rand.Read(b)
	return prefix + "_" + hex.EncodeToString(b)
}
