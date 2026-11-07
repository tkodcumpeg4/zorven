package ingress

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"html"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"

	"github.com/tkodcumpeg4/zorven/server/store"
)

// ReportPath, tunel host'unda ziyaretci kotuye kullanim bildirimi ucu (uyari
// sayfasindaki "Bu siteyi bildir" formu). /.zorven/* altindadir: tunele iletilmez.
const ReportPath = "/.zorven/report"

// reporterCookie, bildireni tanimak icin kalici rastgele kimlik (spam onlemi).
const reporterCookie = "__zv_rid"

// AbuseReporter, ziyaretci bildirimlerini kaydeden depo (store.Store karsilar).
type AbuseReporter interface {
	CreateAbuseReportFull(ctx context.Context, r store.AbuseReport) (store.AbuseReport, error)
	AbuseReportCounts(ctx context.Context, since time.Time, ip, reporterID, fqdn string) (byIP, byID, sameHost int, err error)
}

// Spam sinirlari: saatte IP ve bildiren kimligi basina en fazla 3 bildirim; ayni
// IP ayni host'u 24 saatte bir kez bildirebilir (fazlasi sessizce yok sayilir).
const (
	reportPerHour      = 3
	reportSameHostSpan = 24 * time.Hour
)

var reportCategories = map[string]string{
	"phishing": "Kimlik avı (phishing)",
	"malware":  "Kötü amaçlı yazılım",
	"scam":     "Dolandırıcılık",
	"illegal":  "Yasa dışı içerik",
	"other":    "Diğer",
}

var reporterIDRe = regexp.MustCompile(`^[0-9a-f]{32}$`)

// serveReport, uyari sayfasindaki bildirim formunu kaydeder. Bildirenin IP'si,
// kalici bildiren kimligi (cerez), tarayici bilgisi, dili ve TLS parmak izi
// raporla birlikte saklanir; spam sinirlari bu alanlarla uygulanir.
func (h *Handler) serveReport(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", "POST")
		writeError(w, r, http.StatusMethodNotAllowed, "method_not_allowed", "POST gerekli.")
		return
	}
	if h.AbuseReports == nil {
		reportPage(w, http.StatusServiceUnavailable, "Bildirim şu an alınamıyor", "Lütfen daha sonra tekrar deneyin ya da abuse@"+h.PlatformDomain+" adresine yazın.")
		return
	}
	body, _ := io.ReadAll(io.LimitReader(r.Body, 8<<10))
	form, _ := url.ParseQuery(string(body))
	// Bal kupu alani: insanlar gormez, botlar doldurur; doldurulmussa kaydetmeden tesekkur et.
	if strings.TrimSpace(form.Get("website")) != "" {
		reportPage(w, http.StatusOK, "Bildiriminiz alındı", "Teşekkürler. Bildiriminiz incelenecek.")
		return
	}
	cat := form.Get("category")
	label, ok := reportCategories[cat]
	if !ok {
		cat, label = "other", reportCategories["other"]
	}
	detail := strings.TrimSpace(form.Get("detail"))
	if len([]rune(detail)) > 1000 {
		detail = string([]rune(detail)[:1000])
	}

	rid := ""
	if c, err := r.Cookie(reporterCookie); err == nil && reporterIDRe.MatchString(c.Value) {
		rid = c.Value
	} else {
		b := make([]byte, 16)
		_, _ = rand.Read(b)
		rid = hex.EncodeToString(b)
	}
	http.SetCookie(w, &http.Cookie{Name: reporterCookie, Value: rid, Path: "/.zorven/",
		MaxAge: 365 * 24 * 3600, HttpOnly: true, Secure: r.TLS != nil, SameSite: http.SameSiteLaxMode})

	ip := clientIP(r)
	fqdn := normalizeHost(r.Host)
	ctx := r.Context()
	now := time.Now()
	byIP, byID, _, err := h.AbuseReports.AbuseReportCounts(ctx, now.Add(-time.Hour), ip, rid, fqdn)
	if err == nil && (byIP >= reportPerHour || byID >= reportPerHour) {
		h.Log.Warn("kotuye kullanim bildirimi hiz siniri", "ip", ip, "rid", rid, "host", fqdn)
		reportPage(w, http.StatusTooManyRequests, "Çok fazla bildirim", "Kısa sürede çok fazla bildirim gönderdiniz. Lütfen daha sonra tekrar deneyin.")
		return
	}
	if _, _, same, err := h.AbuseReports.AbuseReportCounts(ctx, now.Add(-reportSameHostSpan), ip, rid, fqdn); err == nil && same > 0 {
		reportPage(w, http.StatusOK, "Bildiriminiz zaten alındı", "Bu adresi daha önce bildirdiniz; inceleniyor.")
		return
	}

	tlsfp := ""
	if h.TLSFP != nil {
		if fp := h.TLSFP.Get(r.RemoteAddr); fp != nil {
			tlsfp = fp.Summary
		}
	}
	reason := label
	if detail != "" {
		reason += ": " + detail
	}
	rep, err := h.AbuseReports.CreateAbuseReportFull(ctx, store.AbuseReport{
		FQDN: fqdn, Reason: reason, Category: cat, ReporterIP: ip, ReporterID: rid,
		ReporterUA: truncate(r.UserAgent(), 400), ReporterLang: truncate(r.Header.Get("Accept-Language"), 100),
		ReporterTLS: tlsfp,
	})
	if err != nil {
		h.Log.Error("kotuye kullanim bildirimi kaydedilemedi", "host", fqdn, "hata", err)
		reportPage(w, http.StatusInternalServerError, "Bildirim kaydedilemedi", "Lütfen daha sonra tekrar deneyin ya da abuse@"+h.PlatformDomain+" adresine yazın.")
		return
	}
	h.Log.Warn("ziyaretci kotuye kullanim bildirimi", "id", rep.ID, "host", fqdn, "kategori", cat, "ip", ip, "rid", rid)
	reportPage(w, http.StatusOK, "Bildiriminiz alındı", "Teşekkürler. Bildiriminiz Zorven ekibi tarafından incelenecek.")
}

func truncate(s string, n int) string {
	if len(s) > n {
		return s[:n]
	}
	return s
}

func reportPage(w http.ResponseWriter, code int, title, msg string) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Referrer-Policy", "no-referrer")
	w.WriteHeader(code)
	_, _ = io.WriteString(w, `<!doctype html><html lang="tr"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1"><meta name="robots" content="noindex"><title>`+html.EscapeString(title)+`</title>
<style>body{margin:0;min-height:100vh;display:grid;place-items:center;background:#0A0A0A;color:#E5E5E5;font:15px/1.5 system-ui,-apple-system,"Segoe UI",Roboto,sans-serif}
.card{max-width:440px;margin:24px;padding:28px;border:1px solid #262626;border-radius:16px;background:#141414}h1{font-size:19px;margin:0 0 8px}p{color:#A3A3A3;margin:0 0 18px}a{color:#4ADE80}</style></head>
<body><div class="card"><h1>`+html.EscapeString(title)+`</h1><p>`+html.EscapeString(msg)+`</p><a href="/">Geri dön</a></div></body></html>`)
}
