package pgstore

import (
	"context"
	"testing"

	"github.com/tkodcumpeg4/zorven/server/store"
)

// K3: policy yabanci kiracinin tunel/hostname'ine BAGLANAMAZ; gecmisten kalan
// yabanci bag ListPolicyRoutes'tan elenir.
func TestPolicyBindingTenantOwnership(t *testing.T) {
	ctx := context.Background()
	s := freshStore(t)

	a, _ := s.CreateTenant(ctx, "kiraci-a")
	b, _ := s.CreateTenant(ctx, "kiraci-b")
	ca, _ := s.CreateClient(ctx, a.ID, "a-pc", "tid-a", "hash-a")
	cb, _ := s.CreateClient(ctx, b.ID, "b-pc", "tid-b", "hash-b")
	ta, _ := s.CreateTunnel(ctx, a.ID, ca.ID, "http://x")
	tb, _ := s.CreateTunnel(ctx, b.ID, cb.ID, "http://y")
	if _, err := s.AddHostname(ctx, a.ID, ta.ID, "a.rpshell.app", store.HostTypeGlobal); err != nil {
		t.Fatal(err)
	}
	if _, err := s.AddHostname(ctx, b.ID, tb.ID, "b.rpshell.app", store.HostTypeGlobal); err != nil {
		t.Fatal(err)
	}
	pol, err := s.CreatePolicy(ctx, a.ID, "", "p", []byte(`{"rules":[]}`), 100)
	if err != nil {
		t.Fatal(err)
	}

	// Kendi hostuna baglanabilir (buyuk/kucuk harf duyarsiz).
	if err := s.BindPolicy(ctx, a.ID, pol.ID, store.PolicyBinding{Hostname: "A.rpshell.app."}); err != nil {
		t.Fatalf("kendi host baglama: %v", err)
	}
	// Yabanci host / tunel reddedilir.
	if err := s.BindPolicy(ctx, a.ID, pol.ID, store.PolicyBinding{Hostname: "b.rpshell.app"}); err != store.ErrNotFound {
		t.Fatalf("yabanci host baglama ErrNotFound olmali: %v", err)
	}
	if err := s.BindPolicy(ctx, a.ID, pol.ID, store.PolicyBinding{TunnelID: tb.ID}); err != store.ErrNotFound {
		t.Fatalf("yabanci tunel baglama ErrNotFound olmali: %v", err)
	}

	// Gecmisten kalan yabanci bag (dogrudan SQL) ingress listesine SIZMAZ.
	if _, err := s.pool.Exec(ctx,
		`INSERT INTO policy_bindings (policy_id, tunnel_id, hostname) VALUES ($1,'','b.rpshell.app')`, pol.ID); err != nil {
		t.Fatal(err)
	}
	routes, err := s.ListPolicyRoutes(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(routes) != 1 || routes[0].Host != "a.rpshell.app" {
		t.Fatalf("yalnizca kendi host donmeli: %+v", routes)
	}
}

// O3: dogrulanmamis custom hostname uzerindeki yol kurali trafik almaz.
func TestPathRouteUnverifiedCustomHostnameExcluded(t *testing.T) {
	ctx := context.Background()
	s := freshStore(t)

	a, _ := s.CreateTenant(ctx, "kiraci-a")
	ca, _ := s.CreateClient(ctx, a.ID, "a-pc", "tid-a", "hash-a")
	ta, _ := s.CreateTunnel(ctx, a.ID, ca.ID, "http://x")
	tb, _ := s.CreateTunnel(ctx, a.ID, ca.ID, "http://y")
	h, err := s.AddCustomHostname(ctx, a.ID, ta.ID, "pr.musteri.com", "tok")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.AddPathRoute(ctx, a.ID, "pr.musteri.com", "/api", tb.ID); err != nil {
		t.Fatalf("AddPathRoute: %v", err)
	}
	ents, err := s.ListPathRouteEntries(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(ents) != 0 {
		t.Fatalf("dogrulanmamis custom domain yol kurali donmemeli: %+v", ents)
	}
	if _, err := s.VerifyHostname(ctx, a.ID, h.ID); err != nil {
		t.Fatal(err)
	}
	ents, err = s.ListPathRouteEntries(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(ents) != 1 {
		t.Fatalf("dogrulaninca yol kurali donmeli: %+v", ents)
	}
}

// K1: platform adlari rezerve (0055).
func TestPlatformNamesReserved(t *testing.T) {
	ctx := context.Background()
	s := freshStore(t)
	for _, n := range []string{"panel", "analytics", "mail", "status", "app", "www", "api"} {
		ok, err := s.IsReservedName(ctx, n)
		if err != nil || !ok {
			t.Errorf("%q rezerve olmaliydi: %v %v", n, ok, err)
		}
	}
}
