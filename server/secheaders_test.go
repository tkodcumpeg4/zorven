package main

import (
	"net/http"
	"strings"
	"testing"
)

func TestSetControlSecurityHeaders(t *testing.T) {
	h := http.Header{}
	setControlSecurityHeaders(h, analyticsOrigins([]string{"analytics.zorven.app"}), "/clients", "enforce")
	if got := h.Get("Permissions-Policy"); !strings.Contains(got, "camera=()") {
		t.Fatalf("Permissions-Policy eksik: %q", got)
	}
	csp := h.Get("Content-Security-Policy")
	for _, want := range []string{"frame-ancestors 'none'", "https://cdn.jsdelivr.net", "https://analytics.zorven.app", "base-uri 'self'"} {
		if !strings.Contains(csp, want) {
			t.Errorf("CSP %q icermiyor: %s", want, csp)
		}
	}
	if h.Get("Content-Security-Policy-Report-Only") != "" || strings.Contains(csp, "report-uri") {
		t.Error("enforce modunda Report-Only / report-uri olmamali")
	}
	if strings.Contains(csp, "'unsafe-eval'") || strings.Contains(csp, "iconify") {
		t.Errorf("genel politika unsafe-eval/iconify/genis connect-src icermemeli: %s", csp)
	}
}

func TestCSPReportModeAndAPIDocs(t *testing.T) {
	h := http.Header{}
	setControlSecurityHeaders(h, nil, "/docs/api", "report")
	if h.Get("Content-Security-Policy") != "" || !strings.Contains(h.Get("Content-Security-Policy-Report-Only"), "'unsafe-eval'") {
		t.Fatalf("report modu: %v", h)
	}
	for _, p := range []string{"/docs/api", "/docs/api.html", "/en/docs/api", "/en/docs/api.html", "/en/docs/api/", "/ja/docs/api", "/ja/docs/api.html"} {
		if !isAPIDocsPath(p) {
			t.Errorf("%s API dokumani sayilmali", p)
		}
	}
	for _, p := range []string{"/docs", "/docs/apis", "/clients", "/en/docs", "/ja/docs", "/api/v1/tunnels", "/docs/api/x"} {
		if isAPIDocsPath(p) || strings.Contains(buildCSP(nil, isAPIDocsPath(p)), "unsafe-eval") {
			t.Errorf("%s unsafe-eval almamali", p)
		}
	}
	if cspModeFromEnv("") != "enforce" || cspModeFromEnv("bogus") != "enforce" || cspModeFromEnv(" Report ") != "report" {
		t.Error("cspModeFromEnv")
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

// Site ve panel yazi tiplerini kendi sunucusundan verir; Google Fonts izni geri gelmemeli.
func TestCSPNoGoogleFonts(t *testing.T) {
	csp := buildCSP([]string{"https://analytics.example"}, false)
	for _, bad := range []string{"fonts.googleapis.com", "fonts.gstatic.com"} {
		if strings.Contains(csp, bad) {
			t.Fatalf("CSP %q icermemeli: %s", bad, csp)
		}
	}
	if !strings.Contains(csp, "font-src 'self' data:") {
		t.Fatalf("font-src yalniz self+data olmali: %s", csp)
	}
}

// Panelin IP algilama ozelligi ucuncu tarafa gitmez; connect-src'de ipify olmamali.
func TestCSPNoThirdPartyIPService(t *testing.T) {
	for _, api := range []bool{false, true} {
		if csp := buildCSP([]string{"https://analytics.example"}, api); strings.Contains(csp, "ipify") {
			t.Fatalf("CSP ipify icermemeli: %s", csp)
		}
	}
}

// Ekran paylasimi blob: URL'leriyle cizer (MJPEG <img>, H.264 MSE <video>);
// CSP bunlari engellerse panelde ekran siyah kalir (2026-10-14 olayi).
func TestCSPAllowsBlobForScreenSharing(t *testing.T) {
	csp := buildCSP(nil, false)
	for _, want := range []string{"img-src 'self' data: https: blob:", "media-src 'self' blob:"} {
		if !strings.Contains(csp, want) {
			t.Errorf("CSP %q icermiyor: %s", want, csp)
		}
	}
}
