package bandwidth

import (
	"context"
	"sync"
	"testing"
	"time"
)

type meterSrc struct {
	mu      sync.Mutex
	state   *TenantRuntimeState
	in, out int64
	calls   int
}

func (s *meterSrc) GetOrCreateState(context.Context, string) (*TenantRuntimeState, error) {
	return s.state, nil
}

func (s *meterSrc) Record(_ context.Context, e UsageEvent) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.in += e.BytesIn
	s.out += e.BytesOut
	s.calls++
	return nil
}

func TestBatcherAggregatesAndFlushes(t *testing.T) {
	src := &meterSrc{state: NewTenantRuntimeState("t", "enterprise", 0, 0, 0, 0)}
	b := NewBatcher(src, "t", UsageTypeTCP)
	for i := 0; i < 100; i++ {
		b.Add(100, 50) // 15 KB toplam: esik (64 KB) ve 1 sn altinda
	}
	src.mu.Lock()
	if src.calls != 0 {
		t.Fatalf("esik alti cagri olmamali: %d", src.calls)
	}
	src.mu.Unlock()
	b.Flush()
	if src.in != 10000 || src.out != 5000 || src.calls != 1 {
		t.Fatalf("in=%d out=%d calls=%d", src.in, src.out, src.calls)
	}
	b.Add(70*1024, 0) // esik: aninda kayit
	if src.calls != 2 {
		t.Fatalf("esikte kayit beklenirdi: %d", src.calls)
	}
}

func TestBatcherNilAndUnlimited(t *testing.T) {
	var b *Batcher
	b.Add(1, 1)
	b.Flush()
	if b.Limiter(context.Background()) != nil {
		t.Fatal("nil batcher limiter nil olmali")
	}
	src := &meterSrc{state: NewTenantRuntimeState("t", "enterprise", 0, 0, 0, 0)}
	if NewBatcher(src, "t", UsageTypeUDP).Limiter(context.Background()) != nil {
		t.Fatal("enterprise sinirsiz olmali")
	}
	if NewBatcher(nil, "t", UsageTypeUDP) != nil || NewBatcher(src, "", UsageTypeUDP) != nil {
		t.Fatal("kaynak/kiraci yoksa nil beklenir")
	}
}

func TestWaitNRespectsContext(t *testing.T) {
	st := NewTenantRuntimeState("t", "free", 0, 0, 10, 1)
	lim := st.GetLimiter()
	lim.SetLimit(10)
	lim.SetBurst(10)
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	if err := WaitN(ctx, lim, 1000); err == nil {
		t.Fatal("iptalde hata beklenirdi")
	}
	if !AllowN(nil, 5) {
		t.Fatal("nil limiter izin vermeli")
	}
}
