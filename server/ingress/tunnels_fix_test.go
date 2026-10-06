package ingress

import (
	"context"
	"crypto/tls"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/tkodcumpeg4/zorven/server/store"
)

// policyStore, policy ve yol kurali verisi saglayan test deposu.
type policyStore struct {
	fakeStore
	paths   []store.HostRoute
	proutes []store.PolicyRoute
	fail    bool
}

func (f *policyStore) ListPathRouteEntries(context.Context) ([]store.HostRoute, error) {
	return f.paths, nil
}

func (f *policyStore) ListPolicyRoutes(context.Context) ([]store.PolicyRoute, error) {
	if f.fail {
		return nil, context.DeadlineExceeded
	}
	return f.proutes, nil
}

func denyPolicy(id, prefix string) store.Policy {
	return store.Policy{
		ID: id, Enabled: true, Priority: 100,
		Config: []byte(`{"rules":[{"match":{"path_prefix":"` + prefix + `"},"action":{"type":"deny","status":403}}]}`),
	}
}

func TestPolicyPathNormalization(t *testing.T) {
	cases := []struct {
		prefix, path string
		want         bool
	}{
		{"/deny", "/deny", true},
		{"/deny", "//deny", true},
		{"/deny", "/./deny", true},
		{"/deny", "/x/../deny/a", true},
		{"/deny", "/\\deny", true},
		{"deny", "/deny", true}, // basta '/' yok -> normalize
		{"//deny", "/deny", true},
		{"/deny", "/other", false},
		{"", "/anything", true},
	}
	for _, c := range cases {
		cp, _ := compilePolicy([]byte(`{"rules":[{"match":{"path_prefix":"`+c.prefix+`"},"action":{"type":"deny"}}]}`), 100, nil)
		rt := NewRouter(nil)
		rt.policies["h.com"] = []*compiledPolicy{cp}
		req := httptest.NewRequest("GET", "http://h.com/", nil)
		req.URL.Path = c.path
		out := rt.evaluatePolicies("h.com", req, "1.1.1.1", "t", false)
		if got := out.denyStatus == 403; got != c.want {
			t.Errorf("prefix=%q path=%q: deny=%v istenen %v", c.prefix, c.path, got, c.want)
		}
	}
}

func TestLookupPathDoubleSlash(t *testing.T) {
	st := &policyStore{}
	st.routes = []store.HostRoute{{FQDN: "ex.com", TenantID: "t1", TunnelID: "def", Enabled: true}}
	st.paths = []store.HostRoute{{FQDN: "ex.com", TenantID: "t1", TunnelID: "api", Enabled: true, PathPrefix: "/api"}}
	r := NewRouter(st)
	if err := r.Reload(context.Background()); err != nil {
		t.Fatal(err)
	}
	for _, p := range []string{"/api/x", "//api/x", "/./api/x", "/z/../api"} {
		rt, ok := r.LookupPath("ex.com", p)
		if !ok || rt.TunnelID != "api" {
			t.Errorf("LookupPath(%q)=%q ok=%v; istenen api", p, rt.TunnelID, ok)
		}
	}
	if rt, _ := r.LookupPath("ex.com", "/apix"); rt.TunnelID != "def" {
		t.Errorf("/apix varsayilana dusmeli, %q", rt.TunnelID)
	}
}

func TestRateLimitSurvivesReload(t *testing.T) {
	pol := store.Policy{
		ID: "pol_rl", Enabled: true, Priority: 100,
		Config: []byte(`{"rules":[{"match":{},"action":{"type":"rate_limit","key":"ip","requests":2,"window_sec":3600}}]}`),
	}
	st := &policyStore{}
	st.routes = []store.HostRoute{{FQDN: "h.com", TenantID: "t1", TunnelID: "tun", Enabled: true}}
	st.proutes = []store.PolicyRoute{{Host: "h.com", Policy: pol}}
	r := NewRouter(st)
	ctx := context.Background()
	if err := r.Reload(ctx); err != nil {
		t.Fatal(err)
	}
	hit := func() bool {
		req := httptest.NewRequest("GET", "http://h.com/", nil)
		return r.evaluatePolicies("h.com", req, "9.9.9.9", "tun", false).rateLimited
	}
	if hit() || hit() {
		t.Fatal("ilk iki istek gecmeli")
	}
	if !hit() {
		t.Fatal("ucuncu istek sinirlanmali")
	}
	// Reload sayaclari SIFIRLAMAMALI.
	if err := r.Reload(ctx); err != nil {
		t.Fatal(err)
	}
	if !hit() {
		t.Fatal("Reload sonrasi sayac sifirlandi (rate_limit atlatilabilir)")
	}
	// Kural degisirse yeni limiter (sifir sayac) olusmali.
	pol.Config = []byte(`{"rules":[{"match":{},"action":{"type":"rate_limit","key":"ip","requests":5,"window_sec":3600}}]}`)
	st.proutes = []store.PolicyRoute{{Host: "h.com", Policy: pol}}
	if err := r.Reload(ctx); err != nil {
		t.Fatal(err)
	}
	if hit() {
		t.Fatal("kural degisince yeni limiter beklenirdi")
	}
}

func TestPathRouteGetsTargetTunnelPolicies(t *testing.T) {
	st := &policyStore{}
	st.routes = []store.HostRoute{{FQDN: "ex.com", TenantID: "t1", TunnelID: "def", Enabled: true}}
	st.paths = []store.HostRoute{{FQDN: "ex.com", TenantID: "t1", TunnelID: "api", Enabled: true, PathPrefix: "/api"}}
	// Politika yalnizca "api" tunelinin tunel baglanti ile baglanmis.
	st.proutes = []store.PolicyRoute{{Host: "api-only.ex.com", TunnelID: "api", Policy: denyPolicy("pol_a", "/")}}
	r := NewRouter(st)
	if err := r.Reload(context.Background()); err != nil {
		t.Fatal(err)
	}
	route, _ := r.LookupPath("ex.com", "/api/x")
	req := httptest.NewRequest("GET", "http://ex.com/api/x", nil)
	out := r.evaluatePolicyList(r.policiesForRoute("ex.com", route), req, "1.1.1.1", route.TunnelID, false)
	if out.denyStatus != 403 {
		t.Fatalf("yol kurali hedef tunelin policy'sini almali, out=%+v", out)
	}
	// Varsayilan route etkilenmemeli.
	def, _ := r.LookupPath("ex.com", "/other")
	req2 := httptest.NewRequest("GET", "http://ex.com/other", nil)
	if o := r.evaluatePolicyList(r.policiesForRoute("ex.com", def), req2, "1.1.1.1", def.TunnelID, false); o.terminal() {
		t.Fatalf("varsayilan route policy almamali, out=%+v", o)
	}
}

func TestPolicyLoadFailureKeepsOldSnapshot(t *testing.T) {
	// ListPolicyRoutes hata verirse eski policy'ler korunmali (fail-stale).
	st := &policyStore{}
	st.routes = []store.HostRoute{{FQDN: "h.com", TenantID: "t1", TunnelID: "tun", Enabled: true}}
	st.proutes = []store.PolicyRoute{{Host: "h.com", Policy: denyPolicy("pol_x", "/")}}
	r := NewRouter(st)
	if err := r.Reload(context.Background()); err != nil {
		t.Fatal(err)
	}
	st.fail = true
	if err := r.Reload(context.Background()); err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest("GET", "http://h.com/", nil)
	if r.evaluatePolicies("h.com", req, "1.1.1.1", "tun", false).denyStatus != 403 {
		t.Fatal("policy hatasinda eski kurallar korunmali")
	}
}

func TestSNIHostMismatch421(t *testing.T) {
	r := newTestRouter(t, "a.com", "b.com")
	h := &Handler{Router: r}
	req := httptest.NewRequest(http.MethodGet, "https://b.com/", nil)
	req.TLS = &tls.ConnectionState{ServerName: "a.com"}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusMisdirectedRequest {
		t.Fatalf("SNI!=Host icin 421 bekleniyordu, alinan %d", rec.Code)
	}
}

func TestSanitizeSetCookie(t *testing.T) {
	cases := []struct{ in, want string }{
		{"a=b; Domain=zorven.app; Path=/", "a=b; Path=/"},
		{"a=b; domain=.zorven.app", "a=b"},
		{"a=b; Domain=app", "a=b"}, // platformun ust alani
		{"a=b; Domain=cust.com; Path=/", "a=b; Domain=cust.com; Path=/"},
		{"a=b; Path=/; HttpOnly", "a=b; Path=/; HttpOnly"},
		{"a=b; Domain=x.zorven.app", "a=b; Domain=x.zorven.app"},
	}
	for _, c := range cases {
		if got := sanitizeSetCookie(c.in, "zorven.app"); got != c.want {
			t.Errorf("sanitizeSetCookie(%q)=%q istenen %q", c.in, got, c.want)
		}
	}
}

func TestValidatePolicyConfig(t *testing.T) {
	ok := []string{
		``, `{}`, `{"rules":[]}`,
		`{"rules":[{"match":{"path_prefix":"/a"},"action":{"type":"deny","status":403}}]}`,
		`{"rules":[{"match":{},"action":{"type":"rate_limit","key":"header:X-Api","requests":5}}]}`,
		`{"rules":[{"match":{},"action":{"type":"waf","ruleset":"owasp-lite"}}]}`,
		`{"rules":[{"match":{},"action":{"type":"redirect","location":"/x","status":308}}]}`,
	}
	for _, c := range ok {
		if err := ValidatePolicyConfig([]byte(c)); err != nil {
			t.Errorf("gecerli config reddedildi %q: %v", c, err)
		}
	}
	bad := []string{
		`[]`, `"x"`, `{"rules":[{"action":{"type":"nope"}}]}`,
		`{"rules":[{"action":{"type":"deny","status":200}}]}`,
		`{"rules":[{"action":{"type":"redirect"}}]}`,
		`{"rules":[{"action":{"type":"rate_limit","requests":0}}]}`,
		`{"rules":[{"action":{"type":"rate_limit","requests":1,"key":"cookie"}}]}`,
		`{"rules":[{"action":{"type":"waf","ruleset":"zzz"}}]}`,
		`{"rules":[{"action":{"type":"waf","patterns":["("]}}]}`,
		`{"rules":[{"action":{"type":"verify_webhook","provider":"x","secret_ref":"s"}}]}`,
		`{"rules":[{"action":{"type":"set_header"}}]}`,
	}
	for _, c := range bad {
		if err := ValidatePolicyConfig([]byte(c)); err == nil {
			t.Errorf("gecersiz config kabul edildi: %s", c)
		}
	}
}
