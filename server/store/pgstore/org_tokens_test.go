package pgstore

import (
	"context"
	"errors"
	"testing"

	"github.com/tkodcumpeg4/zorven/server/store"
)

// Bekleyen davetler sayilmali; cikarilan uyenin kisisel token'lari iptal edilmeli.
func TestMemberRemovalRevokesTokensAndPendingCount(t *testing.T) {
	s := freshStore(t)
	ctx := context.Background()
	ten, err := s.CreateTenant(ctx, "mem-revoke")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.AddTeamMember(ctx, ten.ID, "pending@x.test", "", "member", ""); err != nil {
		t.Fatal(err)
	}
	if n, err := s.CountPendingInvitations(ctx, ten.ID); err != nil || n != 1 {
		t.Fatalf("bekleyen davet sayisi 1 olmali: %d %v", n, err)
	}

	userID := "usr_memrevoke"
	_, _ = s.pool.Exec(ctx, `DELETE FROM "user" WHERE "id" = $1`, userID)
	if _, err := s.pool.Exec(ctx,
		`INSERT INTO "user" ("id","name","email","createdAt","updatedAt") VALUES ($1,'M','memrevoke@x.test',now(),now())`, userID); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = s.pool.Exec(context.Background(), `DELETE FROM "user" WHERE "id" = $1`, userID) })
	if _, err := s.pool.Exec(ctx,
		`INSERT INTO "member" ("id","organizationId","userId","role","createdAt") VALUES ('mem_memrevoke',$1,$2,'member',now())`, ten.ID, userID); err != nil {
		t.Fatal(err)
	}
	tok, err := s.CreateAPIToken(ctx, ten.ID, &userID, "kisisel", "tid_mem_1", "h", "zrv_api_", nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.RemoveTeamMember(ctx, ten.ID, "mem_memrevoke"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.GetAPIToken(ctx, ten.ID, tok.ID); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("cikarilan uyenin token'i iptal edilmeli: %v", err)
	}
}
