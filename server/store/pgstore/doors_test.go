package pgstore

import (
	"context"
	"testing"
	"time"

	"github.com/tkodcumpeg4/zorven/server/accesslog"
	"github.com/tkodcumpeg4/zorven/server/store"
)

func TestDoorSettingsAndGrants(t *testing.T) {
	ctx := context.Background()
	s := freshStore(t)
	tenant := store.DefaultTenantID

	c, err := s.CreateClient(ctx, tenant, "box", "tok_door1", "hash_door1")
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

	// Varsayilan: kapali, 12 saat.
	d, err := s.GetTunnelDoor(ctx, tenant, tun.ID)
	if err != nil || d.Enabled || d.DurationSec != store.DoorDuration12h {
		t.Fatalf("varsayilan beklenmedik: %+v %v", d, err)
	}
	if _, err := s.GetTunnelDoor(ctx, "baska", tun.ID); err == nil {
		t.Fatal("yabanci kiraci ErrNotFound almali")
	}
	if err := s.SetTunnelDoor(ctx, tenant, store.TunnelDoor{TunnelID: tun.ID, Enabled: true, DurationSec: 3600, Host: "door--x--y.example.com"}); err != nil {
		t.Fatal(err)
	}
	doors, err := s.ListEnabledDoors(ctx)
	if err != nil || len(doors) != 1 || doors[0].TunnelID != tun.ID {
		t.Fatalf("etkin kapi listesi: %+v %v", doors, err)
	}
	routes, err := s.ListDoorRoutes(ctx)
	if err != nil || len(routes) != 1 || routes[0].Door == nil || routes[0].FQDN != "door--x--y.example.com" ||
		routes[0].Door.DurationSec != 3600 || routes[0].Door.Proto != store.ProtoTCP {
		t.Fatalf("kapi route'u: %+v %v", routes, err)
	}

	// Grant: olustur, tekrar (ayni kayit), listele, iptal.
	now := time.Now().UTC()
	g1, created, err := s.OpenDoorGrant(ctx, store.DoorGrant{TenantID: tenant, TunnelID: tun.ID, IP: "203.0.113.7",
		Identity: "alice", Method: "basic", ExpiresAt: now.Add(time.Hour)})
	if err != nil || !created {
		t.Fatalf("OpenDoorGrant: %v created=%v", err, created)
	}
	g2, created, err := s.OpenDoorGrant(ctx, store.DoorGrant{TenantID: tenant, TunnelID: tun.ID, IP: "203.0.113.7",
		ExpiresAt: now.Add(48 * time.Hour)})
	if err != nil || created || g2.ID != g1.ID {
		t.Fatalf("aktif grant yeniden kullanilmali (sure uzamaz): %+v created=%v %v", g2, created, err)
	}
	if _, _, err := s.OpenDoorGrant(ctx, store.DoorGrant{TenantID: tenant, TunnelID: tun.ID, IP: "203.0.113.8",
		Method: "oauth", ExpiresAt: now.Add(-time.Minute)}); err != nil {
		t.Fatal(err)
	}
	active, err := s.ListActiveDoorGrantsByTunnel(ctx, tun.ID)
	if err != nil || len(active) != 1 || active[0].IP != "203.0.113.7" {
		t.Fatalf("yalniz suresi dolmamis grant aktif: %+v %v", active, err)
	}
	listed, err := s.ListDoorGrants(ctx, tenant, tun.ID, true, 10)
	if err != nil || len(listed) != 1 {
		t.Fatalf("ListDoorGrants aktif: %+v %v", listed, err)
	}
	if _, err := s.RevokeDoorGrant(ctx, "baska", tun.ID, g1.ID); err == nil {
		t.Fatal("yabanci kiraci iptal edememeli")
	}
	if _, err := s.RevokeDoorGrant(ctx, tenant, tun.ID, g1.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.RevokeDoorGrant(ctx, tenant, tun.ID, g1.ID); err == nil {
		t.Fatal("iptal edilmis grant tekrar iptal edilememeli")
	}
	if a, _ := s.ListActiveDoorGrantsByTunnel(ctx, tun.ID); len(a) != 0 {
		t.Fatal("iptal sonrasi aktif grant kalmamali")
	}

	// IP ile iptal.
	if _, _, err := s.OpenDoorGrant(ctx, store.DoorGrant{TenantID: tenant, TunnelID: tun.ID, IP: "203.0.113.9",
		ExpiresAt: now.Add(time.Hour)}); err != nil {
		t.Fatal(err)
	}
	if n, err := s.RevokeDoorGrantsByIP(ctx, tenant, tun.ID, "203.0.113.9"); err != nil || n != 1 {
		t.Fatalf("RevokeDoorGrantsByIP: %d %v", n, err)
	}

	// Retention: 2 saatlik dolmus grant, 1 saat oncesine kadar silinir.
	n, err := s.PruneDoorGrants(ctx, now)
	if err != nil || n < 1 {
		t.Fatalf("PruneDoorGrants: %d %v", n, err)
	}
}

func TestAccessEventSummaryDoor(t *testing.T) {
	ctx := context.Background()
	s := freshStore(t)
	if _, err := s.pool.Exec(ctx, `DELETE FROM tunnel_access_events`); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	evs := []accesslog.Event{
		{ID: "ae_d1", TenantID: store.DefaultTenantID, TunnelID: "tun_d", Method: "basic", Identity: "a", Success: true,
			Reason: accesslog.ReasonDoorOpened, ClientIP: "1.1.1.1", CreatedAt: now},
		{ID: "ae_d2", TenantID: store.DefaultTenantID, TunnelID: "tun_d", Method: "door", Reason: accesslog.ReasonBlockedNoGrant,
			ClientIP: "2.2.2.2", Count: 40, CreatedAt: now},
		{ID: "ae_d3", TenantID: store.DefaultTenantID, TunnelID: "tun_d", Method: "door", Reason: accesslog.ReasonBlockedNoGrant,
			ClientIP: "3.3.3.3", Count: 2, CreatedAt: now},
	}
	if err := s.InsertAccessEvents(ctx, evs); err != nil {
		t.Fatal(err)
	}
	sum, err := s.AccessEventSummary(ctx, store.DefaultTenantID, "tun_d", now.Add(-time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if sum.DoorGrants != 1 || sum.DoorBlocked != 42 || sum.DoorBlockedIPs != 2 {
		t.Fatalf("kapi ozeti: %+v", sum)
	}
	if sum.Total != 1 || sum.Failure != 0 {
		t.Fatalf("engellenenler giris istatistigine katilmamali: %+v", sum)
	}
}
