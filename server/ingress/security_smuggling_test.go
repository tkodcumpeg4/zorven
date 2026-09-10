package ingress

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestSecurity_ForwardHeaders_HopByHop_And_Smuggling(t *testing.T) {
	req := httptest.NewRequest("GET", "http://example.com/test", nil)
	req.RemoteAddr = "203.0.113.195:54321"

	// Add static hop-by-hop headers
	req.Header.Set("Connection", "close, X-Dynamic-Hop, X-Another-Hop")
	req.Header.Set("Keep-Alive", "timeout=5")
	req.Header.Set("Transfer-Encoding", "chunked")
	req.Header.Set("Upgrade", "websocket")
	req.Header.Set("Proxy-Connection", "keep-alive")

	// Add dynamic hop-by-hop headers defined in Connection
	req.Header.Set("X-Dynamic-Hop", "malicious_injected_value")
	req.Header.Set("X-Another-Hop", "secret")

	// Legitimate headers
	req.Header.Set("User-Agent", "SecurityTester/1.0")
	req.Header.Set("Authorization", "Bearer test_token")

	forwarded := forwardHeaders(req)

	// Check standard hop-by-hop headers are removed
	for _, forbidden := range []string{"connection", "keep-alive", "transfer-encoding", "upgrade", "proxy-connection"} {
		for k := range forwarded {
			if http.CanonicalHeaderKey(k) == http.CanonicalHeaderKey(forbidden) {
				t.Errorf("forbidden hop-by-hop header %q was forwarded!", k)
			}
		}
	}

	// Check dynamic hop-by-hop headers declared in Connection are removed
	for _, forbidden := range []string{"X-Dynamic-Hop", "X-Another-Hop"} {
		if _, ok := forwarded[forbidden]; ok {
			t.Errorf("dynamic hop-by-hop header %q was forwarded!", forbidden)
		}
	}

	// Check legitimate headers are preserved
	if forwarded["User-Agent"][0] != "SecurityTester/1.0" {
		t.Errorf("expected User-Agent preserved, got %v", forwarded["User-Agent"])
	}

	// Check proxy headers correctly added
	if forwarded["X-Forwarded-For"][0] != "203.0.113.195" {
		t.Errorf("expected X-Forwarded-For=203.0.113.195, got %v", forwarded["X-Forwarded-For"])
	}
	if forwarded["X-Forwarded-Proto"][0] != "http" {
		t.Errorf("expected X-Forwarded-Proto=http, got %v", forwarded["X-Forwarded-Proto"])
	}
	if forwarded["X-Forwarded-Host"][0] != "example.com" {
		t.Errorf("expected X-Forwarded-Host=example.com, got %v", forwarded["X-Forwarded-Host"])
	}
}

func TestSecurity_NormalizeHost_InjectionProtection(t *testing.T) {
	badHosts := []string{
		"example.com\r\ninjected.com",
		"example.com\n",
		"example.com\x00evil.com",
		"example.com evil.com",
		"api.example.com\tinjected.com",
	}

	for _, bad := range badHosts {
		normalized := normalizeHost(bad)
		if normalized != "" {
			t.Errorf("normalizeHost(%q) should return empty string for injected host, got %q", bad, normalized)
		}
	}

	validHosts := map[string]string{
		"api.example.com:8443": "api.example.com",
		"API.EXAMPLE.COM":      "api.example.com",
		"[::1]:8443":           "[::1]",
		"example.com.":         "example.com",
	}

	for input, expected := range validHosts {
		got := normalizeHost(input)
		if got != expected {
			t.Errorf("normalizeHost(%q) = %q, expected %q", input, got, expected)
		}
	}
}
