package pgstore

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/tkodcumpeg4/zorven/server/accesslog"
	"github.com/tkodcumpeg4/zorven/server/store"
)

func TestAccessEvents_InsertListSummary(t *testing.T) {
	ctx := context.Background()
	s := freshStore(t)
	if _, err := s.pool.Exec(ctx, `DELETE FROM tunnel_access_events`); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC().Truncate(time.Microsecond)
	mk := func(i int, tunnel, method, provider, ident string, ok bool, reason, ip string, age time.Duration) accesslog.Event {
		return accesslog.Event{
			ID: fmt.Sprintf("ae_t%02d", i), TenantID: store.DefaultTenantID, TunnelID: tunnel,
			Hostname: "h.example.com", Method: method, Provider: provider, Identity: ident,
			Success: ok, Reason: reason, ClientIP: ip, UserAgent: "ua", CreatedAt: now.Add(-age),
		}
	}
	evs := []accesslog.Event{
		mk(1, "tun_a", "basic", "", "alice", true, "ok", "1.1.1.1", time.Minute),
		mk(2, "tun_a", "basic", "", "alice", false, "bad_credentials", "1.1.1.1", 2*time.Minute),
		mk(3, "tun_a", "basic", "", "bob", false, "bad_credentials", "2.2.2.2", 3*time.Minute),
		mk(4, "tun_a", "oauth", "google", "g@x.y", true, "ok", "3.3.3.3", 4*time.Minute),
		mk(5, "tun_a", "oauth", "github", "h@x.y", false, "email_not_allowed", "3.3.3.3", 2*24*time.Hour),
		mk(6, "tun_b", "basic", "", "zed", true, "ok", "9.9.9.9", time.Minute),
	}
	// NUL ve gecersiz UTF-8 yazimi dusurmemeli; uzun UA kesilir.
	evs = append(evs, accesslog.Event{ID: "ae_t07", TenantID: store.DefaultTenantID, TunnelID: "tun_a",
		Method: "basic", Identity: "a\x00b\xff", UserAgent: strings.Repeat("u", 900), CreatedAt: now.Add(-time.Hour)})
	if err := s.InsertAccessEvents(ctx, evs); err != nil {
		t.Fatalf("InsertAccessEvents: %v", err)
	}
	// Tekrar yazim (ayni id) hata vermez.
	if err := s.InsertAccessEvents(ctx, evs[:1]); err != nil {
		t.Fatalf("tekrar yazim: %v", err)
	}

	// Sayfalama: 3'erli, en yeniden eskiye.
	var seen []string
	var beforeTS time.Time
	beforeID := ""
	for page := 0; page < 5; page++ {
		got, err := s.ListAccessEvents(ctx, store.DefaultTenantID, "tun_a", 3, beforeTS, beforeID)
		if err != nil {
			t.Fatal(err)
		}
		if len(got) == 0 {
			break
		}
		for _, e := range got {
			seen = append(seen, e.ID)
		}
		last := got[len(got)-1]
		beforeTS, beforeID = last.CreatedAt, last.ID
	}
	want := []string{"ae_t01", "ae_t02", "ae_t03", "ae_t04", "ae_t07", "ae_t05"}
	if strings.Join(seen, ",") != strings.Join(want, ",") {
		t.Errorf("sayfalama sirasi = %v, want %v", seen, want)
	}
	// Baska kiracinin/tunelin satiri sizmaz.
	other, _ := s.ListAccessEvents(ctx, "baska-kiraci", "tun_a", 10, time.Time{}, "")
	if len(other) != 0 {
		t.Error("kiraci izolasyonu bozuk")
	}
	var ua string
	if err := s.pool.QueryRow(ctx, `SELECT user_agent FROM tunnel_access_events WHERE id='ae_t07'`).Scan(&ua); err != nil || len(ua) > accesslog.MaxUserAgent {
		t.Errorf("UA kesilmeli: len=%d err=%v", len(ua), err)
	}

	// Ozet: son 24 saat -> t01..t04 + t07 (5), t05 pencere disi.
	sum, err := s.AccessEventSummary(ctx, store.DefaultTenantID, "tun_a", now.Add(-24*time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if sum.Total != 5 || sum.Success != 2 || sum.Failure != 3 {
		t.Errorf("toplam/basari/hata = %d/%d/%d", sum.Total, sum.Success, sum.Failure)
	}
	if sum.ByMethod["basic"] != 4 || sum.ByMethod["oauth"] != 1 || sum.ByProvider["google"] != 1 {
		t.Errorf("kirilimlar: %v %v", sum.ByMethod, sum.ByProvider)
	}
	if sum.UniqueIdentities != 4 || sum.UniqueIPs != 3 {
		t.Errorf("benzersiz kimlik/ip = %d/%d", sum.UniqueIdentities, sum.UniqueIPs)
	}
	if sum.ByReason["bad_credentials"] != 2 {
		t.Errorf("by_reason: %v", sum.ByReason)
	}
}
