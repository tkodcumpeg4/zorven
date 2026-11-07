package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// Ek 1 madde 28: joker (*.) ozel alan adlari ACME ile sertifika alamaz; API acik bir hatayla
// reddeder (mevcut hata bicimi: 422 + {"error":{"code","message"}}).
func TestCreateCustomHostnameRejectsWildcard(t *testing.T) {
	s := &Server{PlatformDomain: "zorven.app"}
	for _, fqdn := range []string{"*.example.com", "*.Example.com.", "*.sub.example.com"} {
		r := httptest.NewRequest("POST", "/api/v1/hostnames/custom", strings.NewReader(`{"fqdn":"`+fqdn+`"}`))
		r = r.WithContext(withTenant(r.Context(), "ten_1"))
		w := httptest.NewRecorder()
		s.createCustomHostname(w, r)

		if w.Code != http.StatusUnprocessableEntity || errCode(t, w) != "invalid_domain" {
			t.Fatalf("%s: 422 invalid_domain beklenirdi: %d %s", fqdn, w.Code, w.Body.String())
		}
		var out struct {
			Error struct {
				Message string `json:"message"`
			} `json:"error"`
		}
		_ = json.Unmarshal(w.Body.Bytes(), &out)
		if !strings.Contains(out.Error.Message, "joker alan adlari") || !strings.Contains(out.Error.Message, "ayri ekleyin") {
			t.Fatalf("%s: mesaj jokeri acikca anlatmali: %q", fqdn, out.Error.Message)
		}
	}
}
