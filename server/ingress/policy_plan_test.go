package ingress

import (
	"context"
	"net/http/httptest"
	"testing"

	"github.com/tkodcumpeg4/zorven/server/entitlements"
	"github.com/tkodcumpeg4/zorven/server/store"
)

// KARAR (Ek 1 madde 3): policy motorunun plan kapisi YALNIZ olusturma/duzenlemede (API) uygulanir;
// plan dusunce MEVCUT policy'ler uygulanmaya DEVAM eder (kapatmak tuneli sessizce acardi).
// Ingress tarafi plan sormaz; SecurityGate yalniz WAF/Geo'yu etkiler. Bu test, uretimdeki gibi
// SecurityGate = ProOrAbove iken Free plandaki kiracinin policy'sinin hala uygulandigini kilitler.
func TestPolicyStillEnforcedAfterPlanDowngrade(t *testing.T) {
	for _, plan := range []string{"free", "hobby", "pro", "enterprise"} {
		st := &policyStore{}
		st.routes = []store.HostRoute{{FQDN: "h.com", TenantID: "t1", TunnelID: "tun", Enabled: true, Plan: plan}}
		st.proutes = []store.PolicyRoute{{Host: "h.com", TunnelID: "tun", Policy: denyPolicy("pol_x", "/admin")}}
		r := NewRouter(st)
		r.SecurityGate = entitlements.ProOrAbove // main.go ile ayni
		if err := r.Reload(context.Background()); err != nil {
			t.Fatal(err)
		}

		req := httptest.NewRequest("GET", "http://h.com/admin/panel", nil)
		if got := r.evaluatePolicies("h.com", req, "1.1.1.1", "tun", false).denyStatus; got != 403 {
			t.Errorf("plan=%s: mevcut policy uygulanmaliydi (403), alinan %d", plan, got)
		}
		// Eslesmeyen yol etkilenmez.
		other := httptest.NewRequest("GET", "http://h.com/public", nil)
		if out := r.evaluatePolicies("h.com", other, "1.1.1.1", "tun", false); out.terminal() {
			t.Errorf("plan=%s: eslesmeyen yol reddedilmemeli: %+v", plan, out)
		}
	}
}
