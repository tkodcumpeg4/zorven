package pgstore

import (
	"context"
	"testing"
	"time"

	"github.com/tkodcumpeg4/zorven/server/store"
)

func TestListRecentlyOfflineClients(t *testing.T) {
	s := freshStore(t)
	ctx := context.Background()
	mk := func(name string, seenAgo time.Duration) string {
		c, err := s.CreateClient(ctx, store.DefaultTenantID, name, "tok_"+name, "hash_"+name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := s.pool.Exec(ctx, `UPDATE clients SET last_seen_at = now() - make_interval(secs => $2), agent_version='0.3.6' WHERE id=$1`,
			c.ID, seenAgo.Seconds()); err != nil {
			t.Fatal(err)
		}
		return c.ID
	}
	fresh := mk("yeni", 2*time.Minute)  // esik (10 dk) altinda: aday degil
	down := mk("kopuk", 30*time.Minute) // aday
	old := mk("eski", 48*time.Hour)     // 24 sa disinda: aday degil
	got, err := s.ListRecentlyOfflineClients(ctx, 24*time.Hour, 10*time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	m := map[string]OfflineClient{}
	for _, c := range got {
		m[c.ID] = c
	}
	if _, ok := m[fresh]; ok {
		t.Error("esik altindaki ajan aday olmamali")
	}
	if _, ok := m[old]; ok {
		t.Error("24 saatten eski ajan aday olmamali")
	}
	if c, ok := m[down]; !ok || c.HasSession || c.TenantSlug != store.DefaultTenantSlug || c.Version != "0.3.6" {
		t.Errorf("kopuk ajan aday olmali: %+v (var=%v)", c, ok)
	}
}

func TestOfflineNotifiedLifecycleAndOwners(t *testing.T) {
	s := freshStore(t)
	ctx := context.Background()
	c, err := s.CreateClient(ctx, store.DefaultTenantID, "core", "tok_core", "hash_core")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.pool.Exec(ctx, `UPDATE clients SET last_seen_at = now() - interval '30 minutes' WHERE id=$1`, c.ID); err != nil {
		t.Fatal(err)
	}
	get := func() OfflineClient {
		got, err := s.ListRecentlyOfflineClients(ctx, 24*time.Hour, 10*time.Minute)
		if err != nil {
			t.Fatal(err)
		}
		for _, x := range got {
			if x.ID == c.ID {
				return x
			}
		}
		t.Fatalf("aday bulunamadi")
		return OfflineClient{}
	}
	if get().OfflineNotified {
		t.Fatal("baslangicta isaretsiz olmali")
	}
	if err := s.MarkClientsOfflineNotified(ctx, []string{c.ID}); err != nil {
		t.Fatal(err)
	}
	if !get().OfflineNotified {
		t.Fatal("isaretlenmis olmali")
	}
	// Yeniden baglanma (el sikisma) isareti temizler.
	if err := s.UpdateDeviceInfo(ctx, c.ID, store.DeviceInfo{Hostname: "core", AgentVersion: "0.3.7"}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.pool.Exec(ctx, `UPDATE clients SET last_seen_at = now() - interval '30 minutes' WHERE id=$1`, c.ID); err != nil {
		t.Fatal(err)
	}
	if get().OfflineNotified {
		t.Fatal("yeniden baglaninca isaret temizlenmeli")
	}

	// Owner'lar: yalniz owner rolu ve e-postasi olanlar.
	org := store.DefaultTenantID
	for _, u := range []struct{ id, email, role string }{
		{"u_owner", "owner@ex.test", "owner"}, {"u_admin", "admin@ex.test", "admin"}, {"u_owner2", "owner2@ex.test", "owner"},
	} {
		if _, err := s.pool.Exec(ctx, `INSERT INTO "user" ("id","name","email","emailVerified","createdAt","updatedAt") VALUES ($1,$1,$2,true,now(),now())`, u.id, u.email); err != nil {
			t.Fatal(err)
		}
		if _, err := s.pool.Exec(ctx, `INSERT INTO "organization" ("id","name","slug","createdAt") VALUES ($1,'org','org-x',now()) ON CONFLICT DO NOTHING`, org); err != nil {
			t.Fatal(err)
		}
		if _, err := s.pool.Exec(ctx, `INSERT INTO "member" ("id","organizationId","userId","role","createdAt") VALUES ($1,$2,$3,$4,now())`, "m_"+u.id, org, u.id, u.role); err != nil {
			t.Fatal(err)
		}
	}
	owners, err := s.TenantOwners(ctx, org)
	if err != nil {
		t.Fatal(err)
	}
	if len(owners) != 2 || owners[0].Email == "admin@ex.test" || owners[1].Email == "admin@ex.test" {
		t.Fatalf("yalniz owner'lar donmeli: %+v", owners)
	}
}
