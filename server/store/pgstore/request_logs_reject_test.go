package pgstore

import (
	"context"
	"testing"
	"time"

	"github.com/tkodcumpeg4/zorven/server/reqlog"
)

// reject_reason yazilir, filtrelenir ve gecikme metriklerine katilmaz.
func TestRequestLogs_RejectReason(t *testing.T) {
	ctx := context.Background()
	s := freshStore(t)
	ten, err := s.CreateTenant(ctx, "rl-reject")
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	entries := []reqlog.Entry{
		{ID: reqlog.NewID(), TenantID: ten.ID, TunnelID: "tun_r", Hostname: "a.test", Method: "GET", Path: "/ok", Status: 200, DurationMS: 100, TS: now},
		{ID: reqlog.NewID(), TenantID: ten.ID, TunnelID: "tun_r", Hostname: "a.test", Method: "GET", Path: "/blocked", Status: 403, DurationMS: 900, RejectReason: "ip_forbidden", TS: now},
		{ID: reqlog.NewID(), TenantID: ten.ID, TunnelID: "tun_r", Hostname: "a.test", Method: "GET", Path: "/off", Status: 502, RejectReason: "client_offline", TS: now},
	}
	if err := s.InsertRequestLogs(ctx, entries); err != nil {
		t.Fatalf("InsertRequestLogs: %v", err)
	}

	all, err := s.QueryRequestLogs(ctx, reqlog.Filter{TenantID: ten.ID})
	if err != nil || len(all) != 3 {
		t.Fatalf("hepsi 3 olmali: %d %v", len(all), err)
	}
	rej, err := s.QueryRequestLogs(ctx, reqlog.Filter{TenantID: ten.ID, Rejected: true})
	if err != nil || len(rej) != 2 {
		t.Fatalf("reddedilenler 2 olmali: %d %v", len(rej), err)
	}
	for _, e := range rej {
		if e.RejectReason == "" {
			t.Errorf("reject_reason bos: %+v", e)
		}
	}
	one, err := s.QueryRequestLogs(ctx, reqlog.Filter{TenantID: ten.ID, Reason: "ip_forbidden"})
	if err != nil || len(one) != 1 || one[0].Path != "/blocked" || one[0].RejectReason != "ip_forbidden" {
		t.Fatalf("reason filtresi: %+v %v", one, err)
	}

	// Metrik: sayim 3, ama ortalama/maks sure yalnizca proxy'lenen istegin (100ms).
	b, err := s.TunnelMetrics(ctx, ten.ID, "tun_r", now.Add(-time.Hour), 3600)
	if err != nil || len(b) != 1 {
		t.Fatalf("TunnelMetrics: %v %v", b, err)
	}
	if b[0].Count != 3 || b[0].MaxMs != 100 || b[0].AvgMs != 100 {
		t.Errorf("reddedilenler gecikmeye katilmamali: %+v", b[0])
	}
}
