package bandwidth_test

import (
	"bytes"
	"context"
	"testing"
	"time"

	"github.com/tkodcumpeg4/zorven/server/bandwidth"
	"github.com/tkodcumpeg4/zorven/server/store/pgstore"
	"golang.org/x/time/rate"
)

func TestThrottledWriter_SpeedLimiting(t *testing.T) {
	ctx := context.Background()
	// Limit: 50 KB/s, burst: 10 KB
	lim := rate.NewLimiter(rate.Limit(50*1024), 10*1024)

	var buf bytes.Buffer
	tw := bandwidth.NewThrottledWriter(ctx, &buf, lim)

	data := make([]byte, 20*1024) // 20 KB
	start := time.Now()
	n, err := tw.Write(data)
	duration := time.Since(start)

	if err != nil {
		t.Fatalf("Write failed: %v", err)
	}
	if n != len(data) {
		t.Errorf("Written byte count mismatch: got %d, want %d", n, len(data))
	}
	// 20 KB @ 50 KB/s with 10 KB burst should take at least ~150-200ms
	if duration < 100*time.Millisecond {
		t.Errorf("Throttling did not slow down write as expected, duration: %v", duration)
	}
}

func TestTenantRuntimeState_ThrottlesOnLimit(t *testing.T) {
	// 1000 byte limit, 10 Mbps normal, 1 Mbps throttled
	state := bandwidth.NewTenantRuntimeState("ten_test", "free", 1000, 0, 10, 1)

	if state.IsThrottled() {
		t.Errorf("Initial state should not be throttled")
	}

	// 500 byte harca
	state.Record(300, 200)
	if state.IsThrottled() {
		t.Errorf("500 byte limit altinda, throttled olmamali")
	}

	// 600 byte daha harca -> Toplam 1100 byte > 1000 byte limit!
	state.Record(400, 200)
	if !state.IsThrottled() {
		t.Errorf("1100 byte sonrasi IsThrottled true olmali")
	}

	// Limiter limitinin ThrottledRateBytesSec (1 Mbps = 125,000 byte/s) oldugunu dogrula
	limit := state.GetLimiter().Limit()
	if limit != 125000 {
		t.Errorf("Beklenen throttled limit 125000, alinan: %v", limit)
	}
}

func TestTenantRuntimeState_SelfHostUnlimited(t *testing.T) {
	// Open-core / self-host: acik hiz siniri yok (normalMbps=0) ve enterprise =>
	// SINIRSIZ, limiter nil olmali (throttle uygulanmaz).
	for _, tc := range []struct {
		name     string
		plan     string
		normal   int64
	}{
		{"self-host default (mbps=0)", "free", 0},
		{"enterprise", bandwidth.PlanEnterprise, 1000},
	} {
		state := bandwidth.NewTenantRuntimeState("ten_"+tc.name, tc.plan, 1000, 0, tc.normal, 0)
		if state.GetLimiter() != nil {
			t.Errorf("%s: limiter nil olmali (sinirsiz)", tc.name)
		}
		state.Record(900, 900) // kotayi assa bile
		if state.IsThrottled() {
			t.Errorf("%s: asla throttled olmamali", tc.name)
		}
	}
}

func TestLocalUsageRecorder_RecordAndFlush(t *testing.T) {
	ctx := context.Background()
	st, err := pgstore.Open(ctx, "postgres://rpshell:rpshell@localhost:5432/rpshell_test")
	if err != nil {
		t.Skip("Postgres test DB baglanamadi, atlaniyor")
	}
	defer st.Close()

	ten, err := st.CreateTenant(ctx, "rec-test-"+time.Now().Format("150405"))
	if err != nil {
		t.Fatalf("CreateTenant: %v", err)
	}

	rec := bandwidth.NewLocalRecorder(st)
	defer rec.Close()

	err = rec.Record(ctx, bandwidth.UsageEvent{
		TenantID:  ten.ID,
		Type:      bandwidth.UsageTypeProxyHTTP,
		BytesIn:   5000,
		BytesOut:  15000,
		Timestamp: time.Now(),
	})
	if err != nil {
		t.Fatalf("Record: %v", err)
	}

	// Flush yapip DB'den okuyalim
	if err := rec.Flush(ctx); err != nil {
		t.Fatalf("Flush: %v", err)
	}

	currentPeriod := time.Now().UTC().Format("2006-01")
	in, out, err := st.GetBandwidthUsage(ctx, ten.ID, currentPeriod)
	if err != nil {
		t.Fatalf("GetBandwidthUsage: %v", err)
	}
	if in != 5000 || out != 15000 {
		t.Errorf("DB'deki kullanim hatali: in=%d, out=%d", in, out)
	}
}
