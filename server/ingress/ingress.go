package ingress

import (
	"context"
	"crypto/x509"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/tkodcumpeg4/zorven/server/accesslog"
	"github.com/tkodcumpeg4/zorven/server/bandwidth"
	"github.com/tkodcumpeg4/zorven/server/door"
	"github.com/tkodcumpeg4/zorven/server/events"
	"github.com/tkodcumpeg4/zorven/server/ipfilter"
	"github.com/tkodcumpeg4/zorven/server/ratelimit"
	"github.com/tkodcumpeg4/zorven/server/reqlog"
	"github.com/tkodcumpeg4/zorven/server/store"
	"github.com/tkodcumpeg4/zorven/server/tunnel"
	"github.com/tkodcumpeg4/zorven/server/visitorauth"
	"github.com/tkodcumpeg4/zorven/shared/protocol"
)

// UpstreamTimeout, istemcinin yanit basligini gondermesi icin taninan sure.
// Asilirsa 504 donulur (api_contract.md §3).
const UpstreamTimeout = 30 * time.Second

// hopByHop, RFC 7230 uyarinca proxy tarafindan iletilmemesi gereken basliklar.
// Bunlar tek bir baglantiya ozgudur; ileri tasinirlarsa tunelin diger ucundaki
// baglantiyi bozarlar (ornegin Connection: close tuneli kapatmaya calisirdi).
var hopByHop = map[string]bool{
	"connection":          true,
	"keep-alive":          true,
	"proxy-authenticate":  true,
	"proxy-authorization": true,
	"proxy-connection":    true,
	"te":                  true,
	"trailer":             true,
	"transfer-encoding":   true,
	"upgrade":             true,
}

// Handler, control_hostname disindaki tum hostname'ler icin catch-all proxy.
type Handler struct {
	Router *Router
	Hub    *tunnel.Hub
	Log    *slog.Logger

	// ReqLog ve Events, dashboard'un canli gorunumunu besler. nil olabilir.
	ReqLog *reqlog.Ring
	Events *events.Broker

	// Captures, FAZ 2 istek inspector: opt-in tam istek/yanit yakalama deposu.
	// nil veya kiraci icin kapaliysa hicbir yakalama yapilmaz (sifir maliyet).
	Captures *reqlog.CaptureStore

	// LogPersister, istek kayitlarini Postgres'e toplu yazar. nil olabilir
	// (kalicilik kapali; yalnizca bellek ici ring).
	LogPersister *reqlog.Persister

	// ReqLimiter, TUNEL BASINA istek hizini sinirlar.
	// Anahtar tunel ID'sidir, istemci IP'si degil: amac bir tunelin
	// arkasindaki yerel servisi ve sunucuyu asiri yukten korumak.
	// nil ise sinirlama yapilmaz.
	ReqLimiter *ratelimit.Limiter

	// IPFilter, ingress seviyesinde CIDR bazli IP izin listesi motoru.
	IPFilter *ipfilter.Engine

	// BandwidthRecorder ve Tracker, gercek data-plane bant genisligi sinirlamasi ve kaydini saglar.
	BandwidthRecorder bandwidth.UsageRecorder
	BandwidthTracker  *bandwidth.LocalUsageRecorder

	// Visitor, tunel arkasindaki servise erisen dis ziyaretcileri OAuth ile
	// dogrular (mode=oauth). nil ise oauth modu devre disidir.
	Visitor *visitorauth.Manager

	// BasicSecret, mode=basic tarayici oturum cerezini (_zvb_session) imzalayan
	// sir. Bos ise form/cerez devre disidir ve tarayicilar eski yerel Basic Auth
	// penceresini gorur.
	BasicSecret []byte

	// BasicLimiter, basarisiz Basic giris denemelerini (tunel+IP basina 10/10dk)
	// sinirlar. nil ise ilk kullanimda varsayilan olusturulur.
	BasicLimiter *ratelimit.Limiter
	basicRLOnce  sync.Once

	// Door, ham TCP/UDP tunelleri icin web ile kapi acma servisi. nil ise kapi
	// hostname'leri 404 doner.
	Door *door.Service

	// AccessEvents, ziyaretci erisim olaylarini (istatistik) toplu yazar. nil olabilir.
	AccessEvents *accesslog.Persister

	// PlatformDomain, kiraci subdomainlerinin altinda oldugu domain (or.
	// zorven.app). FAZ 4: yalnizca *.PlatformDomain tunellerinde ve ucretsiz
	// katmanda uyari ara-sayfasi (interstitial) gosterilir. Bos ise kapali.
	PlatformDomain string

	// rr (FAZ 5 / HA): hostname basina round-robin sayaci. Cok-replikalı
	// tunellerde istekleri cevrimici agent'lar arasinda dagitir. Zero-value hazir.
	rr roundRobin

	// rejectLim, ingress'in proxy'lemeden reddettigi isteklerin loglanmasini
	// tunel basina sinirlar (tarama/bot seli request_logs'u doldurmasin).
	rejectLim rejectLimiter
}

func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	started := time.Now()
	// WebSocket yukseltmeleri (Upgrade: websocket) artik desteklenir: tunel
	// kontrolleri (tunel aktif mi, IP izin listesi, hiz siniri, istemci
	// cevrimici mi) yapildiktan sonra serveWebSocket'e dallanilir. Bkz. ServeHTTP
	// sonundaki isWebSocketUpgrade kontrolu.

	// Y1: TLS baglantisinda SNI ile Host basligi AYNI host olmali. mTLS (ve
	// istemci sertifikasi istegi) el sikismada SNI'ye gore verilir; SNI'yi
	// mTLS'siz bir host yapip Host basligini korumali tunele ayarlayan istemci
	// sertifika gostermeden korumali tunele ulasirdi. RFC 9110 15.5.20 / RFC 9113
	// 9.1.2: 421 alan tarayici (HTTP/2 baglanti birlestirme) yeni baglanti acar.
	if r.TLS != nil && r.TLS.ServerName != "" &&
		normalizeHost(r.TLS.ServerName) != normalizeHost(r.Host) {
		writeError(w, r, http.StatusMisdirectedRequest, codeMisdirected,
			"Bu baglanti istenen host icin kurulmadi; yeni bir baglanti ile tekrar deneyin.")
		// Host bir tunele cozumleniyorsa (kiraci biliniyor) reddi logla.
		if t, found := h.Router.LookupPath(r.Host, r.URL.Path); found && t.Door == nil {
			h.logReject(t, r, http.StatusMisdirectedRequest, RejectMisdirected, started)
		}
		return
	}

	// FAZ 6.5: yol tabanlı yönlendirme — host + istek yoluna göre tünel seçilir.
	tun, ok := h.Router.LookupPath(r.Host, r.URL.Path)
	if !ok {
		writeError(w, r, http.StatusNotFound, protocol.CodeTunnelNotFound,
			"Bu hostname hicbir tunele bagli degil: "+r.Host)
		return
	}
	if !tun.Enabled {
		writeError(w, r, http.StatusServiceUnavailable, protocol.CodeTunnelDisabled,
			"Bu tunel su anda pasif.")
		return
	}

	// FAZ 4: platform admin bu tuneli kotuye kullanim nedeniyle dondurmusssa
	// icerik proxy'lenmez; "askiya alindi" yaniti doner.
	if tun.Frozen {
		h.serveSuspended(w, r)
		return
	}

	// Web door hostname'i (ham TCP/UDP tunelinin kapisi): proxy yok; giris + grant
	// akisi. Tunelin statik IP izin listesi BURADA uygulanmaz (ziyaretcinin
	// kapiya ulasabilmesi gerekir; liste ham porta uygulanir).
	if tun.Door != nil {
		h.serveDoor(w, r, tun)
		return
	}

	// Y1: route mTLS istiyorsa dogrulanmis istemci sertifikasi SART. El
	// sikismadaki dogrulamaya guvenmek yetmez: baglanti mTLS acilmadan once
	// kurulmus olabilir (keep-alive) veya istek yol kuraliyla CA'si farkli
	// baska bir tunele gidiyor olabilir. Sertifika tunelin KENDI CA'sina gore
	// yeniden dogrulanir; CA cozulemezse fail-closed.
	if tun.MTLSEnabled && !h.clientCertOK(r, tun.TunnelID) {
		writeError(w, r, http.StatusForbidden, protocol.CodeAccessDenied,
			"Bu tunel istemci sertifikasi (mTLS) gerektirir.")
		h.logReject(tun, r, http.StatusForbidden, RejectMTLSRequired, started)
		return
	}

	// Ingress IP Izin Listesi (IP Filtering) Kontrolu
	if h.IPFilter != nil && tun.TenantID != "" {
		ip := clientIP(r)
		allowed, err := h.IPFilter.CheckAllowed(r.Context(), tun.TenantID, tun.TunnelID, ip)
		if err == nil && !allowed {
			h.Log.Warn("IP izin listesi tarafindan engellendi",
				"ip", ip, "tunel", tun.TunnelID, "tenant", tun.TenantID, "hostname", tun.FQDN)
			writeError(w, r, http.StatusForbidden, protocol.CodeIPForbidden,
				"Erişim engellendi: IP adresiniz ("+ip+") bu tünelin izin listesinde bulunmuyor.")
			h.logReject(tun, r, http.StatusForbidden, RejectIPForbidden, started)
			return
		}
	}

	// Ziyaretçi OAuth uçları (/_zva/start, /_zva/finish) erişim denetiminden
	// MUAFTIR — kimlik doğrulamanın kendisidir. IP filtresinden sonra, erişim
	// denetiminden önce ele alınır.
	if h.Visitor != nil && h.Visitor.Enabled() && visitorauth.HandlesPath(r.URL.Path) {
		h.Visitor.ServeTunnelEndpoint(w, r)
		return
	}

	// Basic Auth giriş formu uçları (/_zvb/login, /_zvb/logout) da erişim
	// denetiminden muaftır ve ASLA istemci uygulamasına iletilmez.
	if strings.HasPrefix(r.URL.Path, "/_zvb/") {
		h.serveBasicEndpoint(w, r, tun)
		return
	}

	// Tünel erişim denetimi (Basic Auth / OAuth). Politika yoksa/kapalıysa
	// tünel herkese açıktır (geriye uyumlu). Reddedilirse yanıt yazılmıştır.
	arec := &recorder{ResponseWriter: w, status: http.StatusOK}
	if !h.enforceAccess(arec, r, tun) {
		h.logReject(tun, r, arec.status, accessRejectReason(arec.status), started)
		return
	}

	// FAZ 4: ücretsiz katman platform-domain tünellerinde ilk ziyarette uyarı
	// ara-sayfası göster (phishing/kötüye kullanım caydırma). Onay çerezi varsa
	// veya ücretli katman / özel domain / asset-XHR isteğiyse atlanır.
	if h.shouldShowInterstitial(tun, r) {
		irec := &recorder{ResponseWriter: w, status: http.StatusOK}
		h.serveInterstitial(irec, r)
		h.logReject(tun, r, irec.status, RejectInterstitial, started)
		return
	}

	// FAZ 6: trafik politikasi (parse edilmis, hot-path'te sadece uygulanir).
	// O4: yol kuraliyla baska tunele giden istek HEDEF tunelin politikasini alir.
	tp := h.Router.trafficForRoute(r.Host, tun, r.URL.Path)

	// FAZ 1 (F04): birlesik policy motoru. Erisim denetiminden sonra, istemciye is
	// gonderilmeden once degerlendirilir. Terminal karar (deny/redirect/429/mTLS)
	// istegi burada sonlandirir; set_header kurallari trafik politikasina merge edilir.
	// mtlsOK: zincir el sikismada dogrulandi VE SNI bu host (yukaridaki 421
	// denetimi farkli SNI'yi zaten reddeder; burada savunma derinligi).
	mtlsOK := r.TLS != nil && len(r.TLS.VerifiedChains) > 0 &&
		normalizeHost(r.TLS.ServerName) == normalizeHost(r.Host)
	// O4: host'a bagli policy'ler + yol kuraliyla gidilen tunelin policy'leri.
	pol := h.Router.evaluatePolicyList(h.Router.policiesForRoute(r.Host, tun), r, clientIP(r), tun.TunnelID, mtlsOK)
	if pol.terminal() {
		switch {
		case pol.denyStatus > 0:
			msg := pol.denyMessage
			if msg == "" {
				msg = "Erişim policy tarafından engellendi."
			}
			writeError(w, r, pol.denyStatus, protocol.CodeAccessDenied, msg)
			h.logReject(tun, r, pol.denyStatus, RejectPolicyDeny, started)
		case pol.redirectLoc != "":
			http.Redirect(w, r, pol.redirectLoc, pol.redirectStatus)
			h.logReject(tun, r, pol.redirectStatus, RejectPolicyRedirect, started)
		case pol.rateLimited:
			w.Header().Set("Retry-After", strconv.Itoa(int(pol.retryAfter.Seconds())))
			writeError(w, r, http.StatusTooManyRequests, protocol.CodeRateLimited,
				"İstek hız sınırı aşıldı. Lütfen sonra tekrar deneyin.")
			h.logReject(tun, r, http.StatusTooManyRequests, RejectRateLimited, started)
		case pol.mtlsFailed:
			writeError(w, r, http.StatusForbidden, protocol.CodeAccessDenied,
				"Bu kaynak istemci sertifikası (mTLS) gerektirir.")
			h.logReject(tun, r, http.StatusForbidden, RejectMTLSRequired, started)
		case pol.wafBlocked:
			// Hangi kuralin eslestigi YALNIZCA logda; yanit bilgi sizdirmaz.
			h.Log.Warn("waf tarafindan engellendi",
				"tunel", tun.TunnelID, "hostname", tun.FQDN,
				"ip", clientIP(r), "yol", r.URL.Path, "kural", pol.wafRule)
			writeError(w, r, http.StatusForbidden, protocol.CodeAccessDenied,
				"İstek güvenlik politikası tarafından engellendi.")
			h.logReject(tun, r, http.StatusForbidden, RejectWAFBlocked, started)
		case pol.webhookFailed:
			st := pol.webhookStatus
			if st == 0 {
				st = http.StatusUnauthorized
			}
			h.Log.Warn("webhook imza dogrulamasi basarisiz",
				"tunel", tun.TunnelID, "hostname", tun.FQDN, "durum", st)
			writeError(w, r, st, protocol.CodeAccessDenied, pol.webhookMessage)
			h.logReject(tun, r, st, RejectWebhookFailed, started)
		}
		return
	}
	tp = mergePolicyHeaders(tp, pol)

	// Yönlendirme (redirect) kurallari: eslesirse istemciye is gonderilmeden
	// once yaniti burada dondururuz.
	if loc, status, ok := tp.matchRedirect(r.URL.Path); ok {
		http.Redirect(w, r, loc, status)
		h.logReject(tun, r, status, RejectRedirectRule, started)
		return
	}

	// Hiz siniri tunel cozumlendikten hemen sonra, istemciye herhangi bir
	// is gonderilmeden once uygulanir.
	if h.ReqLimiter != nil && !h.ReqLimiter.Allow(tun.TunnelID) {
		h.Log.Warn("tunel hiz siniri asildi", "tunel", tun.TunnelID, "hostname", tun.FQDN)
		w.Header().Set("Retry-After", strconv.Itoa(int(h.ReqLimiter.RetryAfter().Seconds())))
		writeError(w, r, http.StatusTooManyRequests, protocol.CodeRateLimited,
			"Bu tunel icin istek hizi siniri asildi.")
		h.logReject(tun, r, http.StatusTooManyRequests, RejectRateLimited, started)
		return
	}

	var runtimeState *bandwidth.TenantRuntimeState
	if h.BandwidthTracker != nil && tun.TenantID != "" {
		runtimeState, _ = h.BandwidthTracker.GetOrCreateState(r.Context(), tun.TenantID)
	}
	if runtimeState != nil && runtimeState.IsThrottled() {
		w.Header().Set("X-Zorven-Throttled", "true")
		w.Header().Set("X-Zorven-Plan-Status", "bandwidth_exceeded")
		w.Header().Set("X-RPShell-Throttled", "true")
		w.Header().Set("X-RPShell-Plan-Status", "bandwidth_exceeded")
	}

	// FAZ 5 / HA: bu hostname'i birden çok agent servis edebilir. Adayları
	// (birincil + replikalar) round-robin sıralayıp yerel Hub'da çevrimiçi
	// olanı seç; düşen agent otomatik atlanır (failover).
	candidates := candidateClients(tun)
	var ordered []string
	if e, ok := h.Router.lbFor(tun.TunnelID); ok {
		// F21: saglik durumu + strateji. Kayit yoksa asagidaki eski yol.
		ordered = orderBackends(candidates, e.cfg, h.rr.next(tun.FQDN), hubView{h: h, tunnelID: tun.TunnelID})
	} else {
		ordered = orderOnline(candidates, h.rr.next(tun.FQDN), func(cid string) bool {
			_, ok := h.Hub.Get(cid)
			return ok
		})
	}

	var sess *tunnel.Session
	for _, cid := range ordered {
		if s, ok := h.Hub.Get(cid); ok {
			sess = s
			// least_connections icin acik istek sayaci (her tunelde tutulur; ucuz).
			h.Router.lb.begin(cid)
			defer h.Router.lb.end(cid)
			break
		}
	}

	if sess == nil {
		writeError(w, r, http.StatusBadGateway, protocol.CodeClientOffline,
			"Tunelin istemcisi su anda bagli degil.")
		h.logReject(tun, r, http.StatusBadGateway, RejectClientOffline, started)
		return
	}

	if isWebSocketUpgrade(r) {
		h.serveWebSocket(w, r, sess, tun.TunnelID, tun.TenantID)
		return
	}

	h.proxy(w, r, sess, tun.TunnelID, tun.TenantID, runtimeState, tp)
}

// enforceAccess, tunelin erişim denetimi politikasını uygular.
// true = geç; false = reddedildi (yanıt zaten yazıldı). Politika kapalı/none ise
// her zaman true (geriye uyumlu — tünel herkese açık).
func (h *Handler) enforceAccess(w http.ResponseWriter, r *http.Request, tun store.HostRoute) bool {
	if !tun.AccessEnabled || tun.AccessMode == "" || tun.AccessMode == "none" {
		return true
	}
	switch tun.AccessMode {
	case "basic":
		return h.enforceBasic(w, r, tun)
	case "oauth":
		return h.enforceOAuth(w, r, tun)
	}
	return true
}

func (h *Handler) proxy(w http.ResponseWriter, r *http.Request, sess *tunnel.Session, tunnelID, tenantID string, runtimeState *bandwidth.TenantRuntimeState, tp *trafficPolicy) {
	ctx := r.Context()
	hasBody := r.Body != nil && r.ContentLength != 0

	// FAZ 2: opt-in tam yakalama (kapaliyken sifir maliyet). Istek govdesini
	// tavana kadar yakala; yanit writeResponse'da tee edilir.
	var capt *reqlog.Capture
	if h.Captures != nil && h.Captures.Enabled(tenantID) {
		capt = &reqlog.Capture{
			ID:         newCaptureID(),
			TunnelID:   tunnelID,
			TenantID:   tenantID,
			Hostname:   normalizeHost(r.Host),
			ClientIP:   clientIP(r),
			TS:         time.Now().UTC(),
			Method:     r.Method,
			Path:       r.URL.Path,
			Query:      r.URL.RawQuery,
			ReqHeaders: r.Header.Clone(),
		}
		if hasBody {
			r.Body = captureReadCloser(r.Body, &capt.ReqBody, &capt.ReqBodyTruncated, reqlog.CaptureMaxBody)
		}
	}

	// Yanit durumunu ve boyutunu yakalayabilmek icin ResponseWriter'i sar.
	rec := &recorder{ResponseWriter: w, status: http.StatusOK}
	var targetWriter http.ResponseWriter = rec
	if runtimeState != nil && runtimeState.GetLimiter() != nil {
		targetWriter = bandwidth.NewThrottledWriter(ctx, rec, runtimeState.GetLimiter())
	}
	w = targetWriter

	started := time.Now()
	defer func() {
		if capt != nil {
			capt.DurationMS = time.Since(started).Milliseconds()
			if capt.Status == 0 {
				capt.Status = rec.status
			}
			h.Captures.Put(capt)
		}
		h.recordWithID(tunnelID, tenantID, r, rec, started, captureID(capt))
	}()

	// FAZ 6: trafik politikasi istek basliklari (backend'e iletilen).
	reqHeaders := forwardHeaders(r)
	tp.applyToRequest(http.Header(reqHeaders))

	ex, err := sess.SendRequest(ctx, protocol.HTTPRequest{
		TunnelID:      tunnelID,
		Method:        r.Method,
		Path:          r.URL.Path,
		Query:         r.URL.RawQuery,
		Headers:       reqHeaders,
		HasBody:       hasBody,
		ContentLength: r.ContentLength, // >0 ise ajan chunked yerine Content-Length kullanir
		RemoteAddr:    clientIP(r),
	})
	if err != nil {
		h.Log.Warn("istek istemciye gonderilemedi", "hata", err)
		if errors.Is(err, tunnel.ErrTooManyRequests) {
			writeError(w, r, http.StatusServiceUnavailable, protocol.CodeRateLimited,
				"Istemci eszamanli istek sinirina ulasti, lutfen tekrar deneyin.")
			return
		}
		writeError(w, r, http.StatusBadGateway, protocol.CodeClientOffline,
			"Istek istemciye iletilemedi.")
		return
	}
	// Bekleyen istek tablosunda sizinti olmamasi icin her cikista temizle.
	defer sess.FinishExchange(ex.ReqID())

	if hasBody {
		if err := sess.StreamRequestBody(ctx, ex.ReqID(), r.Body); err != nil {
			if errors.Is(err, tunnel.ErrBodyTooLarge) {
				writeError(w, r, http.StatusRequestEntityTooLarge, protocol.CodeBodyTooLarge,
					"Istek govdesi 32 MB sinirini asiyor.")
				return
			}
			h.Log.Warn("istek govdesi aktarilamadi", "hata", err)
			writeError(w, r, http.StatusBadGateway, protocol.CodeClientOffline,
				"Istek govdesi iletilemedi.")
			return
		}
	}

	// Yanit basligini bekle.
	select {
	case <-ctx.Done():
		// Tarayici vazgecti; istemciye iptal bildir.
		sess.SendCancel(context.Background(), ex.ReqID(), "client_disconnected")
		return

	case <-time.After(UpstreamTimeout):
		writeError(w, r, http.StatusGatewayTimeout, protocol.CodeUpstreamTimeout,
			"Istemci zamaninda yanit vermedi.")
		return

	case head := <-ex.Head():
		if head.ErrCode != "" {
			status := http.StatusBadGateway
			if head.ErrCode == protocol.CodeUpstreamTimeout {
				status = http.StatusGatewayTimeout
			}
			writeError(w, r, status, head.ErrCode, head.ErrMsg)
			return
		}
		if capt != nil {
			capt.Status = head.Status
			capt.RespHeaders = head.Headers
		}
		h.writeResponse(w, head, ex, capt, tp)
	}
}

func (h *Handler) writeResponse(w http.ResponseWriter, head tunnel.ResponseHead, ex *tunnel.Exchange, capt *reqlog.Capture, tp *trafficPolicy) {
	dst := w.Header()
	for k, vals := range head.Headers {
		lk := strings.ToLower(k)
		if hopByHop[lk] {
			continue
		}
		for _, v := range vals {
			if lk == "set-cookie" {
				// O7: tunel backend'i platform ust alanina (or. Domain=zorven.app)
				// cerez yazamaz; Domain niteligi silinir, cerez host-only olur.
				v = sanitizeSetCookie(v, h.PlatformDomain)
			}
			dst.Add(k, v)
		}
	}
	// FAZ 6: trafik politikasi yanit basliklari (backend yanitina eklenir/silinir).
	tp.applyToResponse(dst)
	w.WriteHeader(head.Status)

	if !head.HasBody {
		return
	}

	// Govde akitilir; buyuk yanitlar tamamen bellege alinmaz. FAZ 2: yakalama
	// aciksa akan govde tavana kadar bir tampona da tee edilir.
	var out io.Writer = newFlushWriter(w)
	if capt != nil {
		out = io.MultiWriter(out, &captureWriter{w: io.Discard, dst: &capt.RespBody, truncated: &capt.RespBodyTruncated, max: reqlog.CaptureMaxBody})
	}
	if _, err := io.Copy(out, ex.Body()); err != nil {
		h.Log.Debug("yanit govdesi yazilirken kesildi", "hata", err)
	}
}

// --- yardimcilar -----------------------------------------------------------

// codeMisdirected, SNI ile Host uyusmazliginda (421) donen hata kodu.
const codeMisdirected = "misdirected_request"

// clientCertOK, istemcinin sundugu sertifikanin tunelin mTLS CA'sina gore
// gecerli (clientAuth kullanimli) olup olmadigi.
func (h *Handler) clientCertOK(r *http.Request, tunnelID string) bool {
	if r.TLS == nil || len(r.TLS.PeerCertificates) == 0 {
		return false
	}
	pool, ok := h.Router.tunnelCAPool(tunnelID)
	if !ok || pool == nil {
		return false
	}
	inter := x509.NewCertPool()
	for _, c := range r.TLS.PeerCertificates[1:] {
		inter.AddCert(c)
	}
	_, err := r.TLS.PeerCertificates[0].Verify(x509.VerifyOptions{
		Roots:         pool,
		Intermediates: inter,
		KeyUsages:     []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth},
	})
	return err == nil
}

// sanitizeSetCookie, Set-Cookie degerinden platform domainine (veya onun ust
// alanina) isaret eden Domain niteligini siler. Boylece bir tunel *.platform
// altindaki kardes hostlara (diger kiracilar, panel) tasan cerez yazamaz;
// cerez yalnizca kendi hostuna (host-only) yazilir. Kiracinin kendi custom
// domaini veya kendi tam hostu icin Domain korunur.
func sanitizeSetCookie(v, platformDomain string) string {
	pd := strings.ToLower(strings.TrimSuffix(strings.TrimSpace(platformDomain), "."))
	if pd == "" || !strings.Contains(strings.ToLower(v), "domain") {
		return v
	}
	parts := strings.Split(v, ";")
	out := parts[:1]
	changed := false
	for _, p := range parts[1:] {
		k, val, _ := strings.Cut(p, "=")
		if strings.EqualFold(strings.TrimSpace(k), "domain") {
			d := strings.ToLower(strings.TrimSuffix(strings.TrimPrefix(strings.TrimSpace(val), "."), "."))
			if d == "" || d == pd || strings.HasSuffix(pd, "."+d) {
				changed = true
				continue
			}
		}
		out = append(out, p)
	}
	if !changed {
		return v
	}
	return strings.Join(out, ";")
}

// forwardHeaders, hop-by-hop basliklari atar ve X-Forwarded-* ekler.
func forwardHeaders(r *http.Request) map[string][]string {
	// RFC 7230 §6.1: Connection basliginda listelenen basliklar da hop-by-hop'tur.
	connHop := make(map[string]bool)
	for _, c := range r.Header["Connection"] {
		for _, token := range strings.Split(c, ",") {
			if t := strings.ToLower(strings.TrimSpace(token)); t != "" {
				connHop[t] = true
			}
		}
	}

	out := make(map[string][]string, len(r.Header)+4)
	for k, v := range r.Header {
		lk := strings.ToLower(k)
		if hopByHop[lk] || connHop[lk] {
			continue
		}
		out[k] = v
	}
	// Zorven oturum cerezleri (_zva_session / _zvb_session) backend'e ASLA gitmez.
	if cv, ok := out["Cookie"]; ok {
		if kept := stripAuthCookies(cv); len(kept) > 0 {
			out["Cookie"] = kept
		} else {
			delete(out, "Cookie")
		}
	}

	proto := "http"
	if r.TLS != nil {
		proto = "https"
	}
	ip := clientIP(r)

	// Zincirdeki onceki proxy'lerin degerini koru, sonuna ekle.
	if prior, ok := out["X-Forwarded-For"]; ok {
		out["X-Forwarded-For"] = []string{strings.Join(prior, ", ") + ", " + ip}
	} else {
		out["X-Forwarded-For"] = []string{ip}
	}
	out["X-Forwarded-Proto"] = []string{proto}
	out["X-Forwarded-Host"] = []string{r.Host}
	return out
}

func clientIP(r *http.Request) string {
	if i := strings.LastIndex(r.RemoteAddr, ":"); i > 0 {
		return strings.Trim(r.RemoteAddr[:i], "[]")
	}
	return r.RemoteAddr
}

func isWebSocketUpgrade(r *http.Request) bool {
	return strings.EqualFold(r.Header.Get("Upgrade"), "websocket")
}

// flushWriter, her yazmadan sonra tamponu bosaltir; boylece akan yanitlar
// (SSE, uzun sorgular) istemciye anlik ulasir.
type flushWriter struct {
	w io.Writer
	f http.Flusher
}

func newFlushWriter(w http.ResponseWriter) io.Writer {
	if f, ok := w.(http.Flusher); ok {
		return &flushWriter{w: w, f: f}
	}
	return w
}

func (fw *flushWriter) Write(p []byte) (int, error) {
	n, err := fw.w.Write(p)
	if n > 0 {
		fw.f.Flush()
	}
	return n, err
}

// recorder, yanit durum kodunu ve yazilan bayt sayisini kaydeder.
// Akitmayi bozmamak icin http.Flusher'i da gecirir.
type recorder struct {
	http.ResponseWriter
	status  int
	written int64
}

func (rec *recorder) WriteHeader(code int) {
	rec.status = code
	rec.ResponseWriter.WriteHeader(code)
}

func (rec *recorder) Write(p []byte) (int, error) {
	n, err := rec.ResponseWriter.Write(p)
	rec.written += int64(n)
	return n, err
}

// Flush, sarmalanan yazicinin akitma yetenegini korur — SSE ve uzun
// yanitlar bu olmadan tamponlanip takilirdi.
func (rec *recorder) Flush() {
	if f, ok := rec.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

// captureID, capt nil degilse kimligini, degilse "" doner. Log kaydinin
// yakalama ile ayni kimligi tasimasi (panelde eslesme) icin kullanilir.
func captureID(capt *reqlog.Capture) string {
	if capt == nil {
		return ""
	}
	return capt.ID
}

// recordWithID, istegi halka tampona yazar ve dashboard'a olay yayinlar.
// id bos degilse log kaydi o kimligi kullanir (yakalama ile eslesir).
func (h *Handler) recordWithID(tunnelID, tenantID string, r *http.Request, rec *recorder, started time.Time, id string) {
	bytesIn := max(r.ContentLength, 0)
	bytesOut := rec.written

	if h.BandwidthRecorder != nil && tenantID != "" {
		_ = h.BandwidthRecorder.Record(context.Background(), bandwidth.UsageEvent{
			TenantID:  tenantID,
			Type:      bandwidth.UsageTypeProxyHTTP,
			BytesIn:   bytesIn,
			BytesOut:  bytesOut,
			Timestamp: time.Now(),
		})
	}

	if h.ReqLog == nil {
		return
	}
	h.emitEntry(reqlog.Entry{
		ID:         id,
		TunnelID:   tunnelID,
		TenantID:   tenantID,
		Hostname:   normalizeHost(r.Host),
		ClientIP:   clientIP(r),
		Method:     r.Method,
		Path:       r.URL.Path,
		Status:     rec.status,
		DurationMS: time.Since(started).Milliseconds(),
		BytesIn:    bytesIn,
		BytesOut:   bytesOut,
	})
}

// emitEntry, kaydi halka tampona, kalici log kuyruguna ve canli olay akisina yazar.
func (h *Handler) emitEntry(e reqlog.Entry) {
	if h.ReqLog == nil {
		return
	}
	entry := h.ReqLog.Add(e)
	// Kalici log (Postgres): batch persister'a devret. nil olabilir.
	if h.LogPersister != nil {
		h.LogPersister.Enqueue(entry)
	}
	if h.Events != nil {
		h.Events.PublishTenant(entry.TenantID, events.TypeRequestCompleted, entry)
	}
}
