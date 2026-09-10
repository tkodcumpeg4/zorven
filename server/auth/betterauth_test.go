package auth

import (
	"context"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

func TestBetterAuthVerifier_EmptyOrNil(t *testing.T) {
	v := NewBetterAuthVerifier(nil, 1*time.Minute)
	_, err := v.VerifySession(context.Background(), "")
	if err == nil {
		t.Error("bos token hata vermeliydi")
	}

	_, err = v.VerifySession(context.Background(), "token_123")
	if err == nil {
		t.Error("pool nil iken hata vermeliydi")
	}
}

func testPGPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	dsn := os.Getenv("ZORVEN_TEST_PG_DSN")
	if dsn == "" {
		t.Skip("ZORVEN_TEST_PG_DSN ayarli degil; BetterAuth Postgres testleri atlaniyor")
	}
	u, err := url.Parse(dsn)
	if err != nil || !strings.Contains(strings.TrimPrefix(u.Path, "/"), "test") {
		t.Fatalf("ZORVEN_TEST_PG_DSN bir test veritabanina isaret etmeli: %s", dsn)
	}

	ctx := context.Background()
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatalf("pgxpool.New: %v", err)
	}
	t.Cleanup(func() {
		pool.Close()
	})
	return pool
}

func TestBetterAuthVerifier_VerifySession(t *testing.T) {
	pool := testPGPool(t)
	ctx := context.Background()

	// Temizlik
	_, _ = pool.Exec(ctx, `DELETE FROM "session" WHERE "token" IN ('tok_test_valid', 'tok_test_expired')`)
	_, _ = pool.Exec(ctx, `DELETE FROM "member" WHERE "id" = 'mem_test_1'`)
	_, _ = pool.Exec(ctx, `DELETE FROM "organization" WHERE "id" = 'org_test_1'`)
	_, _ = pool.Exec(ctx, `DELETE FROM "user" WHERE "id" = 'usr_test_1'`)

	// Test verileri ekle
	_, err := pool.Exec(ctx, `
		INSERT INTO "user" ("id", "name", "email", "createdAt", "updatedAt")
		VALUES ('usr_test_1', 'Test User', 'test@example.com', now(), now())
	`)
	if err != nil {
		t.Fatalf("user insert: %v", err)
	}

	_, err = pool.Exec(ctx, `
		INSERT INTO "organization" ("id", "name", "slug", "createdAt")
		VALUES ('org_test_1', 'Test Org', 'test-org-auth', now())
	`)
	if err != nil {
		t.Fatalf("org insert: %v", err)
	}

	_, err = pool.Exec(ctx, `
		INSERT INTO "member" ("id", "organizationId", "userId", "role", "createdAt")
		VALUES ('mem_test_1', 'org_test_1', 'usr_test_1', 'admin', now())
	`)
	if err != nil {
		t.Fatalf("member insert: %v", err)
	}

	_, err = pool.Exec(ctx, `
		INSERT INTO "session" ("id", "userId", "token", "expiresAt", "activeOrganizationId", "createdAt", "updatedAt")
		VALUES
			('sess_test_1', 'usr_test_1', 'tok_test_valid', now() + interval '1 hour', 'org_test_1', now(), now()),
			('sess_test_2', 'usr_test_1', 'tok_test_expired', now() - interval '1 hour', 'org_test_1', now(), now())
	`)
	if err != nil {
		t.Fatalf("session insert: %v", err)
	}

	verifier := NewBetterAuthVerifier(pool, 1*time.Minute)

	t.Run("Valid Session", func(t *testing.T) {
		u, err := verifier.VerifySession(ctx, "tok_test_valid")
		if err != nil {
			t.Fatalf("VerifySession hata verdi: %v", err)
		}
		if u.UserID != "usr_test_1" {
			t.Errorf("beklenen UserID usr_test_1, alinan: %s", u.UserID)
		}
		if u.Email != "test@example.com" {
			t.Errorf("beklenen Email test@example.com, alinan: %s", u.Email)
		}
		if u.Name != "Test User" {
			t.Errorf("beklenen Name 'Test User', alinan: %s", u.Name)
		}
		if u.ActiveOrganizationID != "org_test_1" {
			t.Errorf("beklenen ActiveOrganizationID org_test_1, alinan: %s", u.ActiveOrganizationID)
		}
		if u.Role != "admin" {
			t.Errorf("beklenen Role admin, alinan: %s", u.Role)
		}

		// Cache hit testi: DB'den silsek bile cache'den gelmeli
		cached, err := verifier.VerifySession(ctx, "tok_test_valid")
		if err != nil {
			t.Fatalf("cache VerifySession: %v", err)
		}
		if cached.UserID != u.UserID {
			t.Errorf("cache UserID uyusmuyor")
		}
	})

	t.Run("Expired Session", func(t *testing.T) {
		_, err := verifier.VerifySession(ctx, "tok_test_expired")
		if err == nil {
			t.Error("suresi dolmus oturum kabul edilmemeliydi")
		}
	})

	t.Run("Nonexistent Session", func(t *testing.T) {
		_, err := verifier.VerifySession(ctx, "tok_non_existent")
		if err == nil {
			t.Error("olmayan oturum kabul edilmemeliydi")
		}
	})
}
