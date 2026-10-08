package pgstore

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/tkodcumpeg4/zorven/server/accesslog"
)

func TestRetentionDays_Env(t *testing.T) {
	t.Setenv("ZORVEN_LOG_RETENTION_DAYS", "")
	if got := RetentionDays(); got != DefaultRetentionDays {
		t.Fatalf("varsayilan %d bekleniyordu, %d", DefaultRetentionDays, got)
	}
	t.Setenv("ZORVEN_LOG_RETENTION_DAYS", "7")
	if got := RetentionDays(); got != 7 {
		t.Fatalf("7 bekleniyordu, %d", got)
	}
	t.Setenv("ZORVEN_LOG_RETENTION_DAYS", "abc")
	if got := RetentionDays(); got != DefaultRetentionDays {
		t.Fatalf("gecersiz deger varsayilana dusmeli, %d", got)
	}
}

// Postgres gerektirir (ZORVEN_TEST_PG_DSN yoksa atlanir).
func TestPruneRetention_FixedDays(t *testing.T) {
	ctx := context.Background()
	s := freshStore(t)
	if _, err := s.pool.Exec(ctx, `DELETE FROM request_logs; DELETE FROM udp_stats_minute`); err != nil {
		t.Fatalf("temizlik: %v", err)
	}
	ten, err := s.CreateTenant(ctx, "ret-fixed")
	if err != nil {
		t.Fatalf("CreateTenant: %v", err)
	}
	now := time.Now().UTC()
	day := 24 * time.Hour
	for i, age := range []time.Duration{2 * day, 10 * day, 40 * day} {
		if _, err := s.pool.Exec(ctx,
			`INSERT INTO request_logs (id, tenant_id, ts) VALUES ($1,$2,$3)`,
			fmt.Sprintf("rl-fixed-%d", i), ten.ID, now.Add(-age)); err != nil {
			t.Fatalf("insert: %v", err)
		}
	}
	res, err := s.pruneRetentionAt(ctx, now, 30)
	if err != nil {
		t.Fatalf("prune: %v", err)
	}
	if res.RequestLogs != 1 {
		t.Fatalf("yalniz 40 gunluk silinmeli: %+v", res)
	}
	// days<=0: temizlik kapali
	if res, _ := s.pruneRetentionAt(ctx, now.Add(1000*day), 0); res.RequestLogs != 0 {
		t.Fatalf("0 gun = kapali olmali: %+v", res)
	}
}

// Ziyaretci erisim olaylari da ayni sabit sureye tabidir ve web-door izinleri
// bitisinden 24 saat sonra silinir. Postgres gerektirir.
func TestPruneRetention_AccessEventsFixedDays(t *testing.T) {
	ctx := context.Background()
	s := freshStore(t)
	if _, err := s.pool.Exec(ctx, `DELETE FROM tunnel_access_events`); err != nil {
		t.Fatalf("temizlik: %v", err)
	}
	ten, err := s.CreateTenant(ctx, "ae-fixed")
	if err != nil {
		t.Fatalf("CreateTenant: %v", err)
	}
	now := time.Now().UTC()
	day := 24 * time.Hour
	evs := []accesslog.Event{
		{ID: "ae_f1", TenantID: ten.ID, TunnelID: "t", Method: "basic", CreatedAt: now.Add(-time.Hour)},
		{ID: "ae_f2", TenantID: ten.ID, TunnelID: "t", Method: "basic", CreatedAt: now.Add(-10 * day)},
		{ID: "ae_f3", TenantID: ten.ID, TunnelID: "t", Method: "basic", CreatedAt: now.Add(-40 * day)},
	}
	if err := s.InsertAccessEvents(ctx, evs); err != nil {
		t.Fatal(err)
	}
	res, err := s.pruneRetentionAt(ctx, now, 30)
	if err != nil {
		t.Fatalf("prune: %v", err)
	}
	if res.AccessEvents != 1 {
		t.Fatalf("yalniz 40 gunluk silinmeli: %+v", res)
	}
}
