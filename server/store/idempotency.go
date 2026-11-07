package store

import (
	"context"
	"time"
)

// IdempotencyRecord, idempotency_keys satiri.
type IdempotencyRecord struct {
	Status      int // 0 = islem suruyor
	Response    []byte
	ContentType string
	RequestSHA  []byte
}

// IdempotencyStore, Idempotency-Key destegi icin ISTEGE BAGLI depo yuzeyi
// (tip dogrulamasiyla kullanilir; desteklemeyen depoda idempotency atlanir).
type IdempotencyStore interface {
	// BeginIdempotency anahtari ayirir. created=true: bu cagri sahibi (islem yapilabilir).
	// created=false: mevcut kayit doner. 5 dakikadan eski "suruyor" kaydi devralinir.
	BeginIdempotency(ctx context.Context, tenantID, key string, requestSHA []byte) (rec IdempotencyRecord, created bool, err error)
	// CompleteIdempotency yaniti saklar.
	CompleteIdempotency(ctx context.Context, tenantID, key string, status int, response []byte, contentType string) error
	// DeleteIdempotency ayirmayi kaldirir (5xx / islem yapilmadi).
	DeleteIdempotency(ctx context.Context, tenantID, key string) error
	// PruneIdempotency before oncesi kayitlari siler.
	PruneIdempotency(ctx context.Context, before time.Time) (int64, error)
}

// IdempotencyKeyTTL, kayitlarin saklama suresi.
const IdempotencyKeyTTL = 24 * time.Hour

// IdempotencyStaleAfter, "suruyor" kaydinin devralinabilecegi sure (surec cokmesi).
const IdempotencyStaleAfter = 5 * time.Minute
