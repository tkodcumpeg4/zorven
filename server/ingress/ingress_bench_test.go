package ingress

import (
	"context"
	"html"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/tkodcumpeg4/zorven/server/botguard"
	"github.com/tkodcumpeg4/zorven/server/reqlog"
	"github.com/tkodcumpeg4/zorven/server/store"
	"github.com/tkodcumpeg4/zorven/server/tunnel"
)

// benchHandler, canli (sahte ajanli) bir tunel uzerinden Handler.ServeHTTP
// yolunu olcen Handler kurar. Ajan 200 "ok" doner.
func benchHandler(b *testing.B, secCfg string, policies ...*compiledPolicy) *Handler {
	b.Helper()
	tun := rejectRoute()
	if secCfg != "" {
		tun.SecurityConfig = []byte(secCfg)
	}
	tun.Plan = "pro"
	rt := NewRouter(&fakeStore{routes: []store.HostRoute{tun}})
	if err := rt.Reload(context.Background()); err != nil {
		b.Fatal(err)
	}
	if len(policies) > 0 {
		rt.policies[testHost] = policies
	}
	hub := tunnel.NewHub()
	newFakeAgent(b, hub, tun.ClientID, func(agentReq) agentResp {
		return agentResp{status: 200, headers: map[string][]string{"Content-Type": {"text/plain"}}, chunks: [][]byte{[]byte("ok")}}
	})
	return &Handler{Router: rt, Hub: hub, ReqLog: reqlog.New(500),
		BotKey:      []byte("test-bot-key"),
		BotResolver: &botResolver{},
		Log:         slog.New(slog.NewTextHandler(io.Discard, nil))}
}

func runBench(b *testing.B, h *Handler, mk func() *http.Request) {
	b.Helper()
	w := httptest.NewRecorder()
	h.ServeHTTP(w, mk())
	if w.Code != 200 {
		b.Fatalf("200 bekleniyordu: %d %s", w.Code, w.Body.String())
	}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		h.ServeHTTP(httptest.NewRecorder(), mk())
	}
}

func benchReq() *http.Request {
	return browserGet("/api/v1/items?page=2&limit=20", "198.51.100.4")
}

func benchPolicies(b *testing.B) []*compiledPolicy {
	b.Helper()
	cp, _ := compilePolicy([]byte(`{"rules":[
{"match":{"path_prefix":"/admin"},"action":{"type":"deny"}},
{"match":{"methods":["DELETE"],"path_prefix":"/api"},"action":{"type":"deny"}},
{"match":{"header":{"X-Blocked":"1"}},"action":{"type":"deny"}},
{"match":{"path_prefix":"/api"},"action":{"type":"set_header","response":{"set":{"X-A":"1"}}}}]}`), 10, nil)
	if cp == nil {
		b.Fatal("politika derlenmedi")
	}
	return []*compiledPolicy{cp}
}

func BenchmarkIngress1Plain(b *testing.B) {
	runBench(b, benchHandler(b, ""), benchReq)
}

func BenchmarkIngress2Policy(b *testing.B) {
	runBench(b, benchHandler(b, "", benchPolicies(b)...), benchReq)
}

func BenchmarkIngress3WAFLog(b *testing.B) {
	runBench(b, benchHandler(b, `{"waf":{"mode":"log"}}`), benchReq)
}

func BenchmarkIngress4WAFBlock(b *testing.B) {
	runBench(b, benchHandler(b, `{"waf":{"mode":"block"}}`), benchReq)
}

func BenchmarkIngress5BotCookie(b *testing.B) {
	// Cerez "always" kipinde (ayni anahtar, ayni tunel) cozulur; olculen yol
	// "suspicious" kipte gecerli cerezli bir istektir.
	solver := benchHandler(b, always12)
	ip := "198.51.100.4"
	body := serve(solver, browserGet("/p", ip)).Body.String()
	tm, nm := reTok.FindStringSubmatch(body), reNonce.FindStringSubmatch(body)
	if tm == nil || nm == nil {
		b.Fatal("sorgu yok")
	}
	ctr := botguard.SolvePoW(html.UnescapeString(nm[1]), 12)
	vw := serve(solver, verifyReq(ip, html.UnescapeString(tm[1]), ctr, "/"))
	var ck *http.Cookie
	for _, c := range vw.Result().Cookies() {
		if c.Name == botguard.CookieName {
			ck = c
		}
	}
	if ck == nil {
		b.Fatal("cerez yok")
	}
	h := benchHandler(b, `{"bot":{"mode":"suspicious","difficulty":12,"ttl_minutes":30}}`)
	runBench(b, h, func() *http.Request {
		r := benchReq()
		r.AddCookie(ck)
		return r
	})
}

func BenchmarkIngress6RateLimit(b *testing.B) {
	runBench(b, benchHandler(b, `{"rate":{"enabled":true,"requests":100000000,"window_seconds":60}}`), benchReq)
}

// Ajan gidis-donusunden bagimsiz, yalniz karar katmanlarinin maliyeti.
func BenchmarkIngressPolicyEvalOnly(b *testing.B) {
	rt := routerWith(benchPolicies(b)...)
	r := httptest.NewRequest("GET", "http://example.com/api/v1/items?page=2&limit=20", nil)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		rt.evaluatePolicies("example.com", r, "1.2.3.4", "tun_1", false)
	}
}

func BenchmarkIngressWAFPolicyEvalOnly(b *testing.B) {
	cp, _ := compilePolicy([]byte(wafPolicy), 10, nil)
	rt := routerWith(cp)
	r := browserGet("/api/v1/items?page=2&limit=20", "198.51.100.4")
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		rt.evaluatePolicies("example.com", r, "1.2.3.4", "tun_1", false)
	}
}
