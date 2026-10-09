package ingress

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

const wafPolicy = `{"rules":[{"match":{"path_prefix":"/"},"action":{"type":"waf","ruleset":"owasp-lite"}}]}`

func wafRouter(t *testing.T, raw string) *Router {
	t.Helper()
	cp, _ := compilePolicy([]byte(raw), 10, nil)
	if cp == nil {
		t.Fatalf("waf kurali derlenmedi: %s", raw)
	}
	return routerWith(cp)
}

func wafHit(rt *Router, target string, headers map[string]string) policyOutcome {
	req := httptest.NewRequest(http.MethodGet, target, nil)
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	return rt.evaluatePolicies("example.com", req, "1.2.3.4", "tun_1", false)
}

// Yerlesik kume tipik yukleri yakalamali.
func TestWAFBlocksKnownPayloads(t *testing.T) {
	rt := wafRouter(t, wafPolicy)
	cases := []struct {
		ad     string
		target string
		hdr    map[string]string
	}{
		{"sqli-union", "http://example.com/x?q=1+UNION+SELECT+pass+FROM+users", nil},
		{"sqli-or1", "http://example.com/x?id=1'+or+'1'='1", nil},
		{"sqli-schema", "http://example.com/x?t=information_schema.tables", nil},
		{"sqli-sleep", "http://example.com/x?id=1;sleep(5)", nil},
		{"xss-script", "http://example.com/x?q=%3Cscript%3Ealert(1)%3C/script%3E", nil},
		{"xss-onerror", "http://example.com/x?q=%3Cimg+src%3Dx+onerror%3Dalert(1)%3E", nil},
		{"xss-javascript", "http://example.com/x?u=javascript:alert(1)", nil},
		{"traversal", "http://example.com/x?f=../../etc/passwd", nil},
		{"traversal-win", "http://example.com/x?f=..%5C..%5Cwin.ini", nil},
		{"cmdi", "http://example.com/x?c=1;cat+/etc/hosts", nil},
		{"scanner-ua", "http://example.com/", map[string]string{"User-Agent": "sqlmap/1.7"}},
		{"referer", "http://example.com/", map[string]string{"Referer": "http://a/?q=<script>"}},
	}
	for _, c := range cases {
		t.Run(c.ad, func(t *testing.T) {
			out := wafHit(rt, c.target, c.hdr)
			if !out.wafBlocked {
				t.Errorf("yakalanmadi: %s", c.target)
			}
		})
	}
}

// Masum istekler engellenmemeli (yanlis pozitif kontrolu).
func TestWAFAllowsBenignRequests(t *testing.T) {
	rt := wafRouter(t, wafPolicy)
	cases := []string{
		"http://example.com/api/v1/orders?page=2&limit=50",
		"http://example.com/search?q=selection+of+union+members",
		"http://example.com/blog/how-to-select-a-database",
		"http://example.com/files/report-2026.pdf",
		"http://example.com/users/42",
	}
	for _, c := range cases {
		t.Run(c, func(t *testing.T) {
			if out := wafHit(rt, c, nil); out.wafBlocked {
				t.Errorf("yanlis pozitif: %s (kural: %s)", c, out.wafRule)
			}
		})
	}
}

// URL-encode edilmis yuk de yakalanmali (decode edilmis hali de taranir).
func TestWAFCatchesEncodedPayload(t *testing.T) {
	rt := wafRouter(t, wafPolicy)
	if out := wafHit(rt, "http://example.com/x?q=%75nion%20select", nil); !out.wafBlocked {
		// %75 = u; decode sonrasi "union select" olur.
		t.Error("URL-encode edilmis SQLi yakalanmadi")
	}
}

// Kuruma ozel kalip eklenebilmeli.
func TestWAFCustomPattern(t *testing.T) {
	raw := `{"rules":[{"match":{"path_prefix":"/"},"action":{"type":"waf",
	  "ruleset":"owasp-lite","patterns":["(?i)gizli-uc-nokta"]}}]}`
	rt := wafRouter(t, raw)
	if out := wafHit(rt, "http://example.com/x?a=gizli-uc-nokta", nil); !out.wafBlocked {
		t.Error("ozel kalip yakalamadi")
	}
}

// Gecersiz regex fail-closed (503) olmali — kullanici korundugunu sanmamali.
func TestWAFInvalidPatternFailsClosed(t *testing.T) {
	raw := `{"rules":[{"match":{"path_prefix":"/"},"action":{"type":"waf","patterns":["[bozuk"]}}]}`
	cp, _ := compilePolicy([]byte(raw), 10, nil)
	assertFailClosed(t, cp, "gecersiz regex")
}

// Bilinmeyen kume de fail-closed olmali.
func TestWAFUnknownRulesetFailsClosed(t *testing.T) {
	raw := `{"rules":[{"match":{"path_prefix":"/"},"action":{"type":"waf","ruleset":"yok-boyle"}}]}`
	cp, _ := compilePolicy([]byte(raw), 10, nil)
	assertFailClosed(t, cp, "bilinmeyen kume")
}

// Alanlar tamamen taranir (dolgu ile atlatma yok, F-08); yalniz ust sinir asilirsa red.
func TestWAFOversizeFieldRejected(t *testing.T) {
	rt := wafRouter(t, wafPolicy)
	pad := strings.Repeat("a", 9000)
	out := wafHit(rt, "http://example.com/x?pad="+pad+"&q=%3Cscript%3E", nil)
	if !out.wafBlocked || out.wafRule == wafOversizeRule {
		t.Errorf("9000 karakter dolgu + payload tarama ile engellenmedi: %+v", out)
	}
	// 9000 karakterlik zararsiz Cookie gecmeli.
	out = wafHit(rt, "http://example.com/x", map[string]string{"Cookie": "s=" + pad})
	if out.wafBlocked {
		t.Error("9000 karakterlik zararsiz Cookie engellendi")
	}
	// 70 KB alan oversize ile reddedilir.
	out = wafHit(rt, "http://example.com/x?q="+strings.Repeat("a", 70<<10), nil)
	if !out.wafBlocked || out.wafRule != wafOversizeRule {
		t.Errorf("sinir asan alan engellenmedi: %+v", out)
	}
	// Sinir icindeki zararsiz istek gecmeli.
	out = wafHit(rt, "http://example.com/x?q="+strings.Repeat("a", 1000), nil)
	if out.wafBlocked {
		t.Error("sinir icindeki zararsiz istek engellendi")
	}
}

// Govde ASLA taranmamali — tamponlama yok.
func TestWAFDoesNotReadBody(t *testing.T) {
	cp, _ := compilePolicy([]byte(wafPolicy), 10, nil)
	rt := routerWith(cp)
	body := "union select * from users"
	req := httptest.NewRequest(http.MethodPost, "http://example.com/x", strings.NewReader(body))
	orig := req.Body

	out := rt.evaluatePolicies("example.com", req, "1.2.3.4", "tun_1", false)
	if out.wafBlocked {
		t.Error("govde tarandi — tasarim govdeyi taramamali")
	}
	if req.Body != orig {
		t.Error("govde tamponlandi — WAF govdeye dokunmamali")
	}
}
