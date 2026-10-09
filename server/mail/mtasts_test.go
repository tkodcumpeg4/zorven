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

func TestMTASTSPolicyModes(t *testing.T) {
	if p := mtaSTSPolicy("enforce", "mail.x"); !strings.Contains(p, "mode: enforce") || !strings.Contains(p, "max_age: 604800") {
		t.Fatalf("enforce: %q", p)
	}
	if p := mtaSTSPolicy("bogus", "mail.x"); !strings.Contains(p, "mode: testing") {
		t.Fatalf("bilinmeyen mod testing'e dusmeli: %q", p)
	}
}
