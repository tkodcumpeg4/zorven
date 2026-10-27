package pgstore

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/tkodcumpeg4/zorven/server/store"
)

// CAPTCHA deposu: site CRUD, secret ozeti ile arama, sayac upsert + silmede temizlik (0102).
func TestCaptchaStore_Contract(t *testing.T) {
	ctx := context.Background()
	s := freshStore(t)
	tn := store.DefaultTenantID

	c, err := s.CreateCaptchaSite(ctx, store.CaptchaSite{TenantID: tn, Name: "Blog", SiteKey: "zcs_t1", SecretHash: "h1",
		Domains: []string{"ornek.com"}, Difficulty: 17, PassTTLMinutes: 1440})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.CreateCaptchaSite(ctx, store.CaptchaSite{TenantID: tn, Name: "x", SiteKey: "zcs_t1", SecretHash: "h2", Difficulty: 17, PassTTLMinutes: 1440}); err == nil {
		t.Error("tekrarlanan site_key reddedilmeli")
	}
	got, err := s.GetCaptchaSiteByKey(ctx, "zcs_t1")
	if err != nil || got.ID != c.ID || len(got.Domains) != 1 || got.PassEnabled {
		t.Fatalf("ByKey: %v %+v", err, got)
	}
	if by, err := s.GetCaptchaSiteBySecretHash(ctx, "h1"); err != nil || by.ID != c.ID {
		t.Fatalf("BySecretHash: %v", err)
	}
	if _, err := s.GetCaptchaSiteByKey(ctx, "yok"); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("ErrNotFound beklenir: %v", err)
	}

	name, diff, en, ttl := "Blog2", 20, true, 60
	up, err := s.UpdateCaptchaSite(ctx, tn, c.ID, store.CaptchaSitePatch{Name: &name, Difficulty: &diff, SetDomains: true, PassEnabled: &en, PassTTLMinutes: &ttl})
	if err != nil || up.Name != "Blog2" || up.Difficulty != 20 || len(up.Domains) != 0 || !up.PassEnabled || up.PassTTLMinutes != 60 {
		t.Fatalf("Update: %v %+v", err, up)
	}
	if _, err := s.UpdateCaptchaSite(ctx, "baska", c.ID, store.CaptchaSitePatch{Name: &name}); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("baska kiraci guncelleyemez: %v", err)
	}
	rot, err := s.RotateCaptchaSiteSecret(ctx, tn, c.ID, "h9")
	if err != nil || rot.SecretHash != "h9" {
		t.Fatalf("Rotate: %v", err)
	}
	if _, err := s.GetCaptchaSiteBySecretHash(ctx, "h1"); !errors.Is(err, store.ErrNotFound) {
		t.Error("eski ozet bulunmamali")
	}

	now := time.Now().UTC()
	d := store.CaptchaCounterDelta{SiteID: c.ID, Challenge: 2, Solve: 1, VerifyOK: 1, Renewals: 3, UniquePasses: 2}
	for i := 0; i < 2; i++ {
		if err := s.AddCaptchaCounters(ctx, now, []store.CaptchaCounterDelta{d, {SiteID: "platform", Challenge: 1}}); err != nil {
			t.Fatal(err)
		}
	}
	rows, err := s.CaptchaCounters(ctx, []string{c.ID, "platform"}, now.AddDate(0, 0, -6))
	if err != nil || len(rows) != 2 {
		t.Fatalf("Counters: %v %+v", err, rows)
	}
	for _, r := range rows {
		if r.SiteID == c.ID && (r.Challenge != 4 || r.Renewals != 6 || r.UniquePasses != 4 || r.VerifyOK != 2) {
			t.Errorf("upsert toplami: %+v", r)
		}
	}
	if err := s.PruneCaptchaCounters(ctx, now.AddDate(0, 0, 1)); err != nil {
		t.Fatal(err)
	}
	if rows, _ := s.CaptchaCounters(ctx, []string{c.ID}, now.AddDate(0, 0, -6)); len(rows) != 0 {
		t.Error("budama calismadi")
	}

	_ = s.AddCaptchaCounters(ctx, now, []store.CaptchaCounterDelta{d})
	if err := s.DeleteCaptchaSite(ctx, "baska", c.ID); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("baska kiraci silemez: %v", err)
	}
	if err := s.DeleteCaptchaSite(ctx, tn, c.ID); err != nil {
		t.Fatal(err)
	}
	if rows, _ := s.CaptchaCounters(ctx, []string{c.ID}, now.AddDate(0, 0, -6)); len(rows) != 0 {
		t.Error("silinen sitenin sayaclari kalmamali")
	}
	if l, _ := s.ListCaptchaSites(ctx, tn); len(l) != 0 {
		t.Error("liste bos olmali")
	}
}
