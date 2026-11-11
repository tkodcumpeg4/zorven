package ingress

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/tkodcumpeg4/zorven/server/store"
)

// A4 #13: supurucu henuz Expired'i isaretlememis olsa da bitis ani gecen gecici
// tunel aninda 410 almali (eskiden ~30-60 sn daha trafik aliyordu). Suresi
// dolmamis gecici tunel bu kontrolden etkilenmez.
func TestServeHTTPEphemeralPastExpiresAtIsGone(t *testing.T) {
	past := time.Now().Add(-time.Second)
	future := time.Now().Add(time.Hour)
	st := &fakeStore{routes: []store.HostRoute{
		{FQDN: "gecti.zorven.app", TenantID: "ten_1", TunnelID: "tun_1", ClientID: "cli_1", Enabled: true, ExpiresAt: &past},
		{FQDN: "suruyor.zorven.app", TenantID: "ten_1", TunnelID: "tun_2", ClientID: "cli_1", Enabled: false, ExpiresAt: &future},
	}}
	r := NewRouter(st)
	if err := r.Reload(context.Background()); err != nil {
		t.Fatal(err)
	}
	h := &Handler{Router: r}
	do := func(host string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodGet, "https://"+host+"/api", nil)
		req.Host = host
		req.Header.Set("Accept", "application/json")
		w := httptest.NewRecorder()
		h.ServeHTTP(w, req)
		return w
	}
	if w := do("gecti.zorven.app"); w.Code != http.StatusGone || !strings.Contains(w.Body.String(), "tunnel_expired") {
		t.Fatalf("410 tunnel_expired bekleniyordu: %d %s", w.Code, w.Body.String())
	}
	// Suresi dolmamis: TTL kontrolunden gecer (pasif oldugu icin 503; Hub'a ulasmaz).
	if w := do("suruyor.zorven.app"); w.Code != http.StatusServiceUnavailable {
		t.Fatalf("suresi dolmamis tunel: %d %s", w.Code, w.Body.String())
	}
}
