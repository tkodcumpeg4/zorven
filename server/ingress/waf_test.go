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

// Gecersiz regex kurali DUSURMELI — kullanici korundugunu sanmamali.
func TestWAFInvalidPatternDisablesRule(t *testing.T) {
	raw := `{"rules":[{"match":{"path_prefix":"/"},"action":{"type":"waf","patterns":["[bozuk"]}}]}`
	if cp, _ := compilePolicy([]byte(raw), 10, nil); cp != nil {
		t.Fatal("gecersiz regex ile kural etkin kaldi")
	}
}

// Bilinmeyen kume de kurali dusurmeli.
func TestWAFUnknownRulesetDisablesRule(t *testing.T) {
	raw := `{"rules":[{"match":{"path_prefix":"/"},"action":{"type":"waf","ruleset":"yok-boyle"}}]}`
	if cp, _ := compilePolicy([]byte(raw), 10, nil); cp != nil {
		t.Fatal("bilinmeyen kume ile kural etkin kaldi")
	}
}

// Cok uzun sorgu dizesi tarama sinirinda kesilmeli (CPU korumasi).
func TestWAFScanLimit(t *testing.T) {
	rt := wafRouter(t, wafPolicy)
	// Sinirin OTESINE yerlestirilen yuk taranmaz — bu bilincli bir takas.
	long := strings.Repeat("a", wafScanLimit+100) + "union%20select"
	out := wafHit(rt, "http://example.com/x?q="+long, nil)
	if out.wafBlocked {
		t.Error("tarama siniri uygulanmadi (sinir otesi taranmis)")
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
