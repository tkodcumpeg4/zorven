package ingress

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/tkodcumpeg4/zorven/server/store"
)

type fakeAbuse struct {
	mu   sync.Mutex
	reps []store.AbuseReport
}

func (f *fakeAbuse) CreateAbuseReportFull(_ context.Context, r store.AbuseReport) (store.AbuseReport, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	r.ID, r.CreatedAt = "abr_x", time.Now()
	f.reps = append(f.reps, r)
	return r, nil
}

func (f *fakeAbuse) AbuseReportCounts(_ context.Context, since time.Time, ip, rid, fqdn string) (int, int, int, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var a, b, c int
	for _, r := range f.reps {
		if r.CreatedAt.Before(since) {
			continue
		}
		if r.ReporterIP == ip {
			a++
			if r.FQDN == fqdn {
				c++
			}
		}
		if rid != "" && r.ReporterID == rid {
			b++
		}
	}
	return a, b, c, nil
}

func postReport(h *Handler, host, ip string, form url.Values, cookie *http.Cookie) *httptest.ResponseRecorder {
	r := httptest.NewRequest(http.MethodPost, "https://"+host+ReportPath, strings.NewReader(form.Encode()))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	r.Header.Set("User-Agent", "TestBrowser/1.0")
	r.Header.Set("Accept-Language", "tr-TR")
	r.RemoteAddr = ip + ":5555"
	if cookie != nil {
		r.AddCookie(cookie)
	}
	w := httptest.NewRecorder()
	h.serveReport(w, r)
	return w
}

func TestServeReportRecordsReporterIdentity(t *testing.T) {
	fa := &fakeAbuse{}
	h := &Handler{AbuseReports: fa, PlatformDomain: "zorven.app", Log: slog.New(slog.NewTextHandler(io.Discard, nil))}
	w := postReport(h, "evil--x.zorven.app", "203.0.113.7", url.Values{"category": {"phishing"}, "detail": {"sahte banka girisi"}}, nil)
	if w.Code != 200 || len(fa.reps) != 1 {
		t.Fatalf("kod %d, rapor %d", w.Code, len(fa.reps))
	}
	r := fa.reps[0]
	if r.FQDN != "evil--x.zorven.app" || r.Category != "phishing" || r.ReporterIP != "203.0.113.7" ||
		r.ReporterUA != "TestBrowser/1.0" || r.ReporterLang != "tr-TR" || len(r.ReporterID) != 32 ||
		!strings.Contains(r.Reason, "sahte banka girisi") {
		t.Fatalf("rapor alanlari eksik: %+v", r)
	}
	var rid *http.Cookie
	for _, c := range w.Result().Cookies() {
		if c.Name == reporterCookie {
			rid = c
		}
	}
	if rid == nil || rid.Value != r.ReporterID || !rid.HttpOnly {
		t.Fatalf("bildiren cerezi yok/uyumsuz: %+v", rid)
	}
	// Ayni IP ayni host'u yeniden bildirirse yeni kayit acilmaz.
	postReport(h, "evil--x.zorven.app", "203.0.113.7", url.Values{"category": {"scam"}}, rid)
	if len(fa.reps) != 1 {
		t.Fatalf("ayni host tekrar kaydedildi: %d", len(fa.reps))
	}
}

func TestServeReportSpamGuards(t *testing.T) {
	fa := &fakeAbuse{}
	h := &Handler{AbuseReports: fa, PlatformDomain: "zorven.app", Log: slog.New(slog.NewTextHandler(io.Discard, nil))}
	// Bal kupu dolu: kayit yok ama 200.
	if w := postReport(h, "a.zorven.app", "198.51.100.1", url.Values{"website": {"x"}}, nil); w.Code != 200 || len(fa.reps) != 0 {
		t.Fatalf("bal kupu: kod %d, rapor %d", w.Code, len(fa.reps))
	}
	// Ayni IP'den saatte 3'ten fazla farkli host: 4.su 429.
	for i, host := range []string{"a.zorven.app", "b.zorven.app", "c.zorven.app"} {
		if w := postReport(h, host, "198.51.100.1", url.Values{"category": {"other"}}, nil); w.Code != 200 {
			t.Fatalf("%d. bildirim kod %d", i+1, w.Code)
		}
	}
	if w := postReport(h, "d.zorven.app", "198.51.100.1", url.Values{"category": {"other"}}, nil); w.Code != http.StatusTooManyRequests {
		t.Fatalf("hiz siniri beklendi, kod %d", w.Code)
	}
	// Bilinmeyen kategori "other"a duser; GET reddedilir.
	postReport(h, "e.zorven.app", "198.51.100.2", url.Values{"category": {"<script>"}}, nil)
	if fa.reps[len(fa.reps)-1].Category != "other" {
		t.Fatalf("kategori dogrulanmadi: %q", fa.reps[len(fa.reps)-1].Category)
	}
	r := httptest.NewRequest(http.MethodGet, "https://a.zorven.app"+ReportPath, nil)
	w := httptest.NewRecorder()
	h.serveReport(w, r)
	if w.Code != http.StatusMethodNotAllowed {
		t.Fatalf("GET kod %d", w.Code)
	}
}

func TestInterstitialHasReportForm(t *testing.T) {
	if !strings.Contains(interstitialHTML, `action="/.zorven/report"`) || strings.Contains(interstitialHTML, "mailto:") {
		t.Fatal("uyari sayfasi sayfa ici bildirim formu icermeli, mailto olmamali")
	}
}
