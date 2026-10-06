package bandwidth_test

import (
	"testing"

	"github.com/tkodcumpeg4/zorven/server/bandwidth"
)

// Limit 0 = sinirsiz: ne enterprise'da ne baska planda kota asimi throttle uretmez.
func TestTenantRuntimeState_ZeroLimitNeverThrottles(t *testing.T) {
	for _, plan := range []string{"enterprise", "pro"} {
		s := bandwidth.NewTenantRuntimeState("t1", plan, 0, 0, 100, 5)
		s.Record(1<<50, 1<<50) // astronomik trafik
		if s.IsThrottled() {
			t.Fatalf("%s: limit 0 iken throttle olmamali", plan)
		}
	}
	// Enterprise ayrica hic rate limiter uygulamaz.
	if bandwidth.NewTenantRuntimeState("t1", "enterprise", 0, 1<<60, 100, 5).GetLimiter() != nil {
		t.Fatal("enterprise icin limiter nil olmali")
	}
	// Baslangicta kullanim >> 0 iken de limit 0 throttle etmez.
	if bandwidth.NewTenantRuntimeState("t1", "pro", 0, 1<<60, 100, 5).IsThrottled() {
		t.Fatal("limit 0, kullanim yuksek: throttle olmamali")
	}
}
