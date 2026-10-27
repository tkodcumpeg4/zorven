package ingress

import (
	"net/http/httptest"
	"testing"
)

// Istemcinin X-Forwarded-For'u tek basina kalmaz; gercek IP sona eklenir
// (HTTP ve WebSocket ayni yardimciyi kullanir).
func TestSetForwardedHeadersAppendsRealIP(t *testing.T) {
	r := httptest.NewRequest("GET", "http://t.example/x", nil)
	r.RemoteAddr = "203.0.113.9:5555"
	h := map[string][]string{"X-Forwarded-For": {"1.2.3.4"}, "X-Forwarded-Host": {"evil"}}
	setForwardedHeaders(h, r)
	if got := h["X-Forwarded-For"][0]; got != "1.2.3.4, 203.0.113.9" {
		t.Fatalf("XFF: %q", got)
	}
	if h["X-Forwarded-Host"][0] != "t.example" || h["X-Forwarded-Proto"][0] != "http" {
		t.Fatalf("host/proto: %v", h)
	}
}
