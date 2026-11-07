package pgstore

import (
	"context"
	"testing"
	"time"

	"github.com/tkodcumpeg4/zorven/server/reqlog"
)

// Istemci IP filtresi: tam IP birebir, eksik adres onek olarak eslenir.
func TestRequestLogs_ClientIPFilter(t *testing.T) {
	ctx := context.Background()
	s := freshStore(t)
	ten, err := s.CreateTenant(ctx, "rl-ip")
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	mk := func(ip string) reqlog.Entry {
		return reqlog.Entry{ID: reqlog.NewID(), TenantID: ten.ID, TunnelID: "tun_ip", Hostname: "a.test", Method: "GET", Path: "/", Status: 200, ClientIP: ip, TS: now}
	}
	if err := s.InsertRequestLogs(ctx, []reqlog.Entry{mk("84.38.241.118"), mk("84.38.241.11"), mk("10.0.0.1"), mk("2001:db8::1")}); err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct {
		q    string
		want int
	}{{"84.38.241.11", 1}, {"84.38.", 2}, {"10.0.0.1", 1}, {"2001:db8::1", 1}, {"9.9.9.9", 0}, {"84_38", 0}} {
		got, err := s.QueryRequestLogs(ctx, reqlog.Filter{TenantID: ten.ID, ClientIP: c.q})
		if err != nil || len(got) != c.want {
			t.Errorf("ip=%q: %d kayit, beklenen %d (hata %v)", c.q, len(got), c.want, err)
		}
	}
}
