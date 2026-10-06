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
	// 2. Panel hostlari. Platform domaininin HER alt alani KABUL EDILMEZ:
	// kiraci tunelleri de *.PlatformDomain altindadir ve kotu niyetli bir tunel
	// sayfasi panel oturumuyla terminal/ekran WS'i acmaya calisabilirdi.
	for _, h := range s.panelOriginHosts() {
		if originHost == h {
			return true
		}
	}
	return false
}

// panelOriginHosts, WS Origin'i olarak kabul edilen hostlar (port'suz, kucuk harf).
func (s *Server) panelOriginHosts() []string {
	var out []string
	add := func(h string) {
		h = strings.ToLower(strings.TrimSpace(strings.TrimSuffix(h, ".")))
		if hh, _, err := net.SplitHostPort(h); err == nil {
			h = hh
		}
		h = strings.TrimSuffix(strings.TrimPrefix(h, "["), "]")
		if h != "" {
			out = append(out, h)
		}
	}
	if len(s.PanelHosts) > 0 {
		for _, h := range s.PanelHosts {
			add(h)
		}
		return out
	}
	// Yapilandirma yoksa: yerel gelistirme + platformun kendi panel hostlari.
	for _, h := range []string{"localhost", "127.0.0.1", "::1"} {
		add(h)
	}
	if p := strings.ToLower(strings.TrimSuffix(s.PlatformDomain, ".")); p != "" {
		for _, pre := range []string{"", "www.", "panel.", "app."} {
			add(pre + p)
		}
	}
	return out
}
