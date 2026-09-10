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

	limiter     *rate.Limiter
	isThrottled atomic.Bool
	mu          sync.RWMutex
}

// NewTenantRuntimeState, kiraci icin bellek ici calisma durumu olusturur.
func NewTenantRuntimeState(tenantID, planID string, monthlyLimit, usedSoFar, normalMbps, throttledMbps int64) *TenantRuntimeState {
	// Mbps -> bytes/sec donusumu (1 Mbps = 1,000,000 / 8 = 125,000 byte/s)
	normalBytesSec := normalMbps * 125000
	throttledBytesSec := throttledMbps * 125000

	if normalBytesSec <= 0 {
		normalBytesSec = 10 * 125000 // min 10 Mbps
	}
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
	total := bytesIn + bytesOut
	if total <= 0 {
		return
	}
	newTotal := s.BytesUsedThisMonth.Add(total)

	if s.MonthlyLimitBytes > 0 && newTotal >= s.MonthlyLimitBytes {
		if !s.isThrottled.Load() {
			s.isThrottled.Store(true)
			s.mu.Lock()
			s.limiter.SetLimit(rate.Limit(s.ThrottledRateBytesSec))
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
