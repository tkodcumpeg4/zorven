package pgstore

import (
	"context"
	"testing"
	"time"

	"github.com/tkodcumpeg4/zorven/server/store"
)

// K3: (public_ip, public_port) benzersizligi, karantina tetikleyicisi ve sayac.
func TestTCPPortPool_AddrUniqueQuarantineCount(t *testing.T) {
	ctx := context.Background()
	s := freshStore(t)
	if _, err := s.pool.Exec(ctx, `DELETE FROM tcp_port_releases`); err != nil {
		t.Fatal(err)
	}
	c, err := s.CreateClient(ctx, store.DefaultTenantID, "c1", "tok1", "hash1")
	if err != nil {
		t.Fatal(err)
	}
	mk := func() store.Tunnel {
		tn, err := s.CreateTunnel(ctx, store.DefaultTenantID, c.ID, "http://127.0.0.1:1")
		if err != nil {
			t.Fatal(err)
		}
		return tn
	}
	a, b, d := mk(), mk(), mk()

	// Ayni port farkli IP'de serbest; ayni (ip, port) cakisir.
	if err := s.SetTunnelAddr(ctx, store.DefaultTenantID, a.ID, "", 10000); err != nil {
		t.Fatal(err)
	}
	if err := s.SetTunnelAddr(ctx, store.DefaultTenantID, b.ID, "198.51.100.7", 10000); err != nil {
		t.Fatalf("ayni port farkli IP'de verilebilmeli: %v", err)
	}
	if err := s.SetTunnelAddr(ctx, store.DefaultTenantID, d.ID, "", 10000); err == nil {
		t.Fatal("ayni (ip, port) ikinci tunele verilmemeli")
	}

	// Eski API (SetTunnelPort) birincil IP'ye yazar: geriye uyum.
	if err := s.SetTunnelPort(ctx, store.DefaultTenantID, d.ID, 10001); err != nil {
		t.Fatal(err)
	}
	list, err := s.ListReservedPortTunnels(ctx)
	if err != nil || len(list) != 3 {
		t.Fatalf("liste: %v %d", err, len(list))
	}
	for _, tn := range list {
		if tn.ID == d.ID && tn.PublicIP != "" {
			t.Fatalf("SetTunnelPort birincil IP olmali: %q", tn.PublicIP)
		}
		if tn.ID == b.ID && tn.PublicIP != "198.51.100.7" {
			t.Fatalf("ikincil IP okunmadi: %q", tn.PublicIP)
		}
	}
	if n, err := s.CountReservedPorts(ctx, store.DefaultTenantID); err != nil || n != 3 {
		t.Fatalf("CountReservedPorts=%d %v", n, err)
	}

	// Serbest birakma -> karantina; silme -> karantina.
	if err := s.SetTunnelPort(ctx, store.DefaultTenantID, a.ID, 0); err != nil {
		t.Fatal(err)
	}
	if err := s.DeleteTunnel(ctx, store.DefaultTenantID, b.ID); err != nil {
		t.Fatal(err)
	}
	rel, err := s.ListReleasedPorts(ctx, time.Now().Add(-10*time.Minute))
	if err != nil || len(rel) != 2 {
		t.Fatalf("karantina: %v %+v", err, rel)
	}
	got := map[store.PortAddr]bool{}
	for _, r := range rel {
		got[r] = true
	}
	if !got[store.PortAddr{IP: "", Port: 10000}] || !got[store.PortAddr{IP: "198.51.100.7", Port: 10000}] {
		t.Fatalf("beklenen karantina satirlari yok: %+v", rel)
	}
	// Pencere disinda kalan (gelecekte baslayan pencere) bos doner.
	if rel, _ := s.ListReleasedPorts(ctx, time.Now().Add(time.Minute)); len(rel) != 0 {
		t.Fatalf("pencere disi satir donmemeli: %+v", rel)
	}
	if n, _ := s.CountReservedPorts(ctx, store.DefaultTenantID); n != 1 {
		t.Fatalf("serbest sonrasi sayac=%d", n)
	}
}
