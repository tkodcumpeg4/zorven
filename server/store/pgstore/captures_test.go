package pgstore

import (
	"bytes"
	"context"
	"crypto/rand"
	"errors"
	"testing"
	"time"

	"github.com/tkodcumpeg4/zorven/server/reqlog"
)

func capCodec(t *testing.T) *reqlog.CaptureCodec {
	t.Helper()
	k := make([]byte, 32)
	_, _ = rand.Read(k)
	c, err := reqlog.NewCaptureCodec(k)
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func putCap(t *testing.T, s *Store, k *reqlog.CaptureCodec, id, tenant string, ts time.Time, body []byte) {
	t.Helper()
	rec, err := k.Encode(&reqlog.Capture{ID: id, TenantID: tenant, TunnelID: "tun_1", TS: ts, Method: "POST", Path: "/p",
		ReqHeaders: map[string][]string{"Authorization": {"Bearer z"}}, ReqBody: body, RespBody: []byte("ok"), Status: 200})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.PutCaptures(context.Background(), []*reqlog.CaptureRecord{rec}); err != nil {
		t.Fatalf("PutCaptures: %v", err)
	}
}

func TestCaptures_PutGetUsageDelete(t *testing.T) {
	ctx := context.Background()
	s := freshStore(t)
	k := capCodec(t)
	a, _ := s.CreateTenant(ctx, "cap-a")
	b, _ := s.CreateTenant(ctx, "cap-b")
	big := bytes.Repeat([]byte("govde "), 4000)
	now := time.Now().UTC().Truncate(time.Microsecond)
	putCap(t, s, k, "cap-1", a.ID, now, big)
	putCap(t, s, k, "cap-2", b.ID, now, []byte("kucuk"))
	// ayni id tekrar: yok sayilir
	putCap(t, s, k, "cap-1", a.ID, now, []byte("farkli"))

	rec, err := s.GetCapture(ctx, a.ID, "cap-1")
	if err != nil {
		t.Fatalf("GetCapture: %v", err)
	}
	c, err := k.Decode(rec)
	if err != nil || !bytes.Equal(c.ReqBody, big) || c.ReqHeaders["Authorization"][0] != "Bearer z" || !c.TS.Equal(now) {
		t.Fatalf("gidis-donus bozuk: %v", err)
	}
	if !rec.Compressed {
		t.Error("buyuk govde sikistirilmis olmali")
	}
	// DB'de duz metin yok
	var leaked bool
	if err := s.pool.QueryRow(ctx, `SELECT position('Bearer' in req_headers::text) > 0 OR position('govde' in encode(req_body,'escape')) > 0 FROM request_captures WHERE id='cap-1'`).Scan(&leaked); err != nil || leaked {
		t.Fatalf("duz metin sizdi: %v %v", leaked, err)
	}
	// kiraci kapsami
	if _, err := s.GetCapture(ctx, b.ID, "cap-1"); !errors.Is(err, reqlog.ErrCaptureNotFound) {
		t.Fatalf("baska kiraci gormemeli: %v", err)
	}
	if _, err := s.GetCapture(ctx, "", "yok"); !errors.Is(err, reqlog.ErrCaptureNotFound) {
		t.Fatalf("yok: %v", err)
	}
	u, err := s.CaptureUsage(ctx)
	if err != nil || u[a.ID] <= 0 || u[b.ID] <= 0 || u[a.ID] <= u[b.ID] {
		t.Fatalf("usage: %v %v", u, err)
	}
	if err := s.DeleteTenantCaptures(ctx, a.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.GetCapture(ctx, a.ID, "cap-1"); !errors.Is(err, reqlog.ErrCaptureNotFound) {
		t.Fatal("silinmeliydi")
	}
	if _, err := s.GetCapture(ctx, b.ID, "cap-2"); err != nil {
		t.Fatal("diger kiraci etkilenmemeli")
	}
}

func TestCaptures_RetentionAndOrgDelete(t *testing.T) {
	ctx := context.Background()
	s := freshStore(t)
	k := capCodec(t)
	if _, err := s.pool.Exec(ctx, `DELETE FROM request_captures`); err != nil {
		t.Fatal(err)
	}
	ten, _ := s.CreateTenant(ctx, "cap-ret")
	now := time.Now().UTC()
	putCap(t, s, k, "ret-old", ten.ID, now.Add(-48*time.Hour), []byte("x"))
	putCap(t, s, k, "ret-new", ten.ID, now.Add(-time.Hour), []byte("y"))
	putCap(t, s, k, "ret-orphan", "yok-kiraci", now.Add(-48*time.Hour), []byte("z"))

	res, err := s.PruneRetention(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if res.Captures != 2 {
		t.Fatalf("2 yakalama silinmeli (eski + sahipsiz): %+v", res)
	}
	if _, err := s.GetCapture(ctx, ten.ID, "ret-new"); err != nil {
		t.Fatal("yeni kayit kalmali")
	}
	if _, err := s.GetCapture(ctx, ten.ID, "ret-old"); err == nil {
		t.Fatal("eski kayit silinmeli")
	}

	// kiraci silme
	if _, err := s.DeleteOrganization(ctx, ten.ID); err != nil {
		t.Fatalf("DeleteOrganization: %v", err)
	}
	var n int
	if err := s.pool.QueryRow(ctx, `SELECT count(*) FROM request_captures WHERE tenant_id=$1`, ten.ID).Scan(&n); err != nil || n != 0 {
		t.Fatalf("kiraci silinince yakalamalar da gitmeli: %d %v", n, err)
	}
}

func TestCaptures_MoveTablespace(t *testing.T) {
	ctx := context.Background()
	s := freshStore(t)
	if err := s.MoveCaptureTablespace(ctx, "olmayan_ts"); err == nil {
		t.Fatal("olmayan tablespace hata vermeli")
	}
	if err := s.MoveCaptureTablespace(ctx, ""); err != nil {
		t.Fatal("bos ad no-op olmali")
	}
	// pg_default her kurulumda vardir; tasima sorgulari calismali.
	if err := s.MoveCaptureTablespace(ctx, "pg_default"); err != nil {
		t.Fatalf("pg_default: %v", err)
	}
}
