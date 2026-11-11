package obs

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func metricsText(t *testing.T) string {
	t.Helper()
	var b bytes.Buffer
	if err := Write(&b); err != nil {
		t.Fatal(err)
	}
	return b.String()
}

func newAPITestHandler(log *slog.Logger) (http.Handler, *http.ServeMux) {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v1/tunnels/{id}", func(w http.ResponseWriter, r *http.Request) {
		if r.PathValue("id") == "boom" {
			panic("deneme panigi")
		}
		if r.PathValue("id") == "abort" {
			panic(http.ErrAbortHandler)
		}
		if r.PathValue("id") == "limit" {
			w.WriteHeader(http.StatusTooManyRequests)
			return
		}
		w.WriteHeader(http.StatusOK)
	})
	routeOf := func(r *http.Request) string {
		_, p := mux.Handler(r)
		return p
	}
	return InstrumentAPI(mux, routeOf, log), mux
}

// O17: rota etiketi kalip; 2000 farkli yol ve host sonrasi metrik satir sayisi sabit.
func TestAPIMetricsRouteTemplateCardinality(t *testing.T) {
	h, _ := newAPITestHandler(slog.New(slog.NewTextHandler(io.Discard, nil)))
	do := func(path, host string) int {
		r := httptest.NewRequest(http.MethodGet, path, nil)
		r.Host = host
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		return w.Code
	}
	do("/api/v1/tunnels/x0", "a.test")
	do("/yok/0", "a.test")
	before := strings.Count(metricsText(t), "\n")
	for i := 0; i < 2000; i++ {
		do(fmt.Sprintf("/api/v1/tunnels/tun_%d", i), fmt.Sprintf("h%d.test", i))
		do(fmt.Sprintf("/yok/%d", i), fmt.Sprintf("h%d.test", i))
	}
	txt := metricsText(t)
	if after := strings.Count(txt, "\n"); after != before {
		t.Fatalf("kardinalite: satir sayisi %d -> %d", before, after)
	}
	if strings.Contains(txt, "tun_17") || strings.Contains(txt, "/yok/") {
		t.Fatal("ham yol metrikte")
	}
	if got := APIRequests("GET /api/v1/tunnels/{id}", "GET", "2xx"); got < 2001 {
		t.Fatalf("2xx sayaci %d", got)
	}
	if APIRequests(apiRouteUnmatched, "GET", "4xx") < 2001 {
		t.Fatal("eslesmeyen istekler unmatched kovasinda sayilmadi")
	}
	if do("/api/v1/tunnels/limit", "a.test") != 429 || APIRequests("GET /api/v1/tunnels/{id}", "GET", "429") == 0 {
		t.Fatal("429 ayri sinifta sayilmadi")
	}
	if !strings.Contains(txt, `zorven_api_request_duration_seconds_bucket{route="GET /api/v1/tunnels/{id}",le="+Inf"}`) {
		t.Fatal("rota histogrami yok")
	}
}

// A5 #7: gecerli X-Request-Id korunur, gecersiz/uzun olan yankilanmaz.
func TestAPIRequestIDValidation(t *testing.T) {
	h, _ := newAPITestHandler(nil)
	cases := []struct {
		in   string
		keep bool
	}{
		{"abc-123_X.y", true},
		{strings.Repeat("a", 64), true},
		{strings.Repeat("a", 65), false},
		{"evil-<script>", false},
		{"a b", false},
		{"", false},
	}
	for _, c := range cases {
		r := httptest.NewRequest(http.MethodGet, "/api/v1/tunnels/x", nil)
		if c.in != "" {
			r.Header.Set("X-Request-Id", c.in)
		}
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		got := w.Header().Get("X-Request-Id")
		if c.keep && got != c.in {
			t.Errorf("%q korunmadi: %q", c.in, got)
		}
		if !c.keep && (got == c.in || !strings.HasPrefix(got, "req_") || !ValidRequestID(got)) {
			t.Errorf("%q icin yeni kimlik bekleniyordu: %q", c.in, got)
		}
	}
}

// Isleyici panigi: 500 + request_id + sayac; ErrAbortHandler yeniden firlatilir.
func TestAPIRecover(t *testing.T) {
	var logBuf bytes.Buffer
	h, _ := newAPITestHandler(slog.New(slog.NewTextHandler(&logBuf, nil)))
	before := Panics("api")
	r := httptest.NewRequest(http.MethodGet, "/api/v1/tunnels/boom", nil)
	r.Header.Set("X-Request-Id", "rid-1")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != 500 || !strings.Contains(w.Body.String(), `"request_id":"rid-1"`) {
		t.Fatalf("panik yaniti: %d %s", w.Code, w.Body.String())
	}
	if Panics("api") != before+1 || !strings.Contains(logBuf.String(), "request_id=rid-1") {
		t.Fatalf("panik sayilmadi/loglanmadi: %s", logBuf.String())
	}
	if APIRequests("GET /api/v1/tunnels/{id}", "GET", "5xx") == 0 {
		t.Fatal("panik 5xx sayilmadi")
	}
	func() {
		defer func() {
			if p := recover(); p != http.ErrAbortHandler {
				t.Fatalf("ErrAbortHandler yeniden firlatilmadi: %v", p)
			}
		}()
		h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/api/v1/tunnels/abort", nil))
	}()
}

func TestRegisterCounterType(t *testing.T) {
	RegisterCounter("zorven_test_p6_total", "test", func() float64 { return 3 })
	RegisterGauge("zorven_test_p6_gauge", "test", func() float64 { return 1 })
	txt := metricsText(t)
	if !strings.Contains(txt, "# TYPE zorven_test_p6_total counter") || !strings.Contains(txt, "# TYPE zorven_test_p6_gauge gauge") {
		t.Fatal("TYPE yanlis")
	}
}

func TestProcessAndBuildMetrics(t *testing.T) {
	SetBuildInfo("9.9.9")
	txt := metricsText(t)
	for _, want := range []string{
		"process_resident_memory_bytes ", "process_open_fds ", "process_max_fds ",
		"process_cpu_seconds_total ", "process_start_time_seconds ", "go_goroutines ",
		"go_memstats_heap_inuse_bytes ", "go_memstats_heap_alloc_bytes ",
		`zorven_build_info{version="9.9.9",go_version="go`,
		"zorven_agent_connects_total ", "zorven_agent_disconnects_total ",
		`zorven_log_messages_total{level="error"}`,
	} {
		if !strings.Contains(txt, want) {
			t.Errorf("eksik metrik: %s", want)
		}
	}
}

func TestCountingHandler(t *testing.T) {
	l := slog.New(CountingHandler(slog.NewTextHandler(io.Discard, &slog.HandlerOptions{Level: slog.LevelInfo})))
	e0, d0 := LogMessages("error"), LogMessages("debug")
	l.With("a", 1).WithGroup("g").Error("x")
	l.Debug("filtrelenen")
	if LogMessages("error") != e0+1 || LogMessages("debug") != d0 {
		t.Fatal("seviye sayaci yanlis")
	}
}

func TestGoRestartAndFatal(t *testing.T) {
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var runs atomic.Int32
	done := make(chan struct{})
	GoRestart(ctx, log, "test_restart", func(context.Context) {
		if runs.Add(1) == 1 {
			panic("ilk tur")
		}
		close(done)
	})
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("panik sonrasi yeniden baslatilmadi")
	}
	if Panics("test_restart") != 1 || runs.Load() != 2 {
		t.Fatalf("panik=%d tur=%d", Panics("test_restart"), runs.Load())
	}

	exited := make(chan int, 1)
	old := FatalExit
	FatalExit = func(code int) { exited <- code }
	defer func() { FatalExit = old }()
	GoFatal(ctx, log, "test_fatal", func(context.Context) { panic("durum bozuk") })
	select {
	case code := <-exited:
		if code != 2 || Panics("test_fatal") != 1 {
			t.Fatalf("cikis kodu %d", code)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("GoFatal cikmadi")
	}
}
