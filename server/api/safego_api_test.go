package api

import (
	"testing"
	"time"

	"github.com/tkodcumpeg4/zorven/server/obs"
)

func waitPanics(t *testing.T, where string, want uint64) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for obs.Panics(where) < want {
		if time.Now().After(deadline) {
			t.Fatalf("%s panik sayaci %d, beklenen >= %d", where, obs.Panics(where), want)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// Istek basina acilan kisa omurlu goroutine'lerdeki (obs.Go) panik sureci
// dusurmemeli; kurtarilip zorven_panics_total{where=...} ile sayilmali.
// (Acik surumde denetim kaydi no-op oldugundan denetim goroutine testi yok.)
func TestAuthzChangeHookPanicRecovered(t *testing.T) {
	base := obs.Panics("api_authz_change")
	srv := &Server{OnAuthzChange: func(string) { panic("kanca bozuk") }}
	srv.notifyAuthzChange("ten_1")
	waitPanics(t, "api_authz_change", base+1)
}
