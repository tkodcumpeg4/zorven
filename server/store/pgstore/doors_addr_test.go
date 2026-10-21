package pgstore

import (
	"context"
	"testing"
	"time"

	"github.com/tkodcumpeg4/zorven/server/store"
)

func TestDoorGrantExtraAddr(t *testing.T) {
	ctx := context.Background()
	s := freshStore(t)
	tenant := store.DefaultTenantID
	c, err := s.CreateClient(ctx, tenant, "box", "tok_dooraddr", "hash_dooraddr")
	if err != nil {
		t.Fatal(err)
	}
	tun, err := s.CreateTunnel(ctx, tenant, c.ID, "http://localhost:1")
	if err != nil {
		t.Fatal(err)
	}
	g, _, err := s.OpenDoorGrant(ctx, store.DoorGrant{TenantID: tenant, TunnelID: tun.ID, IP: "203.0.113.7",
		Identity: "alice", Method: "basic", ExpiresAt: time.Now().Add(time.Hour)})
	if err != nil {
		t.Fatal(err)
	}
	// Ayni aile: etkisiz.
	if _, added, err := s.AddDoorGrantAddr(ctx, g.ID, "203.0.113.8"); err != nil || added {
		t.Fatalf("ayni aile eklenmemeli: %v %v", added, err)
	}
	got, added, err := s.AddDoorGrantAddr(ctx, g.ID, "2001:db8::5")
	if err != nil || !added || len(got.ExtraIPs) != 1 || got.ExtraIPs[0] != "2001:db8::5" {
		t.Fatalf("ek adres: %+v %v %v", got, added, err)
	}
	active, _ := s.ListActiveDoorGrantsByTunnel(ctx, tun.ID)
	if len(active) != 1 || len(active[0].ExtraIPs) != 1 {
		t.Fatalf("aktif listede ek adres yok: %+v", active)
	}
	addrs, err := s.DoorGrantAddrsByIP(ctx, tenant, tun.ID, "2001:db8::5")
	if err != nil || len(addrs) != 2 {
		t.Fatalf("tum adresler: %v %v", addrs, err)
	}
	// Ek adres uzerinden kapatma ana grant'i da iptal eder.
	n, err := s.RevokeDoorGrantsByIP(ctx, tenant, tun.ID, "2001:db8::5")
	if err != nil || n != 1 {
		t.Fatalf("ek adresle iptal: %d %v", n, err)
	}
	if a, _ := s.ListActiveDoorGrantsByTunnel(ctx, tun.ID); len(a) != 0 {
		t.Fatal("iptal sonrasi aktif grant kalmamali")
	}
	// Iptal edilmis grant'e ek adres baglanamaz.
	if _, _, err := s.AddDoorGrantAddr(ctx, g.ID, "2001:db8::6"); err == nil {
		t.Fatal("iptal edilmis grant'e ek adres baglanmamali")
	}
	// Budama grant'i silince ek adres satirlari da gider (CASCADE).
	if _, err := s.PruneDoorGrants(ctx, time.Now().Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	var cnt int
	if err := s.pool.QueryRow(ctx, `SELECT count(*) FROM tunnel_door_grant_addrs WHERE grant_id=$1`, g.ID).Scan(&cnt); err != nil || cnt != 0 {
		t.Fatalf("ek adres satirlari silinmeli: %d %v", cnt, err)
	}
}
