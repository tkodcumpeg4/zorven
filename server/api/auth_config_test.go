package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

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
