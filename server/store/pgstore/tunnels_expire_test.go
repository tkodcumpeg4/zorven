package pgstore

import (
	"context"
	"testing"
	"time"

	"github.com/tkodcumpeg4/zorven/server/store"
)

// DeleteExpiredTunnels, SILINEN tunellerin satirlarini (kiraci + istemci dahil)
// doner: cagiran bunlardan kiraci olayini ve istemci config_update'ini yayar.
// Suresi dolmamis gecici tunel ve kalici tunel yerinde kalir.
func TestDeleteExpiredTunnelsReturnsDeletedRows(t *testing.T) {
	ctx := context.Background()
	s := freshStore(t)
	tenant := store.DefaultTenantID

	cli, err := s.CreateClient(ctx, tenant, "pc", "tok_pc", "hash_pc")
	if err != nil {
		t.Fatal(err)
	}
	soon := time.Now().Add(time.Hour)
	expired1, err := s.CreateEphemeralTunnel(ctx, tenant, cli.ID, "http://localhost:1", "", soon)
	if err != nil {
		t.Fatal(err)
	}
	expired2, err := s.CreateEphemeralTunnel(ctx, tenant, cli.ID, "http://localhost:2", "", soon)
	if err != nil {
		t.Fatal(err)
	}
	alive, err := s.CreateEphemeralTunnel(ctx, tenant, cli.ID, "http://localhost:3", "", soon)
	if err != nil {
		t.Fatal(err)
	}
	perm, err := s.CreateTunnel(ctx, tenant, cli.ID, "http://localhost:4")
	if err != nil {
		t.Fatal(err)
	}

	// Henuz dolan yok: bos liste, hata yok.
	if got, err := s.DeleteExpiredTunnels(ctx); err != nil || len(got) != 0 {
		t.Fatalf("dolan yokken bos liste beklenirdi: %v %v", got, err)
	}

	// Iki tunelin suresini gecmise cek (CreateEphemeralTunnel gecmis bitis kabul etmez).
	if _, err := s.pool.Exec(ctx,
		`UPDATE tunnels SET expires_at = now() - interval '1 minute' WHERE id = ANY($1)`,
		[]string{expired1.ID, expired2.ID}); err != nil {
		t.Fatal(err)
	}

	// Gecis: sure dolunca tunel SILINMEZ, trafige kapanir ve saklanir.
	now, err := s.ExpireEphemeralTunnels(ctx)
	if err != nil || len(now) != 2 {
		t.Fatalf("2 tunel gecis yapmali: %v %v", now, err)
	}
	if got, err := s.DeleteExpiredTunnels(ctx); err != nil || len(got) != 0 {
		t.Fatalf("saklama suresi dolmadan silme olmamali: %v %v", got, err)
	}
	if again, _ := s.ExpireEphemeralTunnels(ctx); len(again) != 0 {
		t.Fatalf("gecis ikinci kez yayilmamali: %v", again)
	}
	kept, err := s.GetTunnel(ctx, tenant, expired1.ID)
	if err != nil || kept.Enabled || kept.ExpiredAt == nil || kept.DeleteAt == nil {
		t.Fatalf("saklanan tunel pasif ve expired_at/delete_at dolu olmali: %+v %v", kept, err)
	}
	// Saklama penceresini gecmise cek: 24 sa + 1 dk once dolmus say.
	if _, err := s.pool.Exec(ctx,
		`UPDATE tunnels SET expired_at = now() - interval '25 hours' WHERE id = ANY($1)`,
		[]string{expired1.ID, expired2.ID}); err != nil {
		t.Fatal(err)
	}

	got, err := s.DeleteExpiredTunnels(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("2 silinen tunel beklenirdi: %+v", got)
	}
	ids := map[string]store.Tunnel{}
	for _, tn := range got {
		ids[tn.ID] = tn
	}
	for _, want := range []store.Tunnel{expired1, expired2} {
		tn, ok := ids[want.ID]
		if !ok {
			t.Fatalf("%s silinenler arasinda olmali: %+v", want.ID, got)
		}
		if tn.TenantID != tenant || tn.ClientID != cli.ID || !tn.Ephemeral || tn.ExpiresAt == nil {
			t.Fatalf("silinen satir kiraci/istemci/gecici bilgisini tasimali: %+v", tn)
		}
	}

	// Silinenler gitti; kalan tuneller yerinde.
	for _, id := range []string{expired1.ID, expired2.ID} {
		if _, err := s.GetTunnel(ctx, tenant, id); err != store.ErrNotFound {
			t.Fatalf("%s silinmeli: %v", id, err)
		}
	}
	for _, id := range []string{alive.ID, perm.ID} {
		if _, err := s.GetTunnel(ctx, tenant, id); err != nil {
			t.Fatalf("%s yerinde kalmali: %v", id, err)
		}
	}

	// Ikinci tur: silinecek bir sey yok (olay iki kez yayilmaz).
	if got, err := s.DeleteExpiredTunnels(ctx); err != nil || len(got) != 0 {
		t.Fatalf("ikinci turda bos liste beklenirdi: %v %v", got, err)
	}
}

// Silme otomatik (scoped) adi da serbest birakir; elle bagli ozel alan adi
// (custom) tunelden ayrilir ama kiracida kalir. Ayni ad yeniden alinabilir.
func TestDeleteExpiredTunnelsFreesAutoHostnameKeepsCustom(t *testing.T) {
	ctx := context.Background()
	s := freshStore(t)
	tenant := store.DefaultTenantID
	cli, err := s.CreateClient(ctx, tenant, "pc", "tok_pc", "hash_pc")
	if err != nil {
		t.Fatal(err)
	}
	tn, err := s.CreateEphemeralTunnel(ctx, tenant, cli.ID, "http://localhost:1", "", time.Now().Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.AddHostname(ctx, tenant, tn.ID, "demo--default.zorven.test", store.HostTypeScoped); err != nil {
		t.Fatal(err)
	}
	cust, err := s.AddCustomHostname(ctx, tenant, tn.ID, "app.musteri.test", "tok")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.pool.Exec(ctx, `UPDATE tunnels SET expires_at = now() - interval '1 minute' WHERE id = $1`, tn.ID); err != nil {
		t.Fatal(err)
	}
	if got, err := s.ExpireEphemeralTunnels(ctx); err != nil || len(got) != 1 {
		t.Fatalf("gecis beklenirdi: %v %v", got, err)
	}
	// Saklama suresince ad hala tunele bagli (yeni tunel 409 alir).
	if _, err := s.AddHostname(ctx, tenant, "", "demo--default.zorven.test", store.HostTypeScoped); err == nil {
		t.Fatal("saklama suresince ad serbest olmamali")
	}
	s.pool.Exec(ctx, `UPDATE tunnels SET expired_at = now() - interval '25 hours' WHERE id = $1`, tn.ID)
	if got, err := s.DeleteExpiredTunnels(ctx); err != nil || len(got) != 1 {
		t.Fatalf("silme beklenirdi: %v %v", got, err)
	}
	if _, err := s.AddHostname(ctx, tenant, "", "demo--default.zorven.test", store.HostTypeScoped); err != nil {
		t.Fatalf("otomatik ad serbest kalmali: %v", err)
	}
	h, err := s.GetHostnameByID(ctx, tenant, cust.ID)
	if err != nil || h.TunnelID != "" {
		t.Fatalf("ozel alan adi kalmali ve tunelden ayrilmali: %+v %v", h, err)
	}
}
