package api

import (
	"mime"
	"net"
	"net/http"
	"net/url"
	"strings"
)

// CSRF korumasi (cerezle kimliklenen durum-degistiren istekler).
//
// Neden gerekli: kiraci tunelleri (ad--slug.<domain>) ve panel (panel.<domain>)
// AYNI SITE'dir; SameSite=Lax cerezler tunel sayfasindan panele giden isteklere
// eklenir. Kotu niyetli bir tunel sayfasi "basit istek" (text/plain govde,
// preflight'siz) ile panel yazma uclarini tetikleyebilirdi.
//
// Kural (web/server/lib/auth.ts trustedOrigins ile AYNI liste):
//   - Origin (yoksa Referer) host'u guvenilir olmali: istegin kendi kontrol
//     host'u (ayni origin), platform apex/www/panel/app, --control-host
//     degerleri ve gelistirme loopback'leri. Kiraci tunel alt alanlari GUVENILMEZ.
//   - Govdeli isteklerde Content-Type application/json olmali (basit istek
//     turleri text/plain, form ve multipart reddedilir).
// Bearer / admin anahtari / API token istekleri muaftir: tarayici bu basliklari
// capraz sitede preflight'siz ekleyemez.

// csrfSafeMethod, durum degistirmeyen yontemler.
func csrfSafeMethod(m string) bool {
	return m == http.MethodGet || m == http.MethodHead || m == http.MethodOptions || m == http.MethodTrace
}

// TrustedOriginHosts, platform domaini ve kontrol host'larindan guvenilir
// origin host listesini uretir (kucuk harf, portsuz). auth.ts ile ayni kural.
func TrustedOriginHosts(platformDomain string, controlHosts []string) []string {
	seen := map[string]bool{}
	var out []string
	add := func(h string) {
		h = normalizeOriginHost(h)
		if h == "" || seen[h] {
			return
		}
		seen[h] = true
		out = append(out, h)
	}
	if pd := strings.TrimSuffix(strings.ToLower(strings.TrimSpace(platformDomain)), "."); pd != "" {
		add(pd)
		add("www." + pd)
		add("panel." + pd)
		add("app." + pd)
	}
	for _, h := range controlHosts {
		add(h)
	}
	add("localhost")
	add("127.0.0.1")
	add("::1")
	return out
}

// normalizeOriginHost, "Host:port", "[::1]:8443" veya tam URL'den portsuz,
// kucuk harfli host cikarir.
func normalizeOriginHost(h string) string {
	h = strings.ToLower(strings.TrimSpace(h))
	if h == "" {
		return ""
	}
	if strings.Contains(h, "://") {
		if u, err := url.Parse(h); err == nil {
			return strings.TrimSuffix(u.Hostname(), ".")
		}
		return ""
	}
	if host, _, err := net.SplitHostPort(h); err == nil {
		h = host
	}
	h = strings.TrimPrefix(strings.TrimSuffix(h, "]"), "[")
	return strings.TrimSuffix(h, ".")
}

// requestOriginHost, istegin Origin (yoksa Referer) basligindan host'u doner.
// Ikisi de yoksa veya Origin "null" ise "".
func requestOriginHost(r *http.Request) string {
	src := r.Header.Get("Origin")
	if src == "" {
		src = r.Header.Get("Referer")
	}
	if src == "" || src == "null" {
		return ""
	}
	u, err := url.Parse(src)
	if err != nil || (u.Scheme != "https" && u.Scheme != "http") {
		return ""
	}
	return strings.TrimSuffix(strings.ToLower(u.Hostname()), ".")
}

// originTrusted, istegin Origin/Referer'inin guvenilir olup olmadigini soyler.
func (m *Middleware) originTrusted(r *http.Request) bool {
	oh := requestOriginHost(r)
	if oh == "" {
		return false
	}
	// Ayni origin: /api/v1 yalnizca kontrol host'larinda (veya IP) servis
	// edilir; istegin kendi Host'u bu yuzden panel host'udur.
	if oh == normalizeOriginHost(r.Host) {
		return true
	}
	for _, h := range m.TrustedHosts {
		if oh == normalizeOriginHost(h) {
			return true
		}
	}
	return oh == "localhost" || oh == "127.0.0.1" || oh == "::1"
}

// csrfCheck, cerezle kimliklenen bir istegi denetler. Gecerse true; aksi halde
// 403/415 yazar ve false doner.
func (m *Middleware) csrfCheck(w http.ResponseWriter, r *http.Request) bool {
	if csrfSafeMethod(r.Method) {
		return true
	}
	// Authorization basligi tasiyan istek capraz sitede preflight'siz
	// uretilemez (admin anahtari / API token / Better Auth bearer).
	if r.Header.Get("Authorization") != "" {
		return true
	}
	if !m.originTrusted(r) {
		writeJSONError(w, http.StatusForbidden, "csrf_origin",
			"istek kaynagi (Origin) guvenilir degil")
		return false
	}
	if r.ContentLength != 0 || len(r.TransferEncoding) > 0 {
		mt, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
		if err != nil || mt != "application/json" {
			writeJSONError(w, http.StatusUnsupportedMediaType, "unsupported_media_type",
				"govdeli istekler Content-Type: application/json olmali")
			return false
		}
	}
	return true
}
