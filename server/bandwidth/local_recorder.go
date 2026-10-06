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
	states   map[string]*cachedState

	deltasMu sync.Mutex
	// deltas: donem ("2006-01") -> tenantID -> [bytesIn, bytesOut]. Donem
	// anahtari, ay sonunda biriken trafigin yeni aya yazilmasini onler.
	deltas map[string]map[string][2]int64

	stopChan chan struct{}
	once     sync.Once

	// refreshEvery, onbellekteki kiraci durumunun DB'den (plan, kota, kullanim)
	// yeniden yuklenme araligi. Plan/kota degisimi ve deneme bitisi en gec bu
	// sure sonra uygulanir; ay donumu ise aninda (donem anahtari degisince).
	refreshEvery time.Duration
	// now, testlerde saati sabitlemek icin.
	now func() time.Time
}

// stateRefreshInterval, varsayilan periyodik tazeleme araligi.
const stateRefreshInterval = time.Minute

// cachedState, kiracinin canli durumu + ne zaman/hangi donem icin yuklendigi.
type cachedState struct {
	state    *TenantRuntimeState
	period   string // "2006-01"
	loadedAt time.Time
}

func NewLocalRecorder(st store.Store) *LocalUsageRecorder {
	r := &LocalUsageRecorder{
		st:       st,
		states:   make(map[string]*cachedState),
		deltas:   make(map[string]map[string][2]int64),
		stopChan: make(chan struct{}),

		refreshEvery: stateRefreshInterval,
		now:          time.Now,
	}
	go r.flushLoop()
	return r
}

// fresh, onbellek kaydinin hala gecerli olup olmadigini soyler.
func (r *LocalUsageRecorder) fresh(c *cachedState, period string, now time.Time) bool {
	return c != nil && c.period == period && now.Sub(c.loadedAt) < r.refreshEvery
}

// Invalidate, kiracinin onbellekteki durumunu atar; bir sonraki istek planı,
// kotayi ve kullanimi DB'den yeniden yukler (plan degisimi sonrasi aninda etki).
func (r *LocalUsageRecorder) Invalidate(tenantID string) {
	r.statesMu.Lock()
	delete(r.states, tenantID)
	r.statesMu.Unlock()
}

// GetOrCreateState, kiracinin bellek ici durumunu doner; yoksa ya da bayatsa
// (refreshEvery doldu / ay dondu) DB'den yeniden yukler.
func (r *LocalUsageRecorder) GetOrCreateState(ctx context.Context, tenantID string) (*TenantRuntimeState, error) {
	now := r.now().UTC()
	period := now.Format("2006-01")

	r.statesMu.RLock()
	c := r.states[tenantID]
	r.statesMu.RUnlock()
	if r.fresh(c, period, now) {
		return c.state, nil
	}

	r.statesMu.Lock()
	defer r.statesMu.Unlock()

	// Double-check lock
	c = r.states[tenantID]
	if r.fresh(c, period, now) {
		return c.state, nil
	}

	sub, err := r.st.GetSubscription(ctx, tenantID)
	if err != nil {
		// DB gecici olarak erisilemezse ayni donem icindeki eski durumu koru
		// (fail-stale); bir sonraki denemeyi refreshEvery kadar ertele.
		if c != nil && c.period == period {
			c.loadedAt = now
			return c.state, nil
		}
		return nil, err
	}

	bytesIn, bytesOut, _ := r.st.GetBandwidthUsage(ctx, tenantID, period)
	usedSoFar := bytesIn + bytesOut
	// Henuz DB'ye aktarilmamis (10 sn tamponu) tuketim de sayilir; yoksa
	// tazeleme kullanimi geriye sarar ve kota gec uygulanir.
	r.deltasMu.Lock()
	pending := r.deltas[period][tenantID]
	r.deltasMu.Unlock()
	usedSoFar += pending[0] + pending[1]

	// Open-core / self-host varsayilani SINIRSIZ: acik bir plan (PlanDetails)
	// tanimli degilse 0 gecilir -> NewTenantRuntimeState throttle uygulamaz.
	// Boylece plan tablosu seed edilmemis bir self-host kurulumu sessizce
	// yavaslatilmaz. Yalnizca gercek bir plan hiz siniri tanimlarsa throttle olur.
	normalMbps := int64(0)
	throttledMbps := int64(0)

	if sub.PlanDetails != nil {
		normalMbps = int64(sub.PlanDetails.BandwidthNormalMbps)
		throttledMbps = int64(sub.PlanDetails.BandwidthThrottledMbps)
	}

	s := NewTenantRuntimeState(tenantID, sub.Plan, 0 /* acik surum: aylik trafik limiti yok */, usedSoFar, normalMbps, throttledMbps)
	r.states[tenantID] = &cachedState{state: s, period: period, loadedAt: now}
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
	period := r.now().UTC().Format("2006-01")
	r.deltasMu.Lock()
	byTenant := r.deltas[period]
	if byTenant == nil {
		byTenant = make(map[string][2]int64)
		r.deltas[period] = byTenant
	}
	cur := byTenant[event.TenantID]
	cur[0] += event.BytesIn
	cur[1] += event.BytesOut
	byTenant[event.TenantID] = cur
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
	r.deltas = make(map[string]map[string][2]int64)
	r.deltasMu.Unlock()

	var firstErr error
	for period, byTenant := range batchCopy {
		if err := r.st.FlushBandwidthDeltas(ctx, period, byTenant); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
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
