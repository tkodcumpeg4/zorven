package pgstore

import (
	"context"
	"testing"

	"github.com/tkodcumpeg4/zorven/server/store"
)

// F-11: dogrulanmamis ozel domain iki kiracida olabilir; ikinci dogrulama 409.
func TestUnverifiedCustomHostnameNotGlobal(t *testing.T) {
	ctx := context.Background()
	s := freshStore(t)
	a, err := s.CreateTenant(ctx, "kiraci-a")
	if err != nil {
		t.Fatal(err)
	}
	b, err := s.CreateTenant(ctx, "kiraci-b")
	if err != nil {
		t.Fatal(err)
	}
	ha, err := s.AddCustomHostname(ctx, a.ID, "", "panel.musteri.test", "tok-a")
	if err != nil {
		t.Fatalf("a ekleme: %v", err)
	}
	hb, err := s.AddCustomHostname(ctx, b.ID, "", "panel.musteri.test", "tok-b")
	if err != nil {
		t.Fatalf("b ekleme (dogrulanmamis kopya serbest olmali): %v", err)
	}
	if _, err := s.AddCustomHostname(ctx, a.ID, "", "PANEL.musteri.test", "tok-a2"); err != store.ErrHostnameTaken {
		t.Fatalf("ayni kiracida ayni ozel ad ikinci kez eklenmemeli: %v", err)
	}
	if _, err := s.VerifyHostname(ctx, a.ID, ha.ID); err != nil {
		t.Fatalf("a dogrulama: %v", err)
	}
	if _, err := s.VerifyHostname(ctx, b.ID, hb.ID); err != store.ErrHostnameTaken {
		t.Fatalf("b dogrulama ErrHostnameTaken olmali: %v", err)
	}
	if _, err := s.AddCustomHostname(ctx, b.ID, "", "panel.musteri.test", "tok-c"); err != store.ErrHostnameTaken {
		t.Fatalf("dogrulanmis ad icin yeni ekleme 409 olmali: %v", err)
	}
}
