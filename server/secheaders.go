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

// CSP modu: ZORVEN_CSP_MODE=enforce (varsayilan) | report (acil geri donus).
const (
	cspEnforce = "enforce"
	cspReport  = "report"
)

// cspModeFromEnv, ortam degiskeninden modu okur; bilinmeyen deger enforce'a duser.
func cspModeFromEnv(v string) string {
	if strings.EqualFold(strings.TrimSpace(v), cspReport) {
		return cspReport
	}
	return cspEnforce
}

// isAPIDocsPath: Scalar API dokumani sayfalari (TR/EN/JA, .html'li ve sonda / ile).
func isAPIDocsPath(p string) bool {
	p = strings.TrimSuffix(strings.TrimSuffix(p, "/"), ".html")
	return p == "/docs/api" || p == "/en/docs/api" || p == "/ja/docs/api"
}

// buildCSP, yola gore CSP degerini uretir. Harici kaynaklar: Scalar
// (cdn.jsdelivr.net, surum sabit + SRI) ve self-hosted Umami (analytics host).
// 'unsafe-eval' YALNIZ API dokumani sayfalarinda (Scalar tam spec islerken ister).
// Google Fonts YOK; panel ikonlari derlemede paketli (api.iconify.design YOK),
// bu yuzden connect-src yalniz self + wss + analytics.
func buildCSP(analytics []string, apiDocs bool) string {
	script := []string{"'self'", "'unsafe-inline'", "https://cdn.jsdelivr.net"}
	if apiDocs {
		script = append(script, "'unsafe-eval'")
	}
	script = append(script, analytics...)
	// "IP'mi algila" (tokens.vue) artik kendi ucumuzu kullanir (/api/v1/my-ip); ucuncu taraf yok.
	connect := append([]string{"'self'", "wss:"}, analytics...)
	parts := []string{
		"default-src 'self'",
		"script-src " + strings.Join(script, " "),
		"style-src 'self' 'unsafe-inline' https://cdn.jsdelivr.net",
		// blob: ekran paylasimi icin zorunlu: MJPEG kareleri <img src=blob:>,
		// H.264 MSE akisi <video src=blob:> ile cizilir.
		"img-src 'self' data: https: blob:",
		"media-src 'self' blob:",
		"font-src 'self' data:",
		"connect-src " + strings.Join(connect, " "),
		"frame-ancestors 'none'",
		"base-uri 'self'",
		"form-action 'self'",
	}
	return strings.Join(parts, "; ")
}

// setControlSecurityHeaders, CSP + Permissions-Policy ekler. mode report ise
// Report-Only basligi, aksi halde zorunlu Content-Security-Policy.
func setControlSecurityHeaders(h http.Header, analytics []string, path, mode string) {
	h.Set("Permissions-Policy", permissionsPolicy)
	name := "Content-Security-Policy"
	if mode == cspReport {
		name += "-Report-Only"
	}
	h.Set(name, buildCSP(analytics, isAPIDocsPath(path)))
}
