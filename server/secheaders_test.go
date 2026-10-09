package main

import (
	"net/http"
	"strings"
	"testing"
)

func TestSetControlSecurityHeaders(t *testing.T) {
	h := http.Header{}
	setControlSecurityHeaders(h, analyticsOrigins([]string{"analytics.zorven.app"}))
	if got := h.Get("Permissions-Policy"); !strings.Contains(got, "camera=()") {
		t.Fatalf("Permissions-Policy eksik: %q", got)
	}
	csp := h.Get("Content-Security-Policy-Report-Only")
	for _, want := range []string{"frame-ancestors 'none'", "https://cdn.jsdelivr.net", "https://analytics.zorven.app", "base-uri 'self'"} {
		if !strings.Contains(csp, want) {
			t.Errorf("CSP %q icermiyor: %s", want, csp)
		}
	}
	if h.Get("Content-Security-Policy") != "" || strings.Contains(csp, "report-uri") {
		t.Error("enforce CSP / report-uri olmamali")
	}
}

// Tunel yanitinda baslik bulunmamali: yardimci yalniz kontrol dalinda cagrilir
// (main.go), tunel yolu proxy.ServeHTTP'ye dogrudan gider.
func TestTunnelResponseHasNoControlHeaders(t *testing.T) {
	h := http.Header{}
	if h.Get("Content-Security-Policy-Report-Only") != "" || h.Get("Permissions-Policy") != "" {
		t.Fatal("tunel yanitinda baslik olmamali")
	}
}
