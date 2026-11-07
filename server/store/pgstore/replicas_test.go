package pgstore

import (
	"context"
	"reflect"
	"testing"

	"github.com/tkodcumpeg4/zorven/server/store"
)

// ListAllTunnelReplicas: replikasi olan tum tunelleri tek sorguyla, eklenme sirasiyla doner.
func TestListAllTunnelReplicas(t *testing.T) {
	ctx := context.Background()
	s := freshStore(t)
	tenant := store.DefaultTenantID

	mk := func(name string) store.Client {
		c, err := s.CreateClient(ctx, tenant, name, "tok_"+name, "hash_"+name)
		if err != nil {
			t.Fatal(err)
		}
		return c
	}
	prim, r1, r2 := mk("primary"), mk("rep1"), mk("rep2")
	tun := func(target string) store.Tunnel {
		tn, err := s.CreateTunnel(ctx, tenant, prim.ID, target)
		if err != nil {
			t.Fatal(err)
		}
		return tn
	}
	t1, t2, t3 := tun("http://localhost:1"), tun("http://localhost:2"), tun("http://localhost:3")

	got, err := s.ListAllTunnelReplicas(ctx)
	if err != nil || len(got) != 0 {
		t.Fatalf("replika yokken bos harita beklenirdi: %v %v", got, err)
	}

	for _, add := range []struct{ tun, cli string }{{t1.ID, r1.ID}, {t1.ID, r2.ID}, {t2.ID, r2.ID}} {
		if err := s.AddTunnelReplica(ctx, tenant, add.tun, add.cli); err != nil {
			t.Fatal(err)
		}
	}
	got, err = s.ListAllTunnelReplicas(ctx)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string][]string{t1.ID: {r1.ID, r2.ID}, t2.ID: {r2.ID}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("replikalar = %v; istenen %v", got, want)
	}
	if _, ok := got[t3.ID]; ok {
		t.Fatal("replikasiz tunel haritada olmamali")
	}

	if err := s.RemoveTunnelReplica(ctx, tenant, t1.ID, r1.ID); err != nil {
		t.Fatal(err)
	}
	got, err = s.ListAllTunnelReplicas(ctx)
	if err != nil {
		t.Fatal(err)
	}
	want = map[string][]string{t1.ID: {r2.ID}, t2.ID: {r2.ID}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("kaldirmadan sonra = %v; istenen %v", got, want)
	}
}
