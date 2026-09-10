package api

import (
	"encoding/json"
	"net"
	"net/http"
	"net/url"
	"strings"
)

// writeJSONError, api_contract.md §1 hata bicimini yazar.
func writeJSONError(w http.ResponseWriter, status int, code, msg string) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(map[string]any{
		"error": map[string]string{"code": code, "message": msg},
	})
}

// clientIP, hiz sinirlama anahtari. X-Forwarded-For BILEREK kullanilmiyor —
// uydurulabilir; bkz. server/tunnel/handler.go icindeki ayni gerekce.
func clientIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

// isAllowedOrigin, WebSocket bağlantılarının CSWSH (Cross-Site WebSocket Hijacking)
// saldırılarına karşı korunması için Origin başlığını doğrular.
func (s *Server) isAllowedOrigin(r *http.Request) bool {
	origin := r.Header.Get("Origin")
	if origin == "" {
		return true // Tarayıcı dışı istemciler (CLI / API araçları) Origin göndermez
	}
	u, err := url.Parse(origin)
	if err != nil {
		return false
	}
	originHost := strings.ToLower(u.Hostname())
	reqHost := strings.ToLower(r.Host)
	if h, _, err := net.SplitHostPort(reqHost); err == nil {
		reqHost = h
	}

	// 1. Same-origin (isteğin geldiği host ile origin hostu aynı)
	if originHost == reqHost {
		return true
	}
	// 2. Localhost geliştirme ortamı
	if originHost == "localhost" || originHost == "127.0.0.1" || originHost == "::1" {
		return true
	}
	// 3. Platform domaini veya platform alt alanı
	if s.PlatformDomain != "" {
		pDomain := strings.ToLower(strings.TrimSuffix(s.PlatformDomain, "."))
		if originHost == pDomain || strings.HasSuffix(originHost, "."+pDomain) {
			return true
		}
	}
	return false
}
