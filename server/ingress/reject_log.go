package ingress

import (
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/tkodcumpeg4/zorven/server/reqlog"
	"github.com/tkodcumpeg4/zorven/server/store"
)

// Reddedilen istek nedenleri (reqlog.Entry.RejectReason). Bos deger = istek
// tunele proxy'lendi (normal kayit).
const (
	RejectIPForbidden    = "ip_forbidden"
	RejectAuthRequired   = "auth_required"
	RejectAccessDenied   = "access_denied"
	RejectInterstitial   = "interstitial"
	RejectPolicyDeny     = "policy_deny"
	RejectPolicyRedirect = "policy_redirect"
	RejectRedirectRule   = "redirect_rule"
	RejectRateLimited    = "rate_limited"
	RejectMTLSRequired   = "mtls_required"
	RejectWAFBlocked     = "waf_blocked"
	RejectWebhookFailed  = "webhook_failed"
	RejectClientOffline  = "client_offline"
	RejectMisdirected    = "misdirected"
)

// Reddedilen istek loglamasinin tunel basina siniri: saniyede 50 kayit
// (patlama 50). Asan istekler loglanmaz, sayilir ve dakikada bir toplu uyari
// olarak yazilir; boylece bir tarama request_logs'u dolduramaz.
const (
	rejectRatePerSec = 50.0
	rejectBurst      = 50.0
	rejectWarnEvery  = time.Minute
)

type rejectBucket struct {
	tokens   float64
	last     time.Time
	dropped  int64
	lastWarn time.Time
}

// rejectLimiter, tunel basina token bucket. Zero-value kullanima hazirdir.
type rejectLimiter struct {
	mu      sync.Mutex
	buckets map[string]*rejectBucket
	now     func() time.Time // testlerde sabitlenir
	pruned  time.Time
}

// allow, tunel icin bir reddedilen-istek kaydina izin var mi soyler. Kayit
// atilirsa sayilir; son uyaridan bu yana bir dakika gectiyse atilan sayisi
// warn olarak doner (cagiran uyariyi yazar).
func (l *rejectLimiter) allow(tunnelID string) (ok bool, warn int64) {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := time.Now()
	if l.now != nil {
		now = l.now()
	}
	if l.buckets == nil {
		l.buckets = make(map[string]*rejectBucket)
	}
	// Bosta kalan kovalari ara sira temizle (bellek siniri).
	if now.Sub(l.pruned) > 5*time.Minute {
		l.pruned = now
		for id, b := range l.buckets {
			if b.dropped == 0 && now.Sub(b.last) > 5*time.Minute {
				delete(l.buckets, id)
			}
		}
	}
	b := l.buckets[tunnelID]
	if b == nil {
		b = &rejectBucket{tokens: rejectBurst, last: now, lastWarn: now}
		l.buckets[tunnelID] = b
	}
	b.tokens = min(rejectBurst, b.tokens+now.Sub(b.last).Seconds()*rejectRatePerSec)
	b.last = now
	if b.tokens >= 1 {
		b.tokens--
		ok = true
	} else {
		b.dropped++
	}
	if b.dropped > 0 && now.Sub(b.lastWarn) >= rejectWarnEvery {
		warn = b.dropped
		b.dropped = 0
		b.lastWarn = now
	}
	return ok, warn
}

// accessRejectReason, erisim denetimi (Basic/OAuth) yanitinin durum kodundan
// neden etiketini turetir.
func accessRejectReason(status int) string {
	switch {
	case status == http.StatusUnauthorized,
		status == http.StatusFound, status == http.StatusSeeOther,
		status == http.StatusTemporaryRedirect, status == http.StatusMovedPermanently,
		status == http.StatusPermanentRedirect:
		return RejectAuthRequired
	case status == http.StatusTooManyRequests:
		return RejectRateLimited
	default:
		return RejectAccessDenied
	}
}

// logReject, tunele cozumlenmis ama proxy'lenmeden reddedilen (ya da ingress'in
// kendi urettigi yanitla sonlandirdigi) istegi gercek durum kodu, sure ve
// reject_reason ile istek loguna yazar. Tunel/kiraci bilinmiyorsa (bilinmeyen
// host 404) cagrilmaz. Dahili yollar (/_zva/, /_zvb/, /_zvd/) erisim olaylarina
// zaten yazildigi icin atlanir. Tunel basina hiz sinirlidir.
func (h *Handler) logReject(tun store.HostRoute, r *http.Request, status int, reason string, started time.Time) {
	if tun.TunnelID == "" || tun.TenantID == "" {
		return
	}
	if p := r.URL.Path; strings.HasPrefix(p, "/_zva/") || strings.HasPrefix(p, "/_zvb/") || strings.HasPrefix(p, "/_zvd/") {
		return
	}
	ok, warn := h.rejectLim.allow(tun.TunnelID)
	if warn > 0 && h.Log != nil {
		h.Log.Warn("reddedilen istek loglari hiz siniri nedeniyle atildi",
			"tunel", tun.TunnelID, "kiraci", tun.TenantID, "atilan", warn, "pencere", rejectWarnEvery.String())
	}
	if !ok {
		return
	}
	h.emitEntry(reqlog.Entry{
		TunnelID:     tun.TunnelID,
		TenantID:     tun.TenantID,
		Hostname:     normalizeHost(r.Host),
		ClientIP:     clientIP(r),
		Method:       r.Method,
		Path:         r.URL.Path,
		Status:       status,
		DurationMS:   time.Since(started).Milliseconds(),
		BytesIn:      max(r.ContentLength, 0),
		RejectReason: reason,
	})
}
