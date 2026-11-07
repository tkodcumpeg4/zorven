package api

import (
	"testing"
	"time"

	"github.com/tkodcumpeg4/zorven/server/events"
	"github.com/tkodcumpeg4/zorven/server/store"
)

// Suresi YENI dolan gecici tunel: silinmez; yonlendirme tazelenir, kiraciya
// tunnel.updated (expired_at dolu) gider, istemciye tunelsiz config itilir.
func TestTunnelsExpiredNowPublishesUpdateAndPushes(t *testing.T) {
	now := time.Now().UTC()
	exp := store.Tunnel{ID: "tun_exp", TenantID: "ten_a", ClientID: "cli_a", Target: "http://localhost:3000", Ephemeral: true, ExpiredAt: &now}
	keep := store.Tunnel{ID: "tun_keep", TenantID: "ten_a", ClientID: "cli_a", Target: "http://localhost:8080", Enabled: true}
	// exp pasif (Enabled=false): config_update'e girmez.
	srv, _, ag, evCh, changed := newExpireEnv(t, []store.Tunnel{exp, keep})

	srv.TunnelsExpiredNow([]store.Tunnel{exp})

	if *changed != 1 {
		t.Fatalf("yonlendirme bir kez tazelenmeli, alinan %d", *changed)
	}
	ev := nextEvent(t, evCh)
	if ev.Type != events.TypeTunnelUpdated || ev.Tenant != "ten_a" {
		t.Fatalf("tunnel.updated/ten_a bekleniyordu: %+v", ev)
	}
	if tn, ok := ev.Data.(store.Tunnel); !ok || tn.ID != "tun_exp" || tn.ExpiredAt == nil {
		t.Fatalf("olay verisi expired_at dolu tunel olmali: %#v", ev.Data)
	}
	ups := ag.waitUpdates(t, 1)
	if len(ups[0]) != 1 || ups[0][0] != "tun_keep" {
		t.Fatalf("config_update yalniz etkin tuneli icermeli: %v", ups)
	}
	srv.TunnelsExpiredNow(nil) // bos tur: yan etki yok
	if *changed != 1 {
		t.Fatal("bos turda tazeleme olmamali")
	}
}
