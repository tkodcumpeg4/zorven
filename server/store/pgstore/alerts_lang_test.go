package pgstore

import (
	"context"
	"testing"

	"github.com/tkodcumpeg4/zorven/server/store"
)

// Metrik uyarisi e-postasinin dili (tunnel_alerts.notify_lang, migration 0112): kurulum
// aninda yazilir, dil sinyali olmayan yeniden kayitta (bos dil) mevcut deger korunur,
// degerlendirici listesinde gelir.
func TestTunnelAlertNotifyLang(t *testing.T) {
	ctx := context.Background()
	s := freshStore(t)
	tn := store.DefaultTenantID

	c, err := s.CreateClient(ctx, tn, "alert-pc", "tok_alang1", "hash_alang1")
	if err != nil {
		t.Fatalf("CreateClient: %v", err)
	}
	tun, err := s.CreateTunnel(ctx, tn, c.ID, "http://localhost:8000")
	if err != nil {
		t.Fatalf("CreateTunnel: %v", err)
	}
	base := store.TunnelAlert{TunnelID: tun.ID, Enabled: true, ErrorRatePct: 20, WindowMin: 5, MinRequests: 10, NotifyEmail: "ops@example.com"}

	// Hic kayit yokken dil bos (varsayilan tr).
	if a, err := s.GetTunnelAlert(ctx, tn, tun.ID); err != nil || a.NotifyLang != "" {
		t.Fatalf("kayitsiz uyari: %v %+v", err, a)
	}

	// Ilk kayit: dil yazilir.
	first := base
	first.NotifyLang = "ja"
	if err := s.SetTunnelAlert(ctx, tn, first); err != nil {
		t.Fatalf("SetTunnelAlert: %v", err)
	}
	if a, _ := s.GetTunnelAlert(ctx, tn, tun.ID); a.NotifyLang != "ja" || a.NotifyEmail != "ops@example.com" {
		t.Fatalf("dil yazilmadi: %+v", a)
	}

	// Dil sinyali olmayan yeniden kayit ('') mevcut dili korur; diger alanlar guncellenir.
	again := base
	again.ErrorRatePct = 30
	if err := s.SetTunnelAlert(ctx, tn, again); err != nil {
		t.Fatalf("SetTunnelAlert (sinyalsiz): %v", err)
	}
	if a, _ := s.GetTunnelAlert(ctx, tn, tun.ID); a.NotifyLang != "ja" || a.ErrorRatePct != 30 {
		t.Fatalf("mevcut dil korunmaliydi: %+v", a)
	}

	// Yeni dil sinyali eskisini degistirir; degerlendirici listesinde gorunur.
	zh := base
	zh.NotifyLang = "zh"
	if err := s.SetTunnelAlert(ctx, tn, zh); err != nil {
		t.Fatalf("SetTunnelAlert (zh): %v", err)
	}
	list, err := s.ListEnabledAlerts(ctx)
	if err != nil {
		t.Fatalf("ListEnabledAlerts: %v", err)
	}
	var found bool
	for _, a := range list {
		if a.TunnelID == tun.ID {
			found = true
			if a.NotifyLang != "zh" {
				t.Fatalf("degerlendirici listesinde dil = %q, beklenen zh", a.NotifyLang)
			}
		}
	}
	if !found {
		t.Fatal("etkin uyari listede yok")
	}
}
