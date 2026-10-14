package reqlog

import (
	"bytes"
	"context"
	"crypto/rand"
	"log/slog"
	"sync"
	"testing"
	"time"
)

type memBackend struct {
	mu   sync.Mutex
	recs map[string]*CaptureRecord
	put  int
}

func newMemBackend() *memBackend { return &memBackend{recs: map[string]*CaptureRecord{}} }

func (m *memBackend) PutCaptures(_ context.Context, rs []*CaptureRecord) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, r := range rs {
		m.recs[r.ID] = r
		m.put++
	}
	return nil
}
func (m *memBackend) GetCapture(_ context.Context, tenantID, id string) (*CaptureRecord, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	r, ok := m.recs[id]
	if !ok || (tenantID != "" && r.TenantID != tenantID) {
		return nil, ErrCaptureNotFound
	}
	return r, nil
}
func (m *memBackend) DeleteTenantCaptures(_ context.Context, t string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	for id, r := range m.recs {
		if r.TenantID == t {
			delete(m.recs, id)
		}
	}
	return nil
}
func (m *memBackend) CaptureUsage(context.Context) (map[string]int64, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := map[string]int64{}
	for _, r := range m.recs {
		out[r.TenantID] += r.StoredBytes
	}
	return out, nil
}
func (m *memBackend) count() int { m.mu.Lock(); defer m.mu.Unlock(); return len(m.recs) }

func testCodec(t *testing.T) *CaptureCodec {
	t.Helper()
	k := make([]byte, 32)
	_, _ = rand.Read(k)
	c, err := NewCaptureCodec(k)
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func sampleCapture(id, tenant string, body []byte) *Capture {
	return &Capture{ID: id, TenantID: tenant, TunnelID: "tun_1", Hostname: "a.test", ClientIP: "1.2.3.4",
		TS: time.Now().UTC().Truncate(time.Microsecond), Method: "POST", Path: "/x", Query: "a=1", Status: 201,
		ReqHeaders:  map[string][]string{"Authorization": {"Bearer s3cret"}},
		RespHeaders: map[string][]string{"Content-Type": {"text/plain"}},
		ReqBody:     body, RespBody: []byte("ok"), ReqBodyTruncated: true, DurationMS: 7}
}

func TestCodecRoundTripAndCompression(t *testing.T) {
	k := testCodec(t)
	big := bytes.Repeat([]byte("zorven "), 5000) // sikistirilabilir
	c := sampleCapture("req_1", "ten_a", big)
	rec, err := k.Encode(c)
	if err != nil {
		t.Fatal(err)
	}
	if !rec.Compressed || len(rec.ReqBody) >= len(big)/4 {
		t.Fatalf("sikistirma yok: compressed=%v boyut=%d", rec.Compressed, len(rec.ReqBody))
	}
	if bytes.Contains(rec.ReqBody, []byte("zorven")) || bytes.Contains(rec.ReqHeaders, []byte("s3cret")) {
		t.Fatal("duz metin sizdi")
	}
	got, err := k.Decode(rec)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got.ReqBody, big) || string(got.RespBody) != "ok" || got.ReqHeaders["Authorization"][0] != "Bearer s3cret" ||
		got.Path != "/x" || got.Status != 201 || got.DurationMS != 7 || !got.ReqBodyTruncated || !got.TS.Equal(c.TS) {
		t.Fatalf("gidis-donus bozuk: %+v", got)
	}
	// Baska kiraciya kopyalanan satir cozulemez (AAD).
	rec.TenantID = "ten_b"
	if _, err := k.Decode(rec); err == nil {
		t.Fatal("kiraci degisince cozulmemeli")
	}
	// Yanlis anahtar.
	rec.TenantID = "ten_a"
	if _, err := testCodec(t).Decode(rec); err == nil {
		t.Fatal("yanlis anahtarla cozulmemeli")
	}
}

func TestCodecSmallAndEmptyBody(t *testing.T) {
	k := testCodec(t)
	c := sampleCapture("req_2", "t", nil)
	c.RespBody = nil
	rec, err := k.Encode(c)
	if err != nil {
		t.Fatal(err)
	}
	if rec.ReqBody != nil || rec.Compressed {
		t.Fatal("bos govde NULL olmali")
	}
	got, err := k.Decode(rec)
	if err != nil || len(got.ReqBody) != 0 {
		t.Fatalf("decode: %v", err)
	}
	c = sampleCapture("req_3", "t", []byte("kucuk"))
	rec, _ = k.Encode(c)
	if got, err = k.Decode(rec); err != nil || string(got.ReqBody) != "kucuk" {
		t.Fatal("kucuk govde")
	}
}

func TestNewCaptureCodecBadKey(t *testing.T) {
	if _, err := NewCaptureCodec([]byte("kisa")); err == nil {
		t.Fatal("kisa anahtar reddedilmeli")
	}
}

func TestNoKeyDisablesPersistence(t *testing.T) {
	s := NewCaptureStore(10)
	b := newMemBackend()
	if _, ok := s.AttachBackend(b, nil, CapturePersistConfig{}, slog.Default()); ok {
		t.Fatal("anahtarsiz kalici yakalama acilmamali")
	}
	s.SetEnabled("t", true)
	s.Put(sampleCapture("req_a", "t", []byte("x")))
	time.Sleep(20 * time.Millisecond)
	if b.count() != 0 {
		t.Fatal("anahtarsiz yazim olmamali")
	}
	if _, ok := s.Get("t", "req_a"); !ok {
		t.Fatal("bellek onbellegi calismali")
	}
}

func startPersist(t *testing.T, cfg CapturePersistConfig, b *memBackend) *CaptureStore {
	t.Helper()
	s := NewCaptureStore(2) // kucuk LRU: tahliye DB okumasini zorlar
	cfg.FlushInterval = 10 * time.Millisecond
	run, ok := s.AttachBackend(b, testCodec(t), cfg, slog.Default())
	if !ok {
		t.Fatal("attach")
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { run(ctx); close(done) }()
	t.Cleanup(func() { cancel(); <-done })
	return s
}

func waitFor(t *testing.T, f func() bool) {
	t.Helper()
	for i := 0; i < 200; i++ {
		if f() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("zaman asimi")
}

func TestPersistAndGetFromBackendAfterEviction(t *testing.T) {
	b := newMemBackend()
	s := startPersist(t, CapturePersistConfig{}, b)
	s.SetEnabled("t", true)
	for _, id := range []string{"req_1", "req_2", "req_3", "req_4"} {
		s.Put(sampleCapture(id, "t", []byte("govde-"+id)))
	}
	waitFor(t, func() bool { return b.count() == 4 })
	// req_1 LRU'dan dustu; DB'den gelmeli.
	s.mu.RLock()
	_, inCache := s.items["req_1"]
	s.mu.RUnlock()
	if inCache {
		t.Fatal("req_1 onbellekten dusmeliydi")
	}
	c, ok := s.Get("t", "req_1")
	if !ok || string(c.ReqBody) != "govde-req_1" || c.ReqHeaders["Authorization"][0] != "Bearer s3cret" {
		t.Fatalf("DB'den okunamadi: %v %+v", ok, c)
	}
	if _, ok := s.Get("baska", "req_1"); ok {
		t.Fatal("baska kiraci goremez")
	}
}

func TestTenantQuotaStopsPersisting(t *testing.T) {
	b := newMemBackend()
	s := startPersist(t, CapturePersistConfig{TenantQuotaBytes: 1}, b)
	s.SetEnabled("t", true)
	s.Put(sampleCapture("req_1", "t", []byte("a")))
	waitFor(t, func() bool { return b.count() == 1 })
	// Kullanim artik kotayi astı: sonraki kayitlar yazilmaz.
	s.Put(sampleCapture("req_2", "t", []byte("b")))
	time.Sleep(60 * time.Millisecond)
	if b.count() != 1 || s.Stats().QuotaDropped != 1 {
		t.Fatalf("kota uygulanmadi: n=%d stats=%+v", b.count(), s.Stats())
	}
	// Baska kiracinin kotasi ayri.
	s.SetEnabled("u", true)
	s.Put(sampleCapture("req_3", "u", []byte("c")))
	waitFor(t, func() bool { return b.count() == 2 })
}

func TestGlobalLimitPauses(t *testing.T) {
	b := newMemBackend()
	s := startPersist(t, CapturePersistConfig{GlobalLimitBytes: 1}, b)
	s.SetEnabled("t", true)
	s.Put(sampleCapture("req_1", "t", []byte("a")))
	waitFor(t, func() bool { return s.Stats().Paused })
	s.Put(sampleCapture("req_2", "t", []byte("b")))
	time.Sleep(60 * time.Millisecond)
	if b.count() != 1 {
		t.Fatalf("global tavanda yeni yakalama durmali: %d", b.count())
	}
}

func TestQueueFullDropsAndCounts(t *testing.T) {
	b := newMemBackend()
	s := NewCaptureStore(10)
	// run baslatilmiyor: kuyruk dolacak.
	if _, ok := s.AttachBackend(b, testCodec(t), CapturePersistConfig{QueueSize: 2}, slog.Default()); !ok {
		t.Fatal("attach")
	}
	s.SetEnabled("t", true)
	done := make(chan struct{})
	go func() {
		for i := 0; i < 5; i++ {
			s.Put(sampleCapture("req_"+string(rune('a'+i)), "t", []byte("x")))
		}
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("Put bloklandi")
	}
	if s.Stats().Dropped != 3 {
		t.Fatalf("dusen = %d, beklenen 3", s.Stats().Dropped)
	}
}

func TestDisableDeletesPersisted(t *testing.T) {
	b := newMemBackend()
	s := startPersist(t, CapturePersistConfig{}, b)
	s.SetEnabled("t", true)
	s.Put(sampleCapture("req_1", "t", []byte("a")))
	waitFor(t, func() bool { return b.count() == 1 })
	s.SetEnabled("t", false)
	waitFor(t, func() bool { return b.count() == 0 })
	if _, ok := s.Get("t", "req_1"); ok {
		t.Fatal("kapatinca silinmeli")
	}
}

func TestCacheByteBudgetEvicts(t *testing.T) {
	s := NewCaptureStore(100)
	s.SetLimits(0, 1000)
	for _, id := range []string{"req_1", "req_2", "req_3"} {
		s.Put(sampleCapture(id, "t", bytes.Repeat([]byte("x"), 600)))
	}
	if _, ok := s.Get("t", "req_1"); ok {
		t.Fatal("bayt butcesi asilinca en eski dusmeli")
	}
	if _, ok := s.Get("t", "req_3"); !ok {
		t.Fatal("en yeni kalmali")
	}
	if s.MaxBody() != DefaultCaptureMaxBody {
		t.Fatal("varsayilan govde tavani")
	}
}
