package ingress

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/tkodcumpeg4/zorven/server/bandwidth"
	"github.com/tkodcumpeg4/zorven/server/events"
	"github.com/tkodcumpeg4/zorven/server/ipfilter"
	"github.com/tkodcumpeg4/zorven/server/ratelimit"
	"github.com/tkodcumpeg4/zorven/server/reqlog"
	"github.com/tkodcumpeg4/zorven/server/tunnel"
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
}

func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	// WebSocket yukseltmeleri (Upgrade: websocket) artik desteklenir: tunel
	// kontrolleri (tunel aktif mi, IP izin listesi, hiz siniri, istemci
	// cevrimici mi) yapildiktan sonra serveWebSocket'e dallanilir. Bkz. ServeHTTP
	// sonundaki isWebSocketUpgrade kontrolu.

	tun, ok := h.Router.Lookup(r.Host)
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

	// Ingress IP Izin Listesi (IP Filtering) Kontrolu
	if h.IPFilter != nil && tun.TenantID != "" {
		ip := clientIP(r)
		allowed, err := h.IPFilter.CheckAllowed(r.Context(), tun.TenantID, tun.TunnelID, ip)
		if err == nil && !allowed {
			h.Log.Warn("IP izin listesi tarafindan engellendi",
				"ip", ip, "tunel", tun.TunnelID, "tenant", tun.TenantID, "hostname", tun.FQDN)
			writeError(w, r, http.StatusForbidden, protocol.CodeIPForbidden,
				"Erişim engellendi: IP adresiniz ("+ip+") bu tünelin izin listesinde bulunmuyor.")
			return
		}
	}

	// Hiz siniri tunel cozumlendikten hemen sonra, istemciye herhangi bir
	// is gonderilmeden once uygulanir.
	if h.ReqLimiter != nil && !h.ReqLimiter.Allow(tun.TunnelID) {
		h.Log.Warn("tunel hiz siniri asildi", "tunel", tun.TunnelID, "hostname", tun.FQDN)
		w.Header().Set("Retry-After", strconv.Itoa(int(h.ReqLimiter.RetryAfter().Seconds())))
		writeError(w, r, http.StatusTooManyRequests, protocol.CodeRateLimited,
			"Bu tunel icin istek hizi siniri asildi.")
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

	sess, online := h.Hub.Get(tun.ClientID)
	if !online {
		writeError(w, r, http.StatusBadGateway, protocol.CodeClientOffline,
			"Tunelin istemcisi su anda bagli degil.")
		return
	}

	if isWebSocketUpgrade(r) {
		h.serveWebSocket(w, r, sess, tun.TunnelID)
		return
	}

	h.proxy(w, r, sess, tun.TunnelID, tun.TenantID, runtimeState)
}

func (h *Handler) proxy(w http.ResponseWriter, r *http.Request, sess *tunnel.Session, tunnelID, tenantID string, runtimeState *bandwidth.TenantRuntimeState) {
	ctx := r.Context()
	hasBody := r.Body != nil && r.ContentLength != 0

	// Yanit durumunu ve boyutunu yakalayabilmek icin ResponseWriter'i sar.
	rec := &recorder{ResponseWriter: w, status: http.StatusOK}
	var targetWriter http.ResponseWriter = rec
	if runtimeState != nil && runtimeState.GetLimiter() != nil {
		targetWriter = bandwidth.NewThrottledWriter(ctx, rec, runtimeState.GetLimiter())
	}
	w = targetWriter

	started := time.Now()
	defer func() {
		h.record(tunnelID, tenantID, r, rec, started)
	}()

	ex, err := sess.SendRequest(ctx, protocol.HTTPRequest{
		TunnelID:   tunnelID,
		Method:     r.Method,
		Path:       r.URL.Path,
		Query:      r.URL.RawQuery,
		Headers:    forwardHeaders(r),
		HasBody:    hasBody,
		RemoteAddr: clientIP(r),
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
		h.writeResponse(w, head, ex)
	}
}

func (h *Handler) writeResponse(w http.ResponseWriter, head tunnel.ResponseHead, ex *tunnel.Exchange) {
	dst := w.Header()
	for k, vals := range head.Headers {
		if hopByHop[strings.ToLower(k)] {
			continue
		}
		for _, v := range vals {
			dst.Add(k, v)
		}
	}
	w.WriteHeader(head.Status)

	if !head.HasBody {
		return
	}

	// Govde akitilir; buyuk yanitlar tamamen bellege alinmaz.
	if _, err := io.Copy(newFlushWriter(w), ex.Body()); err != nil {
		h.Log.Debug("yanit govdesi yazilirken kesildi", "hata", err)
	}
}

// --- yardimcilar -----------------------------------------------------------

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

// record, istegi halka tampona yazar ve dashboard'a olay yayinlar.
func (h *Handler) record(tunnelID, tenantID string, r *http.Request, rec *recorder, started time.Time) {
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
	entry := h.ReqLog.Add(reqlog.Entry{
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
	// Kalici log (Postgres): batch persister'a devret. nil olabilir.
	if h.LogPersister != nil {
		h.LogPersister.Enqueue(entry)
	}
	if h.Events != nil {
		h.Events.Publish(events.TypeRequestCompleted, entry)
	}
}
