package obs

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"
	"time"
)

func scrape(t *testing.T, h http.Handler, token string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest("GET", "/metrics", nil)
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	return rr
}

func TestMetrikBicimi(t *testing.T) {
	ObserveRequest(200, 30*time.Millisecond)
	ObserveRequest(502, 3*time.Second)
	CountReject("waf:sqli")
	CountReject("bot:fail")
	CountReject("rate_limited")
	ClusterForward(nil)
	ClusterForward(errors.New("x"))
	ReloadDone(40*time.Millisecond, nil)
	ReloadDone(time.Millisecond, errors.New("db"))
	SetSnapshotTime(time.Now().Add(-90 * time.Second))
	SetCertNotAfter(time.Now().Add(48 * time.Hour))
	RegisterGauge("zorven_test_gauge", "deneme", func() float64 { return 7 })

	var buf bytes.Buffer
	if err := Write(&buf); err != nil {
		t.Fatal(err)
	}
	out := buf.String()
	for _, want := range []string{
		`zorven_http_requests_total{class="2xx"}`,
		`zorven_http_requests_total{class="5xx"}`,
		`zorven_http_request_duration_seconds_bucket{le="+Inf"}`,
		`zorven_http_request_duration_seconds_count`,
		`zorven_rejects_total{kind="waf"}`,
		`zorven_rejects_total{kind="bot"}`,
		`zorven_rejects_total{kind="rate_limit"}`,
		`zorven_cluster_forward_errors_total`,
		`zorven_router_reload_failures_total`,
		`zorven_router_reload_last_duration_seconds`,
		`zorven_route_snapshot_age_seconds`,
		`zorven_tls_cert_days_remaining`,
		`zorven_test_gauge 7`,
		`go_goroutines`,
		`go_memstats_alloc_bytes`,
		`go_gc_cycles_total`,
	} {
		if !strings.Contains(out, want) {
			t.Errorf("metrik eksik: %s", want)
		}
	}
	// Her satir ya yorum ya da "ad{etiket} deger" olmali.
	re := regexp.MustCompile(`^([a-zA-Z_:][a-zA-Z0-9_:]*)(\{[^}]*\})? (-?[0-9.e+\-]+|\+Inf)$`)
	for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
		if strings.HasPrefix(line, "# HELP ") || strings.HasPrefix(line, "# TYPE ") {
			continue
		}
		if !re.MatchString(line) {
			t.Errorf("gecersiz metrik satiri: %q", line)
		}
	}
	// Histogram kovalari kumulatif ve +Inf = count olmali.
	if strings.Contains(out, "tenant") || strings.Contains(out, "host=") {
		t.Error("kiraci/host etiketi bulunmamali")
	}
}

func TestRejectSiniflari(t *testing.T) {
	cases := map[string]int{
		"waf:sqli": RejectWAF, "waf_blocked": RejectWAF, "bot:challenge": RejectBot,
		"rate_limited": RejectRate, "rate:block": RejectRate, "geo:TR": RejectGeo,
		"ip_forbidden": RejectIP, "policy_deny": RejectPolicy, "auth_required": RejectAuth,
		"mtls_required": RejectAuth, "interstitial": RejectOther, "": RejectOther,
	}
	for in, want := range cases {
		if got := classifyReject(in); got != want {
			t.Errorf("%q: %d, beklenen %d", in, got, want)
		}
	}
}

func TestTokenZorunluluguVeAcikMod(t *testing.T) {
	h := Handler(Config{Token: "gizli-jeton"})
	if rr := scrape(t, h, ""); rr.Code != http.StatusUnauthorized {
		t.Fatalf("tokensiz %d", rr.Code)
	}
	if rr := scrape(t, h, "yanlis"); rr.Code != http.StatusUnauthorized {
		t.Fatalf("yanlis token %d", rr.Code)
	}
	rr := scrape(t, h, "gizli-jeton")
	if rr.Code != http.StatusOK || !strings.HasPrefix(rr.Header().Get("Content-Type"), "text/plain") {
		t.Fatalf("dogru token: %d %q", rr.Code, rr.Header().Get("Content-Type"))
	}
	if rr := scrape(t, Handler(Config{}), ""); rr.Code != http.StatusOK {
		t.Fatalf("tokensiz yapilandirma %d", rr.Code)
	}
}

func TestPprofYalnizBayrakla(t *testing.T) {
	req := httptest.NewRequest("GET", "/debug/pprof/", nil)
	rr := httptest.NewRecorder()
	Handler(Config{}).ServeHTTP(rr, req)
	if rr.Code != http.StatusNotFound {
		t.Fatalf("pprof kapaliyken %d", rr.Code)
	}
	rr = httptest.NewRecorder()
	Handler(Config{Pprof: true, Token: "t"}).ServeHTTP(rr, req)
	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("pprof tokensiz %d", rr.Code)
	}
	req.Header.Set("Authorization", "Bearer t")
	rr = httptest.NewRecorder()
	Handler(Config{Pprof: true, Token: "t"}).ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("pprof tokenli %d", rr.Code)
	}
}

func TestVarsayilanKapali(t *testing.T) {
	cfg := ConfigFromEnv(func(string) string { return "" })
	if cfg.Addr != "" || cfg.Pprof || cfg.Token != "" {
		t.Fatalf("varsayilan kapali olmali: %+v", cfg)
	}
	// Addr bos iken Serve dinleyici acmaz (donmeli, panik yok).
	Serve(context.Background(), cfg, nil)
	cfg = ConfigFromEnv(func(k string) string {
		return map[string]string{"ZORVEN_METRICS_ADDR": " 127.0.0.1:9101 ", "ZORVEN_PPROF": "1", "ZORVEN_METRICS_TOKEN": "x"}[k]
	})
	if cfg.Addr != "127.0.0.1:9101" || !cfg.Pprof || cfg.Token != "x" {
		t.Fatalf("env okunamadi: %+v", cfg)
	}
}

func TestInstrumentWebSocketVeAkisiBozmaz(t *testing.T) {
	before := reqByClass[class2xx].Load()
	h := Instrument(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if _, ok := w.(http.Flusher); !ok {
			t.Error("Flusher kayboldu")
		}
		if _, ok := w.(http.Hijacker); !ok {
			t.Error("Hijacker kayboldu")
		}
		w.Write([]byte("merhaba"))
	}))
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, httptest.NewRequest("POST", "/", strings.NewReader("govde")))
	if rr.Body.String() != "merhaba" {
		t.Fatal("govde bozuldu")
	}
	if reqByClass[class2xx].Load() != before+1 {
		t.Fatal("2xx sayilmadi")
	}
}

func TestReadyz(t *testing.T) {
	get := func() (*httptest.ResponseRecorder, string) {
		rr := httptest.NewRecorder()
		ReadyHandler().ServeHTTP(rr, httptest.NewRequest("GET", "/readyz", nil))
		return rr, rr.Body.String()
	}
	// Yonlendirme verisi hic yok: hazir degil.
	reloadOKUnix.Store(0)
	SetServingFromSnapshot(false)
	certNotAfter.Store(0)
	SetReadiness(Readiness{DBPing: func(context.Context) error { return errors.New("postgres://user:parola@host/db baglanti hatasi") }})
	rr, body := get()
	if rr.Code != http.StatusServiceUnavailable {
		t.Fatalf("hic yuklenmemis: %d", rr.Code)
	}
	if strings.Contains(body, "parola") || strings.Contains(body, "postgres") {
		t.Fatalf("govdede sir/hata metni: %s", body)
	}
	// Taze Reload: DB kapali olsa da hazir.
	ReloadDone(time.Millisecond, nil)
	if rr, body = get(); rr.Code != http.StatusOK || !strings.Contains(body, `"db":{"ok":false}`) {
		t.Fatalf("taze veri, db kapali: %d %s", rr.Code, body)
	}
	// Eski veri: hazir degil.
	reloadOKUnix.Store(time.Now().Add(-2 * time.Hour).Unix())
	if rr, _ = get(); rr.Code != http.StatusServiceUnavailable {
		t.Fatalf("eski veri: %d", rr.Code)
	}
	// Goruntuden hizmet: goruntu yasi sayilir.
	SetServingFromSnapshot(true)
	SetSnapshotTime(time.Now().Add(-time.Minute))
	if rr, body = get(); rr.Code != http.StatusOK || !strings.Contains(body, `"source":"snapshot"`) {
		t.Fatalf("goruntu: %d %s", rr.Code, body)
	}
	SetServingFromSnapshot(false)
	// Suresi dolmus sertifika.
	ReloadDone(time.Millisecond, nil)
	SetCertNotAfter(time.Now().Add(-time.Hour))
	if rr, _ = get(); rr.Code != http.StatusServiceUnavailable {
		t.Fatalf("sertifika dolmus: %d", rr.Code)
	}
	certNotAfter.Store(0)
	// Yontem kisiti.
	rr = httptest.NewRecorder()
	ReadyHandler().ServeHTTP(rr, httptest.NewRequest("POST", "/readyz", nil))
	if rr.Code != http.StatusMethodNotAllowed {
		t.Fatalf("POST: %d", rr.Code)
	}
}
