package bandwidth

import (
	"context"
	"sync/atomic"
	"time"

	"golang.org/x/time/rate"
)

// UsageSource, Batcher'in ihtiyac duydugu yuzey (*LocalUsageRecorder uygular).
// Testler sahte bir kaynak verebilsin diye ayridir.
type UsageSource interface {
	GetOrCreateState(ctx context.Context, tenantID string) (*TenantRuntimeState, error)
	Record(ctx context.Context, event UsageEvent) error
}

const (
	// batchFlushBytes: bu kadar bayt birikince hemen kaydedilir.
	batchFlushBytes = 64 * 1024
	// batchFlushEvery: bu sureden eski birikim bir sonraki Add'de kaydedilir.
	batchFlushEvery = time.Second
	// stateRefreshEvery: kiraci durumu (limiter) Batcher icinde bu sure onbelleklenir.
	stateRefreshEvery = time.Second
)

// Batcher, ham TCP/UDP/WireGuard veri duzlemi icin kiraci kullanimini sicak
// yolda yalnizca atomik islemlerle toplar; belirli esikte (64 KB / 1 sn) ve
// Flush'ta tek bir Record cagrisi yapar. nil Batcher guvenlidir (no-op).
type Batcher struct {
	src    UsageSource
	tenant string
	typ    UsageType

	in, out atomic.Int64
	lastNs  atomic.Int64 // son kayit (unix nano)

	state   atomic.Pointer[TenantRuntimeState]
	stateNs atomic.Int64
}

// NewBatcher, kiraci icin Batcher kurar. src nil ya da tenant bos ise nil doner
// (olcum/sinir yok).
func NewBatcher(src UsageSource, tenantID string, typ UsageType) *Batcher {
	if src == nil || tenantID == "" {
		return nil
	}
	b := &Batcher{src: src, tenant: tenantID, typ: typ}
	b.lastNs.Store(time.Now().UnixNano())
	return b
}

// Add, gelen/giden baytlari biriktirir; esik asildiysa kaydeder.
func (b *Batcher) Add(in, out int64) {
	if b == nil {
		return
	}
	if in > 0 {
		b.in.Add(in)
	}
	if out > 0 {
		b.out.Add(out)
	}
	if b.in.Load()+b.out.Load() >= batchFlushBytes ||
		time.Now().UnixNano()-b.lastNs.Load() >= int64(batchFlushEvery) {
		b.Flush()
	}
}

// Flush, biriken baytlari kullanim kaydina yazar (kapanista cagrilmali).
func (b *Batcher) Flush() {
	if b == nil {
		return
	}
	b.lastNs.Store(time.Now().UnixNano())
	in, out := b.in.Swap(0), b.out.Swap(0)
	if in == 0 && out == 0 {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = b.src.Record(ctx, UsageEvent{TenantID: b.tenant, Type: b.typ, BytesIn: in, BytesOut: out, Timestamp: time.Now()})
}

// Limiter, kiracinin hiz sinirlayicisini doner; sinirsiz plan (enterprise) ya da
// durum okunamazsa nil (sinir yok). Durum 1 sn onbelleklenir.
func (b *Batcher) Limiter(ctx context.Context) *rate.Limiter {
	if b == nil {
		return nil
	}
	now := time.Now().UnixNano()
	st := b.state.Load()
	if st == nil || now-b.stateNs.Load() >= int64(stateRefreshEvery) {
		if ns, err := b.src.GetOrCreateState(ctx, b.tenant); err == nil && ns != nil {
			st = ns
			b.state.Store(ns)
		}
		b.stateNs.Store(now)
	}
	if st == nil {
		return nil
	}
	return st.GetLimiter()
}

// WaitN, lim nil degilse n bayt icin belirtecin gelmesini bekler (burst
// boyutunda parcalar halinde). ctx iptalinde hata doner.
func WaitN(ctx context.Context, lim *rate.Limiter, n int) error {
	if lim == nil {
		return nil
	}
	for n > 0 {
		burst := lim.Burst()
		if burst <= 0 {
			burst = 32 * 1024
		}
		c := n
		if c > burst {
			c = burst
		}
		if err := lim.WaitN(ctx, c); err != nil {
			return err
		}
		n -= c
	}
	return nil
}

// AllowN, bekleme yapmadan n bayt icin belirtec almaya calisir (datagram icin).
// lim nil ise ya da n burst'u asiyorsa (hic karsilanamaz) true doner.
func AllowN(lim *rate.Limiter, n int) bool {
	if lim == nil || n <= 0 || n > lim.Burst() {
		return true
	}
	return lim.AllowN(time.Now(), n)
}
