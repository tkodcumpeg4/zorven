package ingress

import (
	"net/http"
	"strings"
	"testing"
	"time"
)

// O9: bas zaman asiminda (ziyaretci 504 aldi) ajana da iptal gitmeli; eskiden
// ajan istegi kendi dial/baslik zaman asimina kadar (134 sn) tutuyordu.
func TestHeadTimeoutCancelsAgent(t *testing.T) {
	release := make(chan struct{})
	defer close(release)
	env := newMCPEnv(t, `{"head_timeout_sec":1}`, `[]`, func(ar agentReq) agentResp {
		<-release
		return okEcho(ar)
	})
	resp, _ := env.do(t, "POST", "/mcp", callBody, bearer("jwt.ok.a"))
	if resp.StatusCode != http.StatusGatewayTimeout {
		t.Fatalf("504 bekleniyordu: %d", resp.StatusCode)
	}
	select {
	case <-env.agent.cancels:
	case <-time.After(2 * time.Second):
		t.Fatal("bas zaman asiminda ajana iptal gitmedi")
	}
}

func TestWarnSamplerOneLinePerSecondWithSuppressedCount(t *testing.T) {
	now := time.Unix(1000, 0)
	s := &warnSampler{now: func() time.Time { return now }}
	if ok, n := s.allow("ip_forbidden", "tun_1"); !ok || n != 0 {
		t.Fatalf("ilk olay yazilmali: %v %d", ok, n)
	}
	for i := 0; i < 500; i++ {
		if ok, _ := s.allow("ip_forbidden", "tun_1"); ok {
			t.Fatal("ayni saniyede ikinci satir yazilmamali")
		}
	}
	// Farkli tunel / farkli neden kendi kotasina sahip.
	if ok, _ := s.allow("ip_forbidden", "tun_2"); !ok {
		t.Fatal("baska tunel bastirilmamali")
	}
	if ok, _ := s.allow("rate_limited", "tun_1"); !ok {
		t.Fatal("baska neden bastirilmamali")
	}
	now = now.Add(time.Second)
	ok, n := s.allow("ip_forbidden", "tun_1")
	if !ok || n != 500 {
		t.Fatalf("bir saniye sonra satir + bastirilan=500 beklenirdi: %v %d", ok, n)
	}
	// Uzun sessizlikten sonra anahtarlar temizlenir (bellek siniri).
	now = now.Add(warnSamplePrune + time.Second)
	s.allow("x", "y")
	s.mu.Lock()
	left := len(s.m)
	s.mu.Unlock()
	if left != 1 {
		t.Fatalf("eski anahtarlar temizlenmedi: %d", left)
	}
}

// A4 #12: gecersiz baslik adi/degeri ve CRLF Location atilir; bos ad tunelin
// tum isteklerini 502 yapiyordu.
func TestTrafficPolicyDropsInvalidHeaders(t *testing.T) {
	tp := parseTrafficPolicy([]byte(`{
		"request_headers":{"set":{"":"x","Bad Name":"x","X-Ok":"1","X-Crlf":"a\r\nInjected: 1"},"remove":["","X-Del"]},
		"response_headers":{"set":{"X-Resp":"ok","\n":"y"}},
		"redirects":[{"match_prefix":"/a","location":"https://e.com/\r\nSet-Cookie: x=1","status":302},{"match_prefix":"/b","location":"/ok","status":999}]}`))
	if tp == nil {
		t.Fatal("gecerli kurallar kalmaliydi")
	}
	h := http.Header{"X-Del": {"1"}}
	tp.applyToRequest(h)
	if h.Get("X-Ok") != "1" || h.Get("X-Del") != "" {
		t.Fatalf("gecerli kurallar uygulanmadi: %v", h)
	}
	for k, v := range h {
		if k == "" || strings.ContainsAny(k, " \r\n") || strings.ContainsAny(strings.Join(v, ""), "\r\n") {
			t.Fatalf("gecersiz baslik gecti: %q=%q", k, v)
		}
	}
	rh := http.Header{}
	tp.applyToResponse(rh)
	if rh.Get("X-Resp") != "ok" || len(rh) != 1 {
		t.Fatalf("yanit basliklari: %v", rh)
	}
	if _, _, ok := tp.matchRedirect("/a/x"); ok {
		t.Fatal("CRLF iceren Location atilmaliydi")
	}
	if loc, st, ok := tp.matchRedirect("/b"); !ok || loc != "/ok" || st != http.StatusFound {
		t.Fatalf("gecersiz durum 302'ye inmeli: %q %d %v", loc, st, ok)
	}
}

// Policy motorundan (mergePolicyHeaders) gelen gecersiz ad da uygulamada atlanir.
func TestApplySkipsInvalidMergedHeader(t *testing.T) {
	tp := &trafficPolicy{RequestHeaders: headerRules{Set: map[string]string{"": "x", "X-A": "1"}}}
	h := http.Header{}
	tp.applyToRequest(h)
	if _, bad := h[""]; bad || h.Get("X-A") != "1" {
		t.Fatalf("basliklar: %v", h)
	}
}
