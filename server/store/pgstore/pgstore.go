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

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/tkodcumpeg4/zorven/server/store"
)

type Store struct {
	pool *pgxpool.Pool
}

// Arayuz uyumu DERLEME ZAMANINDA kanitlanir: bir metot eksik kalirsa
// veya imza kayarsa build kirilir, testlerin kosmasini beklemeye gerek kalmaz.
var _ store.Store = (*Store)(nil)

// Open, baglanti havuzunu acar ve bekleyen migration'lari uygular.
func Open(ctx context.Context, dsn string) (*Store, error) {
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		return nil, fmt.Errorf("postgres havuzu acilamadi: %w", err)
	}
	// Ping: DSN sozdizimsel olarak dogru ama sunucu erisilemez olabilir;
	// hatayi burada verip yariyolda kalmis bir sunucu baslatmayi onleriz.
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("postgres'e baglanilamadi: %w", err)
	}
	if err := migrate(ctx, pool); err != nil {
		pool.Close()
		return nil, err
	}
	return &Store{pool: pool}, nil
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
