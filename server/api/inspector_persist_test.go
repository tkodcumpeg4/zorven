package api

import (
	"context"
	"crypto/rand"
	"log/slog"
	"sync"
	"testing"
	"time"

	"github.com/tkodcumpeg4/zorven/server/reqlog"
)

// Yalniz GetCapture/PutCaptures kullanan minimal bellek ici kalici depo.
type fakeCaptureBackend struct {
	mu   sync.Mutex
	recs map[string]*reqlog.CaptureRecord
}

func (f *fakeCaptureBackend) PutCaptures(_ context.Context, rs []*reqlog.CaptureRecord) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, r := range rs {
		f.recs[r.ID] = r
	}
	return nil
}
func (f *fakeCaptureBackend) GetCapture(_ context.Context, tenantID, id string) (*reqlog.CaptureRecord, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	r, ok := f.recs[id]
	if !ok || (tenantID != "" && r.TenantID != tenantID) {
		return nil, reqlog.ErrCaptureNotFound
	}
	return r, nil
}
func (f *fakeCaptureBackend) DeleteTenantCaptures(context.Context, string) error { return nil }
func (f *fakeCaptureBackend) CaptureUsage(context.Context) (map[string]int64, error) {
	return map[string]int64{}, nil
}

// LRU'dan dusen yakalama inspector tarafindan DB'den okunur ve maskeleme aynen calisir.
func TestCaptureForReadsFromPersistentBackend(t *testing.T) {
	b := &fakeCaptureBackend{recs: map[string]*reqlog.CaptureRecord{}}
	store := reqlog.NewCaptureStore(1)
	key := make([]byte, 32)
	_, _ = rand.Read(key)
	codec, _ := reqlog.NewCaptureCodec(key)
	run, ok := store.AttachBackend(b, codec, reqlog.CapturePersistConfig{FlushInterval: 10 * time.Millisecond}, slog.Default())
	if !ok {
		t.Fatal("attach")
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go run(ctx)
	store.SetEnabled("ten", true)
	store.Put(&reqlog.Capture{ID: "req_1", TenantID: "ten", TunnelID: "tun", TS: time.Now().UTC(), Method: "GET", Path: "/a",
		ReqHeaders: map[string][]string{"Authorization": {"Bearer abc"}}, ReqBody: []byte("govde")})
	store.Put(&reqlog.Capture{ID: "req_2", TenantID: "ten", TS: time.Now().UTC()}) // req_1'i LRU'dan atar

	s := &Server{Captures: store}
	var c *reqlog.Capture
	for i := 0; i < 200; i++ {
		if c, ok = s.captureFor("ten", "req_1"); ok {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if !ok || string(c.ReqBody) != "govde" {
		t.Fatalf("DB'den okunamadi: %v", ok)
	}
	if _, ok := s.captureFor("baska", "req_1"); ok {
		t.Fatal("baska kiraci goremez")
	}
	member := captureOut(c, false)
	if member["req_headers"].(map[string][]string)["Authorization"][0] != maskedHeaderValue {
		t.Fatal("DB'den gelen yakalamada da maskeleme calismali")
	}
	if captureOut(c, true)["req_headers"].(map[string][]string)["Authorization"][0] != "Bearer abc" {
		t.Fatal("owner icin acik olmali")
	}
}
