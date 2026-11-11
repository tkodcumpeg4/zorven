package pgstore

import (
	"context"
	"testing"
	"time"

	"github.com/tkodcumpeg4/zorven/server/store"
)

// A4 #13: yonlendirme tablosu gecici tunelin bitis anini tasir (ingress veri
// yolunda kontrol eder); kalici tunelde ExpiresAt nil.
func TestListHostRoutesCarriesEphemeralExpiresAt(t *testing.T) {
	ctx := context.Background()
	s := freshStore(t)
	tenant := store.DefaultTenantID
	cli, err := s.CreateClient(ctx, tenant, "pc", "tok_pc", "hash_pc")
	if err != nil {
		t.Fatal(err)
	}
	exp := time.Now().Add(time.Hour).UTC().Truncate(time.Second)
	eph, err := s.CreateEphemeralTunnel(ctx, tenant, cli.ID, "http://localhost:1", "", exp)
	if err != nil {
		t.Fatal(err)
	}
	perm, err := s.CreateTunnel(ctx, tenant, cli.ID, "http://localhost:2")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.AddHostname(ctx, tenant, eph.ID, "gecici-ttl.zorven.test", store.HostTypeScoped); err != nil {
		t.Fatal(err)
	}
	if _, err := s.AddHostname(ctx, tenant, perm.ID, "kalici-ttl.zorven.test", store.HostTypeScoped); err != nil {
		t.Fatal(err)
	}
	routes, err := s.ListHostRoutes(ctx)
	if err != nil {
		t.Fatal(err)
	}
	seen := 0
	for _, r := range routes {
		switch r.FQDN {
		case "gecici-ttl.zorven.test":
			seen++
			if r.ExpiresAt == nil || r.ExpiresAt.Sub(exp).Abs() > 2*time.Second {
				t.Fatalf("gecici tunelin ExpiresAt'i yanlis: %v (beklenen %v)", r.ExpiresAt, exp)
			}
		case "kalici-ttl.zorven.test":
			seen++
			if r.ExpiresAt != nil {
				t.Fatalf("kalici tunelde ExpiresAt nil olmali: %v", r.ExpiresAt)
			}
		}
	}
	if seen != 2 {
		t.Fatalf("yonlendirmeler eksik: %d", seen)
	}
	if _, err := s.ListPathRouteEntries(ctx); err != nil {
		t.Fatalf("yol kurallari sorgusu: %v", err)
	}
}
