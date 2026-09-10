package bandwidth

import (
	"context"
	"sync"
	"time"

	"github.com/tkodcumpeg4/zorven/server/store"
)

// LocalUsageRecorder, tek sunucu node'u icin bellek ici atomik sayac
// ve periyodik veritabani aktarimi (10 saniye batch flush) saglayan kayitci.
type LocalUsageRecorder struct {
	st store.Store

	statesMu sync.RWMutex
	states   map[string]*TenantRuntimeState

	deltasMu sync.Mutex
	deltas   map[string][2]int64 // tenantID -> [bytesIn, bytesOut]

	stopChan chan struct{}
	once     sync.Once
}

func NewLocalRecorder(st store.Store) *LocalUsageRecorder {
	r := &LocalUsageRecorder{
		st:       st,
		states:   make(map[string]*TenantRuntimeState),
		deltas:   make(map[string][2]int64),
		stopChan: make(chan struct{}),
	}
	go r.flushLoop()
	return r
}

// GetOrCreateState, kiracinin bellek ici durumunu doner; yoksa DB'den yukler (warm-up).
func (r *LocalUsageRecorder) GetOrCreateState(ctx context.Context, tenantID string) (*TenantRuntimeState, error) {
	r.statesMu.RLock()
	s, ok := r.states[tenantID]
	r.statesMu.RUnlock()
	if ok {
		return s, nil
	}

	r.statesMu.Lock()
	defer r.statesMu.Unlock()

	// Double-check lock
	if s, ok := r.states[tenantID]; ok {
		return s, nil
	}

	sub, err := r.st.GetSubscription(ctx, tenantID)
	if err != nil {
		return nil, err
	}

	currentPeriod := time.Now().UTC().Format("2006-01")
	bytesIn, bytesOut, _ := r.st.GetBandwidthUsage(ctx, tenantID, currentPeriod)
	usedSoFar := bytesIn + bytesOut

	normalMbps := int64(10)
	throttledMbps := int64(1)

	if sub.PlanDetails != nil {
		normalMbps = int64(sub.PlanDetails.BandwidthNormalMbps)
		throttledMbps = int64(sub.PlanDetails.BandwidthThrottledMbps)
	}

	s = NewTenantRuntimeState(tenantID, sub.Plan, sub.BandwidthLimitBytes, usedSoFar, normalMbps, throttledMbps)
	r.states[tenantID] = s
	return s, nil
}

// Record, bir bant genisligi tuketim olayini isler.
func (r *LocalUsageRecorder) Record(ctx context.Context, event UsageEvent) error {
	if event.TenantID == "" {
		return nil
	}

	state, err := r.GetOrCreateState(ctx, event.TenantID)
	if err != nil {
		return err
	}

	// 1. Bellek sayacini atomik guncelle ve kota asimini kontrol et
	state.Record(event.BytesIn, event.BytesOut)

	// 2. 10 saniyelik DB flush tamponuna ekle
	r.deltasMu.Lock()
	cur := r.deltas[event.TenantID]
	cur[0] += event.BytesIn
	cur[1] += event.BytesOut
	r.deltas[event.TenantID] = cur
	r.deltasMu.Unlock()

	return nil
}

// Flush, bellekte biriken trafik farklarini veritabanina yazar.
func (r *LocalUsageRecorder) Flush(ctx context.Context) error {
	r.deltasMu.Lock()
	if len(r.deltas) == 0 {
		r.deltasMu.Unlock()
		return nil
	}
	batchCopy := r.deltas
	r.deltas = make(map[string][2]int64)
	r.deltasMu.Unlock()

	currentPeriod := time.Now().UTC().Format("2006-01")
	return r.st.FlushBandwidthDeltas(ctx, currentPeriod, batchCopy)
}

func (r *LocalUsageRecorder) flushLoop() {
	ticker := time.NewTicker(10 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-r.stopChan:
			return
		case <-ticker.C:
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			_ = r.Flush(ctx)
			cancel()
		}
	}
}

// Close, toplayiciyi durdurur ve bekleyen son verileri DB'ye aktarir.
func (r *LocalUsageRecorder) Close() error {
	var err error
	r.once.Do(func() {
		close(r.stopChan)
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		err = r.Flush(ctx)
	})
	return err
}
