package mail

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestMTASTSPolicyServed(t *testing.T) {
	h := NewAutoConfigHandler(ClientConfig{MailDomain: "mail.zorven.app", PlatformDomain: "zorven.app", Host: "mail.zorven.app"})
	r := httptest.NewRequest(http.MethodGet, "https://mta-sts.zorven.app/.well-known/mta-sts.txt", nil)
	r.Host = "mta-sts.zorven.app"
	if !h.Match(r) {
		t.Fatal("mta-sts istegi eslesmeli")
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	body := w.Body.String()
	for _, want := range []string{"version: STSv1", "mode: testing", "mx: mail.zorven.app", "max_age: 86400"} {
		if !strings.Contains(body, want) {
			t.Fatalf("politika %q icermeli: %q", want, body)
		}
	}
	if ct := w.Header().Get("Content-Type"); !strings.HasPrefix(ct, "text/plain") {
		t.Fatalf("content-type text/plain olmali: %q", ct)
	}

	other := httptest.NewRequest(http.MethodGet, "https://zorven.app/.well-known/mta-sts.txt", nil)
	other.Host = "zorven.app"
	if h.kind(other) == "mtasts" {
		t.Fatal("politika yalniz mta-sts. host'unda sunulmali")
	}
}

func TestMTASTSSettings(t *testing.T) {
	cases := []struct {
		mode, age string
		wantMode  string
		wantAge   int
		warns     int
	}{
		{"", "", "testing", 86400, 0},
		{"testing", "", "testing", 86400, 0},
		{"none", "", "none", 86400, 0},
		{"enforce", "", "enforce", 604800, 0},
		{"enforce", "604800", "enforce", 604800, 0},
		{"bogus", "", "testing", 86400, 1},
		{"testing", "99999999", "testing", 31557600, 1},
		{"enforce", "3600", "enforce", 3600, 1},
		{"testing", "abc", "testing", 86400, 1},
		{"testing", "-5", "testing", 86400, 1},
	}
	for _, c := range cases {
		m, a, w := mtaSTSSettings(c.mode, c.age)
		if m != c.wantMode || a != c.wantAge || len(w) != c.warns {
			t.Errorf("%q/%q -> %s %d %v", c.mode, c.age, m, a, w)
		}
	}
}

func TestMTASTSPolicyFromEnv(t *testing.T) {
	t.Setenv("ZORVEN_MTA_STS_MODE", "")
	t.Setenv("ZORVEN_MTA_STS_MAX_AGE", "")
	p0, id0, _ := MTASTSPolicyFromEnv("mail.x")
	want := "version: STSv1\r\nmode: testing\r\nmx: mail.x\r\nmax_age: 86400\r\n"
	if p0 != want {
		t.Fatalf("varsayilan politika degisti: %q", p0)
	}
	t.Setenv("ZORVEN_MTA_STS_MODE", "enforce")
	t.Setenv("ZORVEN_MTA_STS_MAX_AGE", "604800")
	p1, id1, w := MTASTSPolicyFromEnv("mail.x")
	if !strings.Contains(p1, "mode: enforce") || !strings.Contains(p1, "max_age: 604800") || len(w) != 0 {
		t.Fatalf("enforce: %q %v", p1, w)
	}
	if id0 == id1 || len(id1) != 16 {
		t.Fatalf("kimlik politika ile degismeli: %s %s", id0, id1)
	}
	if _, id2, _ := MTASTSPolicyFromEnv("mail.x"); id2 != id1 {
		t.Fatal("kimlik kararli olmali")
	}
}
