package pgstore

import (
	"context"
	"testing"
	"time"

	"github.com/tkodcumpeg4/zorven/server/store"
)

func TestDoorRevocationsAndPlanClose(t *testing.T) {
	ctx := context.Background()
	s := freshStore(t)
	tenant := store.DefaultTenantID

	c, err := s.CreateClient(ctx, tenant, "box2", "tok_door2", "hash_door2")
	if err != nil {
		t.Fatal(err)
	}
	tun, err := s.CreateTunnel(ctx, tenant, c.ID, "http://localhost:1")
	if err != nil {
		t.Fatal(err)
	}
	proto, exposure := store.ProtoTCP, store.ExposurePort
	if _, err := s.UpdateTunnel(ctx, tenant, tun.ID, store.TunnelPatch{Proto: &proto, Exposure: &exposure}); err != nil {
		t.Fatal(err)
	}

	// Oturum iptali: yalniz ileri gider.
	t1 := time.Now().UTC().Truncate(time.Millisecond)
	if err := s.RecordDoorRevocation(ctx, tun.ID, "alice", t1); err != nil {
		t.Fatal(err)
	}
	if err := s.RecordDoorRevocation(ctx, tun.ID, "alice", t1.Add(-time.Hour)); err != nil {
		t.Fatal(err)
	}
	revs, err := s.ListDoorRevocations(ctx, tun.ID)
	if err != nil || len(revs) != 1 || !revs["alice"].Equal(t1) {
		t.Fatalf("iptal kaydi geri gitmemeli: %+v %v", revs, err)
	}

	// Plan dususu: kapilar kalici kapanir, aktif grant'ler iptal, oturumlar dusurulur.
	if err := s.SetTunnelDoor(ctx, tenant, store.TunnelDoor{TunnelID: tun.ID, Enabled: true, DurationSec: 3600, Host: "door--p--q.example.com"}); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	if _, _, err := s.OpenDoorGrant(ctx, store.DoorGrant{TenantID: tenant, TunnelID: tun.ID, IP: "203.0.113.7",
		Identity: "Carol", Method: "basic", ExpiresAt: now.Add(time.Hour)}); err != nil {
		t.Fatal(err)
	}
	at := time.Now().UTC().Truncate(time.Millisecond)
	ids, revoked, err := s.CloseDoorsForTenant(ctx, tenant, at)
	if err != nil || len(ids) != 1 || ids[0] != tun.ID || len(revoked) != 1 || revoked[0].IP != "203.0.113.7" {
		t.Fatalf("CloseDoorsForTenant: %v %+v %v", ids, revoked, err)
	}
	d, err := s.GetTunnelDoor(ctx, tenant, tun.ID)
	if err != nil || d.Enabled || !d.PlanClosed {
		t.Fatalf("kapi kalici kapanmali (enabled=false, plan_closed=true): %+v %v", d, err)
	}
	if doors, _ := s.ListEnabledDoors(ctx); len(doors) != 0 {
		t.Fatalf("etkin kapi kalmamali: %+v", doors)
	}
	// Kapali ama plan_closed: route hala listelenir (Disabled) ki host sayfa gostersin.
	routes, err := s.ListDoorRoutes(ctx)
	if err != nil || len(routes) != 1 || routes[0].Door == nil || !routes[0].Door.Disabled {
		t.Fatalf("plan_closed route Disabled olmali: %+v %v", routes, err)
	}
	if active, _ := s.ListActiveDoorGrantsByTunnel(ctx, tun.ID); len(active) != 0 {
		t.Fatalf("grant'ler iptal edilmeli: %+v", active)
	}
	revs, _ = s.ListDoorRevocations(ctx, tun.ID)
	if got, ok := revs["carol"]; !ok || !got.Equal(at) {
		t.Fatalf("plan dususunde kimlik oturumu da iptal edilmeli (kucuk harf): %+v", revs)
	}
	// Idempotent.
	ids, revoked, err = s.CloseDoorsForTenant(ctx, tenant, at)
	if err != nil || len(ids) != 0 || len(revoked) != 0 {
		t.Fatalf("ikinci cagri bos olmali: %v %+v %v", ids, revoked, err)
	}
	// Sahip yeniden acinca plan_closed temizlenir.
	if err := s.SetTunnelDoor(ctx, tenant, store.TunnelDoor{TunnelID: tun.ID, Enabled: true, DurationSec: 3600, Host: "door--p--q.example.com"}); err != nil {
		t.Fatal(err)
	}
	if d, _ := s.GetTunnelDoor(ctx, tenant, tun.ID); !d.Enabled || d.PlanClosed {
		t.Fatalf("yeniden acilinca plan_closed false olmali: %+v", d)
	}
}
