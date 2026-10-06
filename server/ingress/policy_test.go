package ingress

import (
	"net/http/httptest"
	"testing"
)

func compileForTest(t *testing.T, raw string, priority int) *compiledPolicy {
	t.Helper()
	cp, _ := compilePolicy([]byte(raw), priority, nil)
	if cp == nil {
		t.Fatalf("compilePolicy nil dondu: %s", raw)
	}
	return cp
}

func routerWith(policies ...*compiledPolicy) *Router {
	r := NewRouter(nil)
	r.policies["example.com"] = policies
	return r
}

func TestPolicyDenyMatch(t *testing.T) {
	cp := compileForTest(t, `{"rules":[
		{"match":{"path_prefix":"/admin"},"action":{"type":"deny","status":403,"message":"nope"}}
	]}`, 100)
	rt := routerWith(cp)

	req := httptest.NewRequest("GET", "http://example.com/admin/x", nil)
	out := rt.evaluatePolicies("example.com", req, "1.2.3.4", "tun_1", false)
	if !out.terminal() || out.denyStatus != 403 {
		t.Fatalf("deny bekleniyordu, out=%+v", out)
	}

	req2 := httptest.NewRequest("GET", "http://example.com/public", nil)
	out2 := rt.evaluatePolicies("example.com", req2, "1.2.3.4", "tun_1", false)
	if out2.terminal() {
		t.Fatalf("/public eslesmemeliydi, out=%+v", out2)
	}
}

func TestPolicyMethodAndHeaderMatch(t *testing.T) {
	cp := compileForTest(t, `{"rules":[
		{"match":{"methods":["POST"],"header":{"X-Env":"prod"}},"action":{"type":"deny"}}
	]}`, 100)
	rt := routerWith(cp)

	req := httptest.NewRequest("POST", "http://example.com/", nil)
	req.Header.Set("X-Env", "prod")
	if out := rt.evaluatePolicies("example.com", req, "1.1.1.1", "t", false); out.denyStatus != 403 {
		t.Fatalf("POST+header deny bekleniyordu: %+v", out)
	}
	// yanlis metot
	req2 := httptest.NewRequest("GET", "http://example.com/", nil)
	req2.Header.Set("X-Env", "prod")
	if out := rt.evaluatePolicies("example.com", req2, "1.1.1.1", "t", false); out.terminal() {
		t.Fatalf("GET eslesmemeliydi: %+v", out)
	}
	// header yok
	req3 := httptest.NewRequest("POST", "http://example.com/", nil)
	if out := rt.evaluatePolicies("example.com", req3, "1.1.1.1", "t", false); out.terminal() {
		t.Fatalf("header yoksa eslesmemeliydi: %+v", out)
	}
}

func TestPolicyPriorityOrder(t *testing.T) {
	// Dusuk priority (once) allow-benzeri set_header; yuksek priority deny.
	// Priority artan islenir: once 10 (set_header, terminal degil), sonra 20 (deny).
	low := compileForTest(t, `{"rules":[{"match":{},"action":{"type":"set_header","response":{"set":{"X-A":"1"}}}}]}`, 10)
	high := compileForTest(t, `{"rules":[{"match":{"path_prefix":"/x"},"action":{"type":"deny"}}]}`, 20)
	// Router snapshot'i priority sirali verir; burada dogrudan sirali koyuyoruz.
	rt := routerWith(low, high)

	req := httptest.NewRequest("GET", "http://example.com/x", nil)
	out := rt.evaluatePolicies("example.com", req, "1.1.1.1", "t", false)
	if out.denyStatus != 403 {
		t.Fatalf("deny bekleniyordu: %+v", out)
	}
}

func TestPolicySetHeaderAccumulates(t *testing.T) {
	cp := compileForTest(t, `{"rules":[
		{"match":{},"action":{"type":"set_header","request":{"set":{"X-Req":"a"}},"response":{"set":{"X-Res":"b"},"remove":["Server"]}}}
	]}`, 100)
	rt := routerWith(cp)
	req := httptest.NewRequest("GET", "http://example.com/", nil)
	out := rt.evaluatePolicies("example.com", req, "1.1.1.1", "t", false)
	if out.terminal() {
		t.Fatalf("set_header terminal olmamali: %+v", out)
	}
	if out.reqHeaders == nil || out.reqHeaders.Set["X-Req"] != "a" {
		t.Fatalf("istek basligi birikmeliydi: %+v", out.reqHeaders)
	}
	if out.respHeaders == nil || out.respHeaders.Set["X-Res"] != "b" || len(out.respHeaders.Remove) != 1 {
		t.Fatalf("yanit basligi birikmeliydi: %+v", out.respHeaders)
	}
}

func TestPolicyRequireMTLS(t *testing.T) {
	cp := compileForTest(t, `{"rules":[{"match":{"path_prefix":"/secure"},"action":{"type":"require_mtls"}}]}`, 100)
	rt := routerWith(cp)
	req := httptest.NewRequest("GET", "http://example.com/secure", nil)
	if out := rt.evaluatePolicies("example.com", req, "1.1.1.1", "t", false); !out.mtlsFailed {
		t.Fatalf("mTLS yoksa red bekleniyordu: %+v", out)
	}
	if out := rt.evaluatePolicies("example.com", req, "1.1.1.1", "t", true); out.terminal() {
		t.Fatalf("mTLS varsa gecmeliydi: %+v", out)
	}
}

func TestPolicyRateLimit(t *testing.T) {
	// 2 istek/sn, burst 2: ilk 2 gecer, 3. reddedilir.
	cp := compileForTest(t, `{"rules":[{"match":{},"action":{"type":"rate_limit","key":"ip","requests":2,"window_sec":1,"burst":2}}]}`, 100)
	rt := routerWith(cp)
	req := httptest.NewRequest("GET", "http://example.com/", nil)

	for i := 0; i < 2; i++ {
		if out := rt.evaluatePolicies("example.com", req, "9.9.9.9", "t", false); out.rateLimited {
			t.Fatalf("%d. istek gecmeliydi", i+1)
		}
	}
	if out := rt.evaluatePolicies("example.com", req, "9.9.9.9", "t", false); !out.rateLimited {
		t.Fatalf("3. istek rate-limited olmaliydi: %+v", out)
	}
	// farkli IP etkilenmemeli
	if out := rt.evaluatePolicies("example.com", req, "8.8.8.8", "t", false); out.rateLimited {
		t.Fatalf("farkli IP limitlenmemeliydi")
	}
}
