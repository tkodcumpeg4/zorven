package pgstore

import (
	"context"
	"testing"
	"time"

	"github.com/tkodcumpeg4/zorven/server/reqlog"
)

// NUL bayti / gecersiz UTF-8 iceren tek kayit ayni batch'teki digerlerini dusurmemeli.
func TestInsertRequestLogs_SanitizesAndKeepsBatch(t *testing.T) {
	ctx := context.Background()
	s := freshStore(t)
	ten, err := s.CreateTenant(ctx, "rl-sanitize")
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	entries := []reqlog.Entry{
		{ID: reqlog.NewID(), TenantID: ten.ID, Hostname: "a.test", Method: "GET", Path: "/ok", Status: 200, TS: now},
		{ID: reqlog.NewID(), TenantID: ten.ID, Hostname: "a.test", Method: "GET", Path: "/bad\x00path\xff", Status: 404, TS: now},
		{ID: reqlog.NewID(), TenantID: ten.ID, Hostname: "a.test", Method: "GET", Path: "/ok2", Status: 200, TS: now},
	}
	if err := s.InsertRequestLogs(ctx, entries); err != nil {
		t.Fatalf("InsertRequestLogs: %v", err)
	}
	var n int
	if err := s.pool.QueryRow(ctx, `SELECT count(*) FROM request_logs WHERE tenant_id=$1`, ten.ID).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 3 {
		t.Fatalf("3 kayit yazilmali, %d", n)
	}
}
