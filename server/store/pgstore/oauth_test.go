package pgstore

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/tkodcumpeg4/zorven/server/oauthas"
	"github.com/tkodcumpeg4/zorven/server/store"
)

// OAuth deposu: istemci, kod (tek kullanim), grant, refresh donusu/yeniden kullanim,
// iptal ve temizlik (0079). ZORVEN_TEST_PG_DSN yoksa atlanir.
func TestOAuthStore_Contract(t *testing.T) {
	ctx := context.Background()
	s := freshStore(t)
	o := s.OAuth()
	now := time.Now().UTC().Truncate(time.Millisecond)

	c := oauthas.Client{ID: "zoc_t1", Name: "Claude", RedirectURIs: []string{"https://claude.ai/cb"},
		GrantTypes: []string{"authorization_code"}, CreatedAt: now}
	if err := o.CreateClient(ctx, c); err != nil {
		t.Fatal(err)
	}
	got, err := o.GetClient(ctx, "zoc_t1")
	if err != nil || got.Name != "Claude" || len(got.RedirectURIs) != 1 {
		t.Fatalf("GetClient: %v %+v", err, got)
	}
	if _, err := o.GetClient(ctx, "yok"); !errors.Is(err, oauthas.ErrNotFound) {
		t.Fatalf("beklenen ErrNotFound: %v", err)
	}

	// Kod: tek kullanim, sure.
	code := oauthas.Code{ClientID: "zoc_t1", UserID: "u1", TenantID: store.DefaultTenantID, RedirectURI: "https://claude.ai/cb",
		Scope: "mcp:read", Resource: "https://zorven.app/mcp", CodeChallenge: "ch", ExpiresAt: now.Add(time.Minute)}
	if err := o.CreateCode(ctx, "h1", code); err != nil {
		t.Fatal(err)
	}
	if _, err := o.ConsumeCode(ctx, "h1", now); err != nil {
		t.Fatal(err)
	}
	if _, err := o.ConsumeCode(ctx, "h1", now); !errors.Is(err, oauthas.ErrUsed) {
		t.Fatalf("ikinci tuketim ErrUsed olmali: %v", err)
	}
	code.ExpiresAt = now.Add(-time.Second)
	_ = o.CreateCode(ctx, "h2", code)
	if _, err := o.ConsumeCode(ctx, "h2", now); !errors.Is(err, oauthas.ErrNotFound) {
		t.Fatalf("suresi dolmus kod ErrNotFound olmali: %v", err)
	}

	// Grant + refresh donusu.
	g := oauthas.Grant{ID: "zog_t1", UserID: "u1", TenantID: store.DefaultTenantID, ClientID: "zoc_t1",
		Resource: "https://zorven.app/mcp", Scope: "mcp:read", CreatedAt: now}
	if err := o.CreateGrant(ctx, g); err != nil {
		t.Fatal(err)
	}
	gg, err := o.GetGrant(ctx, "zog_t1")
	if err != nil || gg.ClientName != "Claude" || !gg.Active() {
		t.Fatalf("GetGrant: %v %+v", err, gg)
	}
	if err := o.SaveRefresh(ctx, "r1", "zog_t1", now.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	if gid, err := o.RotateRefresh(ctx, "r1", now); err != nil || gid != "zog_t1" {
		t.Fatalf("RotateRefresh: %v %s", err, gid)
	}
	if gid, err := o.RotateRefresh(ctx, "r1", now); !errors.Is(err, oauthas.ErrUsed) || gid != "zog_t1" {
		t.Fatalf("yeniden kullanim: %v %s", err, gid)
	}
	if _, err := o.RotateRefresh(ctx, "yok", now); !errors.Is(err, oauthas.ErrNotFound) {
		t.Fatalf("bilinmeyen: %v", err)
	}

	// Liste + iptal.
	if l, err := o.ListGrants(ctx, "u1", ""); err != nil || len(l) != 1 {
		t.Fatalf("ListGrants: %v %d", err, len(l))
	}
	if l, _ := o.ListGrants(ctx, "baska", ""); len(l) != 0 {
		t.Fatal("baska kullanici gormemeli")
	}
	if err := o.RevokeGrant(ctx, "zog_t1", now); err != nil {
		t.Fatal(err)
	}
	if l, _ := o.ListGrants(ctx, "", store.DefaultTenantID); len(l) != 0 {
		t.Fatal("iptal edilen listelenmemeli")
	}
	if gg, _ := o.GetGrant(ctx, "zog_t1"); gg.Active() {
		t.Fatal("grant iptal edilmis olmali")
	}

	// Temizlik.
	if err := o.Prune(ctx, now.Add(40*24*time.Hour)); err != nil {
		t.Fatal(err)
	}
	if n, _ := o.CountClients(ctx); n != 0 {
		t.Fatalf("grant'siz eski istemci silinmeli: %d", n)
	}
}
