package reqlog

import (
	"context"
	"errors"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"
)

// CaptureBackend, kalici yakalama deposu arayuzudur (bugun Postgres; ileride
// S3 vb. ayni arayuzle eklenebilir). Kayitlar zaten sifreli gelir.
type CaptureBackend interface {
	PutCaptures(ctx context.Context, recs []*CaptureRecord) error
	// GetCapture bulunamazsa ErrCaptureNotFound doner. tenantID "" ise kiraci
	// suzmesi yapilmaz.
	GetCapture(ctx context.Context, tenantID, id string) (*CaptureRecord, error)
	DeleteTenantCaptures(ctx context.Context, tenantID string) error
	// CaptureUsage, kiraci basina saklanan bayt toplami.
	CaptureUsage(ctx context.Context) (map[string]int64, error)
}

// ErrCaptureNotFound, kalici depoda kayit yok.
var ErrCaptureNotFound = errors.New("yakalama bulunamadi")

// CapturePersistConfig, kalici yakalama ayarlari.
type CapturePersistConfig struct {
	TenantQuotaBytes int64 // kiraci basina tavan; <=0 sinirsiz
	GlobalLimitBytes int64 // toplam tavan; <=0 sinirsiz
	QueueSize        int   // varsayilan 1024
	BatchSize        int   // varsayilan 50
	FlushInterval    time.Duration
	UsageRefresh     time.Duration
}

type capturePersist struct {
	store   *CaptureStore
	backend CaptureBackend
	codec   *CaptureCodec
	cfg     CapturePersistConfig
	log     *slog.Logger
	ch      chan *Capture

	mu     sync.Mutex
	usage  map[string]int64
	global int64
	paused bool

	dropped      atomic.Int64 // kuyruk dolu
	quotaDropped atomic.Int64 // kiraci kotasi / global tavan
	written      atomic.Int64
}

// CaptureStats, kalici yakalama sayaclari.
type CaptureStats struct {
	Dropped, QuotaDropped, Written, GlobalBytes int64
	Paused                                      bool
}

// AttachBackend, kalici depoyu baglar. codec nil ise (anahtar yok) kalici
// yakalama KAPALI kalir: sifresiz yazim yoktur. Donen fonksiyon yazma dongusudur
// (goroutine'de calistirin); ctx iptalinde kuyrugu bosaltir.
func (s *CaptureStore) AttachBackend(b CaptureBackend, codec *CaptureCodec, cfg CapturePersistConfig, log *slog.Logger) (run func(ctx context.Context), ok bool) {
	if s == nil || b == nil {
		return nil, false
	}
	if log == nil {
		log = slog.Default()
	}
	if codec == nil {
		log.Warn("kalici istek govdesi yakalama KAPALI: ZORVEN_SECRET_KEY tanimli degil (sifresiz yazim yapilmaz); yalniz bellek onbellegi")
		return nil, false
	}
	if cfg.QueueSize <= 0 {
		cfg.QueueSize = 1024
	}
	if cfg.BatchSize <= 0 {
		cfg.BatchSize = 50
	}
	if cfg.FlushInterval <= 0 {
		cfg.FlushInterval = 2 * time.Second
	}
	if cfg.UsageRefresh <= 0 {
		cfg.UsageRefresh = time.Minute
	}
	p := &capturePersist{store: s, backend: b, codec: codec, cfg: cfg, log: log,
		ch: make(chan *Capture, cfg.QueueSize), usage: map[string]int64{}}
	s.mu.Lock()
	s.persist = p
	s.mu.Unlock()
	return p.run, true
}

// Stats, kalici yakalama sayaclarini doner (kalici depo yoksa sifir).
func (s *CaptureStore) Stats() CaptureStats {
	if s == nil {
		return CaptureStats{}
	}
	s.mu.RLock()
	p := s.persist
	s.mu.RUnlock()
	if p == nil {
		return CaptureStats{}
	}
	p.mu.Lock()
	g, paused := p.global, p.paused
	p.mu.Unlock()
	return CaptureStats{Dropped: p.dropped.Load(), QuotaDropped: p.quotaDropped.Load(), Written: p.written.Load(), GlobalBytes: g, Paused: paused}
}

// enqueue sicak yolda cagrilir: asla bloklamaz.
func (p *capturePersist) enqueue(c *Capture) {
	if !p.admit(c.TenantID) {
		p.quotaDropped.Add(1)
		return
	}
	select {
	case p.ch <- c:
	default:
		p.dropped.Add(1)
	}
}

func (p *capturePersist) admit(tenantID string) bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.paused {
		return false
	}
	if q := p.cfg.TenantQuotaBytes; q > 0 && p.usage[tenantID] >= q {
		return false
	}
	return true
}

func (p *capturePersist) get(tenantID, id string) (*Capture, bool) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	rec, err := p.backend.GetCapture(ctx, tenantID, id)
	if err != nil {
		if !errors.Is(err, ErrCaptureNotFound) {
			p.log.Warn("yakalama okunamadi", "id", id, "hata", err)
		}
		return nil, false
	}
	c, err := p.codec.Decode(rec)
	if err != nil {
		p.log.Warn("yakalama cozulemedi (anahtar degismis olabilir)", "id", id, "hata", err)
		return nil, false
	}
	return c, true
}

func (p *capturePersist) deleteTenant(tenantID string) {
	p.mu.Lock()
	g := p.global - p.usage[tenantID]
	if g < 0 {
		g = 0
	}
	p.global = g
	delete(p.usage, tenantID)
	p.mu.Unlock()
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
		defer cancel()
		if err := p.backend.DeleteTenantCaptures(ctx, tenantID); err != nil {
			p.log.Warn("kiracinin kalici yakalamalari silinemedi", "tenant", tenantID, "hata", err)
		}
	}()
}

func (p *capturePersist) refreshUsage(ctx context.Context) {
	cctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	u, err := p.backend.CaptureUsage(cctx)
	if err != nil {
		p.log.Warn("yakalama kullanimi okunamadi", "hata", err)
		return
	}
	var total int64
	for _, v := range u {
		total += v
	}
	p.mu.Lock()
	p.usage, p.global = u, total
	wasPaused := p.paused
	p.paused = p.cfg.GlobalLimitBytes > 0 && total >= p.cfg.GlobalLimitBytes
	nowPaused := p.paused
	p.mu.Unlock()
	if nowPaused && !wasPaused {
		p.log.Warn("kalici yakalama DURAKLATILDI: global depolama tavani asildi", "kullanim_bayt", total, "tavan_bayt", p.cfg.GlobalLimitBytes)
	} else if !nowPaused && wasPaused {
		p.log.Info("kalici yakalama yeniden basladi", "kullanim_bayt", total)
	}
}

func (p *capturePersist) run(ctx context.Context) {
	p.refreshUsage(ctx)
	flushT := time.NewTicker(p.cfg.FlushInterval)
	usageT := time.NewTicker(p.cfg.UsageRefresh)
	defer flushT.Stop()
	defer usageT.Stop()
	batch := make([]*Capture, 0, p.cfg.BatchSize)
	flush := func() {
		if len(batch) > 0 {
			p.flush(batch)
			batch = batch[:0]
		}
	}
	for {
		select {
		case <-ctx.Done():
			for {
				select {
				case c := <-p.ch:
					batch = append(batch, c)
					if len(batch) >= p.cfg.BatchSize {
						flush()
					}
				default:
					flush()
					return
				}
			}
		case c := <-p.ch:
			batch = append(batch, c)
			if len(batch) >= p.cfg.BatchSize {
				flush()
			}
		case <-flushT.C:
			flush()
		case <-usageT.C:
			p.refreshUsage(ctx)
		}
	}
}

func (p *capturePersist) flush(batch []*Capture) {
	recs := make([]*CaptureRecord, 0, len(batch))
	for _, c := range batch {
		// Kapatilmis kiracinin kuyrukta kalan kayitlari yazilmaz.
		if !p.store.Enabled(c.TenantID) {
			continue
		}
		r, err := p.codec.Encode(c)
		if err != nil {
			p.log.Warn("yakalama sifrelenemedi", "id", c.ID, "hata", err)
			continue
		}
		recs = append(recs, r)
	}
	if len(recs) == 0 {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	if err := p.backend.PutCaptures(ctx, recs); err != nil {
		p.log.Warn("yakalamalar kalici yazilamadi", "adet", len(recs), "hata", err)
		return
	}
	p.written.Add(int64(len(recs)))
	p.mu.Lock()
	for _, r := range recs {
		p.usage[r.TenantID] += r.StoredBytes
		p.global += r.StoredBytes
	}
	if p.cfg.GlobalLimitBytes > 0 && p.global >= p.cfg.GlobalLimitBytes && !p.paused {
		p.paused = true
		p.log.Warn("kalici yakalama DURAKLATILDI: global depolama tavani asildi", "kullanim_bayt", p.global, "tavan_bayt", p.cfg.GlobalLimitBytes)
	}
	p.mu.Unlock()
}
