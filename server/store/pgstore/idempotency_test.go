package pgstore

import (
	"bytes"
	"context"
	"strings"
	"sync"
	"testing"
)

// Idempotency-Key deposu (0105). ZORVEN_TEST_PG_DSN yoksa freshStore testi atlar.
func TestIdempotencyStore(t *testing.T) {
	ctx := context.Background()
	s := freshStore(t)
	_, _ = s.pool.Exec(ctx, `TRUNCATE idempotency_keys`)
	sha := []byte("hash-a")

	_, created, err := s.BeginIdempotency(ctx, "ten_a", "k1", sha)
	if err != nil || !created {
		t.Fatalf("ilk ayirma: %v created=%v", err, created)
	}
	// Suruyor kaydi.
	rec, created, err := s.BeginIdempotency(ctx, "ten_a", "k1", sha)
	if err != nil || created || rec.Status != 0 || !bytes.Equal(rec.RequestSHA, sha) {
		t.Fatalf("suruyor: %v created=%v %+v", err, created, rec)
	}
	if err := s.CompleteIdempotency(ctx, "ten_a", "k1", 201, []byte(`{"id":"x"}`), "application/json"); err != nil {
		t.Fatal(err)
	}
	rec, created, err = s.BeginIdempotency(ctx, "ten_a", "k1", sha)
	if err != nil || created || rec.Status != 201 || string(rec.Response) != `{"id":"x"}` || rec.ContentType != "application/json" {
		t.Fatalf("tamamlandi: %v created=%v %+v", err, created, rec)
	}
	// Kiraci kapsami: ayni anahtar baska kiracida bagimsiz.
	if _, created, _ := s.BeginIdempotency(ctx, "ten_b", "k1", sha); !created {
		t.Fatal("baska kiracida ayni anahtar yeni olmali")
	}
	// Silinince yeniden ayrilabilir.
	if err := s.DeleteIdempotency(ctx, "ten_a", "k1"); err != nil {
		t.Fatal(err)
	}
	if _, created, _ := s.BeginIdempotency(ctx, "ten_a", "k1", sha); !created {
		t.Fatal("silinen anahtar yeniden ayrilabilmeli")
	}
	// Eski "suruyor" kaydi devralinir.
	_, _ = s.pool.Exec(ctx, `UPDATE idempotency_keys SET created_at = now() - interval '10 minutes' WHERE tenant_id='ten_a' AND key='k1'`)
	if _, created, _ := s.BeginIdempotency(ctx, "ten_a", "k1", []byte("hash-b")); !created {
		t.Fatal("bayat suruyor kaydi devralinmali")
	}

	// Es zamanli ayirma: tam bir kazanan.
	var wg sync.WaitGroup
	var mu sync.Mutex
	winners := 0
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, c, err := s.BeginIdempotency(ctx, "ten_a", "race", sha); err == nil && c {
				mu.Lock()
				winners++
				mu.Unlock()
			}
		}()
	}
	wg.Wait()
	if winners != 1 {
		t.Fatalf("tek kazanan bekleniyordu: %d", winners)
	}

	// Temizlik (retention): 24 saatten eskiler silinir.
	_, _ = s.pool.Exec(ctx, `UPDATE idempotency_keys SET created_at = now() - interval '25 hours' WHERE key = 'k1' AND tenant_id = 'ten_b'`)
	res, err := s.PruneRetention(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if res.IdempotencyKeys != 1 {
		t.Fatalf("retention 1 eski anahtar silmeli: %d", res.IdempotencyKeys)
	}
	if _, created, _ := s.BeginIdempotency(ctx, "ten_b", "k1", sha); !created {
		t.Fatal("silinen eski anahtar yeniden ayrilabilmeli")
	}
}

// Ayni organizasyonda ayni e-postaya iki bekleyen davet olusamaz (0105 kismi benzersiz indeks).
func TestPendingInvitationUnique(t *testing.T) {
	ctx := context.Background()
	s := freshStore(t)
	ten, err := s.CreateTenant(ctx, "inv-uniq")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.AddTeamMember(ctx, ten.ID, "Dup@X.test", "", "member", ""); err != nil {
		t.Fatal(err)
	}
	// Sirali ikinci istek: acik hata.
	_, err = s.AddTeamMember(ctx, ten.ID, "dup@x.test", "", "member", "")
	if err == nil || !strings.Contains(err.Error(), "zaten bekleyen bir davet") {
		t.Fatalf("ikinci davet reddedilmeli: %v", err)
	}
	// Es zamanli istekler: tam bir davet olusur, digerleri ayni hatayi alir (birim-ihlali cevrilir).
	var wg sync.WaitGroup
	var mu sync.Mutex
	ok, dup, other := 0, 0, 0
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := s.AddTeamMember(ctx, ten.ID, "race@x.test", "", "member", "")
			mu.Lock()
			defer mu.Unlock()
			switch {
			case err == nil:
				ok++
			case strings.Contains(err.Error(), "zaten bekleyen bir davet"):
				dup++
			default:
				other++
				t.Logf("beklenmeyen hata: %v", err)
			}
		}()
	}
	wg.Wait()
	if ok != 1 || other != 0 || dup != 7 {
		t.Fatalf("ok=%d dup=%d other=%d", ok, dup, other)
	}
	var n int
	_ = s.pool.QueryRow(ctx, `SELECT count(*) FROM "invitation" WHERE "organizationId"=$1 AND lower("email")='race@x.test' AND "status"='pending'`, ten.ID).Scan(&n)
	if n != 1 {
		t.Fatalf("tek bekleyen davet bekleniyordu: %d", n)
	}
	// Iptal/suresi dolmus davet yenisini engellemez.
	if _, err := s.pool.Exec(ctx, `UPDATE "invitation" SET "status"='canceled' WHERE "organizationId"=$1 AND lower("email")='race@x.test'`, ten.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.AddTeamMember(ctx, ten.ID, "race@x.test", "", "member", ""); err != nil {
		t.Fatalf("iptalden sonra yeni davet: %v", err)
	}
}
