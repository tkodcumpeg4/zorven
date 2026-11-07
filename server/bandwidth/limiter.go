package bandwidth

import (
	"context"
	"io"
	"net/http"
	"sync"
	"sync/atomic"

	"golang.org/x/time/rate"
)

// TenantRuntimeState, kiracinin bellek icinde tutulan canli durumu.
// Sicak yolda (hot path) her istekte PostgreSQL'e gitmemek icin
// O(1) hizinda atomik limit ve throttling kontrolu saglar.
type TenantRuntimeState struct {
	TenantID              string
	PlanID                string
	MonthlyLimitBytes     int64
	BytesUsedThisMonth    atomic.Int64
	NormalRateBytesSec    int64
	ThrottledRateBytesSec int64

	// Zorven Apps (0106): uygulama tunelleri (zapp://) icin aylik kota. Uygulama
	// trafigi hem toplam sayaca hem bu sayaca yazilir. Kota asilinca YALNIZ
	// uygulama tunelleri ayri, kisitli hizli bir limiter'a duser (diger tuneller
	// genel kotaya gore calismaya devam eder). 0 = sinirsiz.
	AppMonthlyLimitBytes int64
	AppBytesUsed         atomic.Int64
	appThrottled         atomic.Bool
	appLimiter           *rate.Limiter

	limiter     *rate.Limiter
	isThrottled atomic.Bool
	mu          sync.RWMutex
}

// PlanEnterprise, sinirsiz (throttle uygulanmayan) plan kimligi.
const PlanEnterprise = "enterprise"

// NewTenantRuntimeState, kiraci icin bellek ici calisma durumu olusturur.
func NewTenantRuntimeState(tenantID, planID string, monthlyLimit, usedSoFar, normalMbps, throttledMbps int64) *TenantRuntimeState {
	// SINIRSIZ durumlar (throttle uygulanmaz, limiter nil kalir -> GetLimiter()
	// nil -> ingress ThrottledWriter'i atlar, passthrough):
	//   1) planID == enterprise (ozel teklif).
	//   2) normalMbps <= 0: acik bir hiz siniri tanimlanmamis. Open-core /
	//      self-host varsayilani budur -> kimseyi sessizce yavaslatmayiz.
	// Bu, kapali surumdeki "eksik veri -> 10 Mbps'e kis" davranisindan bilerek
	// ayrilir: open-core'da varsayilan SINIRSIZ olmalidir. Aylik kota da bu
	// durumda sinirsizdir, MonthlyLimitBytes 0 birakilir.
	if planID == PlanEnterprise || normalMbps <= 0 {
		s := &TenantRuntimeState{
			TenantID: tenantID,
			PlanID:   planID,
		}
		s.BytesUsedThisMonth.Store(usedSoFar)
		return s
	}

	// Mbps -> bytes/sec donusumu (1 Mbps = 1,000,000 / 8 = 125,000 byte/s)
	normalBytesSec := normalMbps * 125000
	throttledBytesSec := throttledMbps * 125000

	if throttledBytesSec <= 0 {
		throttledBytesSec = 1 * 125000 // min 1 Mbps
	}

	burst := int(normalBytesSec / 2)
	if burst < 64*1024 {
		burst = 64 * 1024 // min 64 KB burst
	}

	lim := rate.NewLimiter(rate.Limit(normalBytesSec), burst)

	s := &TenantRuntimeState{
		TenantID:              tenantID,
		PlanID:                planID,
		MonthlyLimitBytes:     monthlyLimit,
		NormalRateBytesSec:    normalBytesSec,
		ThrottledRateBytesSec: throttledBytesSec,
		limiter:               lim,
	}
	s.BytesUsedThisMonth.Store(usedSoFar)

	// Eger kiraci sunucu acildiginda zaten kotayi asmissa aninda throttled baslat
	if monthlyLimit > 0 && usedSoFar >= monthlyLimit {
		s.isThrottled.Store(true)
		s.limiter.SetLimit(rate.Limit(throttledBytesSec))
	}

	return s
}

// Record, kiracinin harcadigi bayt miktarini atomik olarak ekler.
// Kota asildiginda hizi O(1) aninda ThrottledRateBytesSec seviyesine indirir.
func (s *TenantRuntimeState) Record(bytesIn, bytesOut int64) {
	s.RecordFor(bytesIn, bytesOut, false)
}

// SetAppQuota, uygulama trafigi kotasini ve su ana kadarki kullanimi yukler
// (durum olusturulurken, yayinlanmadan once cagrilir). limit<=0 = sinirsiz.
// Enterprise'da (limiter nil) kota uygulanmaz.
func (s *TenantRuntimeState) SetAppQuota(limit, used int64) {
	s.AppBytesUsed.Store(used)
	if limit <= 0 || s.limiter == nil {
		return
	}
	s.AppMonthlyLimitBytes = limit
	bps := s.ThrottledRateBytesSec
	burst := int(bps / 2)
	if burst < 64*1024 {
		burst = 64 * 1024
	}
	s.appLimiter = rate.NewLimiter(rate.Limit(bps), burst)
	if used >= limit {
		s.appThrottled.Store(true)
	}
}

// IsAppThrottled, uygulama trafigi kotasinin asilip asilmadigi.
func (s *TenantRuntimeState) IsAppThrottled() bool { return s.appThrottled.Load() }

// LimiterFor, trafigin turune gore hiz sinirlayicisini doner: uygulama tuneli ve
// uygulama kotasi asilmissa kisitli uygulama limiter'i, aksi halde kiracinin
// genel limiter'i (nil = sinirsiz). Uygulama kotasi asilmisken genel kota da
// asilmissa ikisi de ayni kisitli hizdadir.
func (s *TenantRuntimeState) LimiterFor(app bool) *rate.Limiter {
	if app && s.appLimiter != nil && s.appThrottled.Load() {
		return s.appLimiter
	}
	return s.limiter
}

// RecordFor, Record'un uygulama farkindalikli hali: app=true ise baytlar
// uygulama sayacina da yazilir ve uygulama kotasi asildiysa uygulama limiter'i
// kisitli hiza gecer.
func (s *TenantRuntimeState) RecordFor(bytesIn, bytesOut int64, app bool) {
	total := bytesIn + bytesOut
	if total <= 0 {
		return
	}
	if app {
		newApp := s.AppBytesUsed.Add(total)
		if s.AppMonthlyLimitBytes > 0 && newApp >= s.AppMonthlyLimitBytes {
			s.appThrottled.Store(true)
		}
	}
	newTotal := s.BytesUsedThisMonth.Add(total)

	if s.MonthlyLimitBytes > 0 && newTotal >= s.MonthlyLimitBytes {
		if !s.isThrottled.Load() {
			s.isThrottled.Store(true)
			s.mu.Lock()
			if s.limiter != nil { // enterprise'da limiter nil (sinirsiz)
				s.limiter.SetLimit(rate.Limit(s.ThrottledRateBytesSec))
			}
			s.mu.Unlock()
		}
	}
}

func (s *TenantRuntimeState) IsThrottled() bool {
	return s.isThrottled.Load()
}

func (s *TenantRuntimeState) GetLimiter() *rate.Limiter {
	return s.limiter
}

// ThrottledWriter, io.Writer ve http.ResponseWriter sarmalayarak veri akisini
// hedeflenen bytes/sec (token-bucket) hizinda sinirlandirir.
type ThrottledWriter struct {
	ctx context.Context
	w   io.Writer
	lim *rate.Limiter
}

func NewThrottledWriter(ctx context.Context, w io.Writer, lim *rate.Limiter) *ThrottledWriter {
	return &ThrottledWriter{
		ctx: ctx,
		w:   w,
		lim: lim,
	}
}

func (tw *ThrottledWriter) Write(p []byte) (int, error) {
	if tw.lim == nil || len(p) == 0 {
		return tw.w.Write(p)
	}

	totalWritten := 0
	for len(p) > 0 {
		burst := tw.lim.Burst()
		if burst <= 0 {
			burst = 32 * 1024
		}
		chunkSize := len(p)
		if chunkSize > burst {
			chunkSize = burst
		}

		if err := tw.lim.WaitN(tw.ctx, chunkSize); err != nil {
			return totalWritten, err
		}

		n, err := tw.w.Write(p[:chunkSize])
		totalWritten += n
		if err != nil {
			return totalWritten, err
		}
		p = p[n:]
	}
	return totalWritten, nil
}

// http.ResponseWriter ve http.Flusher yeteneklerini gecir

func (tw *ThrottledWriter) Header() http.Header {
	if hw, ok := tw.w.(http.ResponseWriter); ok {
		return hw.Header()
	}
	return nil
}

func (tw *ThrottledWriter) WriteHeader(statusCode int) {
	if hw, ok := tw.w.(http.ResponseWriter); ok {
		hw.WriteHeader(statusCode)
	}
}

func (tw *ThrottledWriter) Flush() {
	if f, ok := tw.w.(http.Flusher); ok {
		f.Flush()
	}
}
