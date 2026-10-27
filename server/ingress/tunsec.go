package ingress

// ZTNA Z3: tunel seviyesinde Geo + WAF uygulamasi. Ayarlar Router.Reload'da
// derlenmistir (Router.SecurityFor); burada yalniz hazir yapi uygulanir.

import (
	"net/http"
	"strings"
	"time"

	"github.com/tkodcumpeg4/zorven/server/reqlog"
	"github.com/tkodcumpeg4/zorven/server/store"
	"github.com/tkodcumpeg4/zorven/server/tunsec"
	"github.com/tkodcumpeg4/zorven/shared/protocol"
)

// Z3 reject_reason on-ekleri ve etiketleri.
const (
	RejectGeoPrefix  = "geo:"      // geo:<ULKE|unknown>   (engellendi, 403)
	RejectWAFPrefix  = "waf:"      // waf:<kural>          (engellendi, 403)
	RejectWAFLog     = "waf_log:"  // waf_log:<kural>      (yalniz gozlem, istek gecti; durum 0)
	RejectChallenge  = "challenge" // politika "challenge" eylemi, ziyaretci girisi gerekti
	requestIDHeader  = "X-Zorven-Request-Id"
	secDeniedMessage = "İstek güvenlik politikası tarafından engellendi."
)

// internalVisitorPath, Zorven'in kendi ziyaretci/giris uclaridir (WAF'tan muaf).
func internalVisitorPath(p string) bool {
	return strings.HasPrefix(p, "/_zva/") || strings.HasPrefix(p, "/_zvb/") || strings.HasPrefix(p, "/_zvd/")
}

// enforceTunnelGeo, tunelin ulke kuralini uygular. true = istek sonlandirildi.
func (h *Handler) enforceTunnelGeo(w http.ResponseWriter, r *http.Request, tun store.HostRoute, sec *tunsec.Compiled, started time.Time) bool {
	if !sec.GeoActive() {
		return false
	}
	ok, cc := sec.GeoCheck(h.Router, clientIP(r))
	if ok {
		return false
	}
	id := reqlog.NewID()
	w.Header().Set(requestIDHeader, id)
	writeError(w, r, http.StatusForbidden, protocol.CodeAccessDenied,
		"Bu bölgeden erişime izin verilmiyor. Istek kimligi: "+id)
	h.logRejectID(tun, r, http.StatusForbidden, RejectGeoPrefix+cc, started, id)
	return true
}

// enforceTunnelWAF, tunelin WAF ayarini uygular. true = istek sonlandirildi.
// log kipinde istek gecer ama bulgu reqlog'a "waf_log:<kural>" olarak yazilir.
func (h *Handler) enforceTunnelWAF(w http.ResponseWriter, r *http.Request, tun store.HostRoute, sec *tunsec.Compiled, started time.Time) bool {
	if !sec.WAFActive() || internalVisitorPath(r.URL.Path) {
		return false
	}
	hit, found := sec.ScanRequest(r)
	if !found {
		hit, found = sec.ScanBody(r)
	}
	if !found {
		return false
	}
	id := reqlog.NewID()
	if sec.WAFMode != tunsec.WAFBlock {
		h.logRejectID(tun, r, 0, RejectWAFLog+hit.Rule, started, id)
		return false
	}
	w.Header().Set(requestIDHeader, id)
	if h.Log != nil {
		h.Log.Debug("tunel WAF engelledi", "tunel", tun.TunnelID, "ip", clientIP(r), "kural", hit.Rule, "kimlik", id)
	}
	writeError(w, r, http.StatusForbidden, protocol.CodeAccessDenied,
		secDeniedMessage+" Istek kimligi: "+id)
	h.logRejectID(tun, r, http.StatusForbidden, RejectWAFPrefix+hit.Rule, started, id)
	return true
}

// enforceChallenge, politika "challenge" eylemi: tunelin ziyaretci OAuth'una
// yonlendirir (oturum gecerliyse gecer). Ziyaretci OAuth'u sunucuda yapilandirilmamissa
// enforceOAuth fail-closed 403 doner ("yoksa deny"). ok=true: devam edilebilir;
// degilse yanit yazilmistir ve status log icindir.
func (h *Handler) enforceChallenge(w http.ResponseWriter, r *http.Request, tun store.HostRoute) (ok bool, status int) {
	syn := tun
	syn.AccessEnabled = true
	if tun.AccessMode != "oauth" || !tun.AccessEnabled {
		// Tunelin kendi modu oauth degilse: saglayici/e-posta kisiti yok, kimligi
		// dogrulanmis herkes gecer (challenge = kimlik kaniti).
		syn.AccessMode = "oauth"
		syn.AccessConfig = []byte(`{}`)
	}
	arec := &recorder{ResponseWriter: w, status: http.StatusOK}
	ok = h.enforceOAuth(arec, r, syn)
	return ok, arec.status
}
