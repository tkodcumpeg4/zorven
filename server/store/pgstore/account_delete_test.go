package pgstore

// Hesap silme (account_delete.go): siniflandirma (saf) ve Postgres tabanli
// uctan uca senaryolar (ZORVEN_TEST_PG_DSN yoksa atlanir).

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/tkodcumpeg4/zorven/server/store"
)

func TestClassifyAccountOrg(t *testing.T) {
	cases := []struct {
		name        string
		orgID, role string
		members     int
		otherOwners int
		kind        string
		reason      string
	}{
		{"kisisel org: tek uye owner -> sil", "ten_a", "owner", 1, 0, acctDelete, ""},
		{"tek uye ama owner degil -> yine sil (sahipsiz kalirdi)", "ten_a", "member", 1, 0, acctDelete, ""},
		{"baska uyeli, ben member -> ayril", "ten_a", "member", 4, 1, acctLeave, ""},
		{"baska uyeli, ben admin -> ayril", "ten_a", "admin", 4, 1, acctLeave, ""},
		{"baska uyeli, tek owner -> ENGEL", "ten_a", "owner", 3, 0, acctBlock, store.AccountBlockSoleOwner},
		{"baska uyeli, baska owner da var -> ayril", "ten_a", "owner", 3, 1, acctLeave, ""},
		{"platform org owner -> ENGEL", store.DefaultTenantID, "owner", 5, 1, acctBlock, store.AccountBlockPlatform},
		{"platform org admin -> ENGEL", store.DefaultTenantID, "admin", 5, 1, acctBlock, store.AccountBlockPlatform},
		{"platform org tek uye -> ENGEL (silinemez)", store.DefaultTenantID, "member", 1, 0, acctBlock, store.AccountBlockPlatform},
		{"platform org sade uye -> ayril", store.DefaultTenantID, "member", 5, 2, acctLeave, ""},
	}
	for _, c := range cases {
		kind, reason := classifyAccountOrg(c.orgID, c.role, c.members, c.otherOwners)
		if kind != c.kind || reason != c.reason {
			t.Errorf("%s: (%s,%q) beklenirken (%s,%q)", c.name, c.kind, c.reason, kind, reason)
		}
	}
}

// --- Postgres ---------------------------------------------------------------

func acctUser(t *testing.T, s *Store, id, email string) {
	t.Helper()
	if _, err := s.pool.Exec(context.Background(),
		`INSERT INTO "user" ("id","name","email","emailVerified","createdAt","updatedAt") VALUES ($1,$1,$2,true,now(),now())`,
		id, email); err != nil {
		t.Fatalf("kullanici eklenemedi: %v", err)
	}
}

func acctMember(t *testing.T, s *Store, orgID, userID, role string) {
	t.Helper()
	if _, err := s.pool.Exec(context.Background(),
		`INSERT INTO "member" ("id","organizationId","userId","role","createdAt") VALUES ($1,$2,$3,$4,now())`,
		"mem_"+orgID+"_"+userID, orgID, userID, role); err != nil {
		t.Fatalf("uye eklenemedi: %v", err)
	}
}

func acctExists(t *testing.T, s *Store, sql string, args ...any) bool {
	t.Helper()
	var n int
	if err := s.pool.QueryRow(context.Background(), `SELECT count(*) FROM (`+sql+`) x`, args...).Scan(&n); err != nil {
		t.Fatalf("sorgu: %v (%s)", err, sql)
	}
	return n > 0
}

func TestPlanAccountDeletion_Siniflandirma(t *testing.T) {
	s := freshStore(t)
	ctx := context.Background()

	personal, _ := s.CreateTenant(ctx, "plan-personal")
	shared, _ := s.CreateTenant(ctx, "plan-shared")
	owned, _ := s.CreateTenant(ctx, "plan-owned")
	acctUser(t, s, "usr_plan", "plan@x.test")
	acctUser(t, s, "usr_other", "other@x.test")
	acctMember(t, s, personal.ID, "usr_plan", "owner")
	acctMember(t, s, shared.ID, "usr_other", "owner")
	acctMember(t, s, shared.ID, "usr_plan", "member")
	acctMember(t, s, owned.ID, "usr_plan", "owner")
	acctMember(t, s, owned.ID, "usr_other", "admin")
	// Silinecek organizasyonun icerigi sayilir.
	cli, err := s.CreateClient(ctx, personal.ID, "ev-pc", "tid_plan_1", "h")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.CreateTunnel(ctx, personal.ID, cli.ID, "http://localhost:8000"); err != nil {
		t.Fatal(err)
	}

	plan, err := s.PlanAccountDeletion(ctx, "usr_plan")
	if err != nil {
		t.Fatal(err)
	}
	if plan.CanDelete() {
		t.Fatalf("baska uyeli org'un tek sahibi silemez: %+v", plan)
	}
	if len(plan.Blockers) != 1 || plan.Blockers[0].ID != owned.ID || plan.Blockers[0].Reason != store.AccountBlockSoleOwner || plan.Blockers[0].Members != 2 {
		t.Fatalf("engel beklenenden farkli: %+v", plan.Blockers)
	}
	if len(plan.Delete) != 1 || plan.Delete[0].ID != personal.ID || plan.Delete[0].Tunnels != 1 || plan.Delete[0].Clients != 1 {
		t.Fatalf("silinecek org ve sayilari beklenenden farkli: %+v", plan.Delete)
	}
	if len(plan.Leave) != 1 || plan.Leave[0].ID != shared.ID || plan.Leave[0].Role != "member" {
		t.Fatalf("ayrilacak org beklenenden farkli: %+v", plan.Leave)
	}

	// Engelli hesapta PurgeAccount hicbir sey degistirmez.
	_, err = s.PurgeAccount(ctx, "usr_plan")
	var blocked *store.AccountBlockedError
	if !errors.As(err, &blocked) || len(blocked.Plan.Blockers) != 1 {
		t.Fatalf("*AccountBlockedError beklenir: %v", err)
	}
	if !acctExists(t, s, `SELECT 1 FROM tenants WHERE id = $1`, personal.ID) ||
		!acctExists(t, s, `SELECT 1 FROM "member" WHERE "userId" = 'usr_plan'`) {
		t.Fatal("engelli silme hicbir veriye dokunmamali")
	}

	// Sahiplik devredilince (diger uye owner olunca) engel kalkar: ayrilir.
	if _, err := s.pool.Exec(ctx, `UPDATE "member" SET "role"='owner' WHERE "organizationId"=$1 AND "userId"='usr_other'`, owned.ID); err != nil {
		t.Fatal(err)
	}
	plan, err = s.PlanAccountDeletion(ctx, "usr_plan")
	if err != nil || !plan.CanDelete() || len(plan.Leave) != 2 {
		t.Fatalf("baska owner varken org'dan ayrilinabilir: %v %+v", err, plan)
	}
}

func TestPlanAccountDeletion_PlatformYoneticisiEngellenir(t *testing.T) {
	s := freshStore(t)
	ctx := context.Background()
	acctUser(t, s, "usr_plat", "plat@x.test")
	acctMember(t, s, store.DefaultTenantID, "usr_plat", "owner")
	plan, err := s.PlanAccountDeletion(ctx, "usr_plat")
	if err != nil {
		t.Fatal(err)
	}
	if plan.CanDelete() || len(plan.Blockers) != 1 || plan.Blockers[0].Reason != store.AccountBlockPlatform {
		t.Fatalf("platform yoneticisi engellenmeli: %+v", plan)
	}
	if _, err := s.PurgeAccount(ctx, "usr_plat"); err == nil {
		t.Fatal("platform yoneticisi silinememeli")
	}
	if !acctExists(t, s, `SELECT 1 FROM tenants WHERE id = $1`, store.DefaultTenantID) {
		t.Fatal("platform organizasyonu silinmemeli")
	}
}

func TestPurgeAccount_BosKimlik(t *testing.T) {
	s := freshStore(t)
	if _, err := s.PurgeAccount(context.Background(), ""); err == nil || !strings.Contains(err.Error(), "bos") {
		t.Fatalf("bos kullanici kimligi reddedilmeli: %v", err)
	}
}
