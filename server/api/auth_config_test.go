package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

// Eski Go-tarafi GitHub girisi izin listesi OLMADAN acilmaz (acik kayit yolu yoktur; Ek 1 madde 20:
// kullanilmayan --github-open-signup bayragi kaldirildi). Better Auth GitHub'i buradan bagimsizdir.
func TestGitHubAuthEnabledRequiresAllowList(t *testing.T) {
	cases := []struct {
		name string
		g    GitHubAuth
		want bool
	}{
		{"hepsi var", GitHubAuth{ClientID: "id", ClientSecret: "sec", Allow: map[string]bool{"ali": true}}, true},
		{"izin listesi bos", GitHubAuth{ClientID: "id", ClientSecret: "sec"}, false},
		{"izin listesi bos harita", GitHubAuth{ClientID: "id", ClientSecret: "sec", Allow: map[string]bool{}}, false},
		{"secret yok", GitHubAuth{ClientID: "id", Allow: map[string]bool{"ali": true}}, false},
		{"client id yok", GitHubAuth{ClientSecret: "sec", Allow: map[string]bool{"ali": true}}, false},
	}
	for _, c := range cases {
		if got := c.g.Enabled(); got != c.want {
			t.Errorf("%s: Enabled()=%v, beklenen %v", c.name, got, c.want)
		}
	}
}

// authConfig platform domainini panele bildirir (rezerve-port adresi icin).
func TestAuthConfig_PlatformDomain(t *testing.T) {
	s := &Server{PlatformDomain: "zorven.app"}
	rec := httptest.NewRecorder()
	s.authConfig(rec, httptest.NewRequest(http.MethodGet, "/api/v1/auth/config", nil))
	var out map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	if out["platform_domain"] != "zorven.app" {
		t.Fatalf("platform_domain = %v", out["platform_domain"])
	}
}
