package ingress

import (
	"net/http"
	"net/http/httptest"
	"regexp"
	"testing"
)

// On-suzgec (wafHints) davranisi degistirmemeli: her aday icin sonuc, suzgecsiz
// brute-force tarama ile ayni olmali.
func TestWAFHintsEquivalentToBruteForce(t *testing.T) {
	if len(wafHints) != len(owaspLite) {
		t.Fatalf("her yerlesik kuralin ipucu olmali: %d/%d", len(wafHints), len(owaspLite))
	}
	targets := []string{
		"/x?q=1+UNION+SELECT+pass+FROM+users", "/x?id=1'+or+'1'='1", "/x?t=Information_Schema.tables",
		"/x?id=1;SLEEP(5)", "/x?q=%3CScRiPt%3Ealert(1)", "/x?u=JavaScript:alert(1)",
		"/x?f=../../etc/passwd", "/x?f=..%5C..%5Cwin.ini", "/x?c=1;cat+/etc/hosts", "/x?c=$(id)",
		"/x?q=1%7Ccurl+http", "/x?a=1&b=2", "/api/v1/items?page=2", "/x?q=%C5%BFelect",
		"/x?q=union%20%C5%BFelect", "/x?q=%C5%BFcript", "/", "/a?b=on%65rror=1", "/a?document+.+cookie",
	}
	uas := []string{"", "Mozilla/5.0 Chrome/130", "SQLMap/1.7", "nikto", "Mozilla ; curl ", "x ſqlmap"}
	for _, tg := range targets {
		for _, ua := range uas {
			r := httptest.NewRequest("GET", "http://example.com"+tg, nil)
			if ua != "" {
				r.Header.Set("User-Agent", ua)
			}
			got, hit := wafScan(r, owaspLite)
			want, wantHit := bruteScan(r)
			if hit != wantHit || got != want {
				t.Errorf("%s ua=%q: hint=(%q,%v) brute=(%q,%v)", tg, ua, got, hit, want, wantHit)
			}
		}
	}
}

// bruteScan, wafScan'in on-suzgecsiz esdegeri (wafHints gecici bos).
func bruteScan(r *http.Request) (string, bool) {
	saved := wafHints
	wafHints = map[*regexp.Regexp][]string{}
	defer func() { wafHints = saved }()
	return wafScan(r, owaspLite)
}
