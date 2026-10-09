package main

import (
	"net/http"
	"strings"
)

// F-20: panel / kontrol duzlemi / pazarlama sitesi icin tarayici guvenlik
// basliklari. Tunel (musteri trafigi) yanitlarina UYGULANMAZ; yalnizca
// kontrol host dalinda cagrilir.

// permissionsPolicy: panel kamera/mikrofon/ekran paylasimi kullanmaz
// (web/app'te getUserMedia/getDisplayMedia yok), bu yuzden istisna yok.
const permissionsPolicy = "camera=(), microphone=(), geolocation=(), payment=(), usb=()"

// analyticsOrigins, analytics host adlarindan https origin listesi uretir.
func analyticsOrigins(hosts []string) []string {
	out := make([]string, 0, len(hosts))
	for _, h := range hosts {
		if h = strings.TrimSpace(h); h != "" {
			out = append(out, "https://"+h)
		}
	}
	return out
}

// buildCSPReportOnly, Report-Only CSP degerini uretir. Harici kaynaklar:
// Scalar (cdn.jsdelivr.net), Google Fonts, self-hosted Umami (analytics host).
func buildCSPReportOnly(analytics []string) string {
	script := append([]string{"'self'", "'unsafe-inline'", "https://cdn.jsdelivr.net"}, analytics...)
	parts := []string{
		"default-src 'self'",
		"script-src " + strings.Join(script, " "),
		"style-src 'self' 'unsafe-inline' https://fonts.googleapis.com https://cdn.jsdelivr.net",
		"img-src 'self' data: https:",
		"font-src 'self' data: https://fonts.gstatic.com",
		"connect-src 'self' wss: https:",
		"frame-ancestors 'none'",
		"base-uri 'self'",
		"form-action 'self'",
	}
	return strings.Join(parts, "; ")
}

// setControlSecurityHeaders, CSP Report-Only + Permissions-Policy ekler.
func setControlSecurityHeaders(h http.Header, analytics []string) {
	h.Set("Permissions-Policy", permissionsPolicy)
	h.Set("Content-Security-Policy-Report-Only", buildCSPReportOnly(analytics))
}
