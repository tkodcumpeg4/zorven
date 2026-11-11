package wgnet

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

func statusServer(t *testing.T, status int, retryAfter, code string) *httptest.Server {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if retryAfter != "" {
			w.Header().Set("Retry-After", retryAfter)
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = io.WriteString(w, `{"error":{"code":"`+code+`","message":"x"}}`)
	}))
	t.Cleanup(srv.Close)
	return srv
}

// 401/403/402 ve net_disabled kalici; diger 503 ve 429 gecici (Retry-After ile).
func TestRunSessionStatusClassification(t *testing.T) {
	cases := []struct {
		status    int
		code      string
		fatal     bool
		transient bool
		ra        time.Duration
	}{
		{401, "invalid_token", true, false, 0},
		{403, "token_required", true, false, 0},
		{402, "plan", true, false, 0},
		{503, "net_disabled", true, false, 0},
		{503, "store_unavailable", false, true, 5 * time.Second},
		{503, "", false, true, 5 * time.Second},
		{429, "rate_limited", false, true, 5 * time.Second},
		{500, "x", false, false, 0},
	}
	for _, c := range cases {
		srv := statusServer(t, c.status, "5", c.code)
		err := RunSession(context.Background(), SessionConfig{ServerAddr: strings.TrimPrefix(srv.URL, "http://"), Insecure: true, Token: "t"}, nil)
		var fe *FatalSessionError
		var te *TransientSessionError
		if errors.As(err, &fe) != c.fatal || errors.As(err, &te) != c.transient {
			t.Fatalf("%d/%s: %T %v", c.status, c.code, err, err)
		}
		if c.transient && te.RetryAfter != c.ra {
			t.Fatalf("%d: retry-after %s", c.status, te.RetryAfter)
		}
	}
}

func TestParseRetryAfter(t *testing.T) {
	now := time.Date(2026, 11, 11, 10, 0, 0, 0, time.UTC)
	h := http.Header{}
	for in, want := range map[string]time.Duration{
		"":                              0,
		"7":                             7 * time.Second,
		"-3":                            0,
		"99999":                         maxRetryAfter,
		"bozuk":                         0,
		"Wed, 11 Nov 2026 10:00:30 GMT": 30 * time.Second,
	} {
		h.Set("Retry-After", in)
		if got := parseRetryAfter(h, now); got != want {
			t.Errorf("%q: %s, beklenen %s", in, got, want)
		}
	}
}

// Accessor 429'da durmaz; Retry-After'a uyarak yeniden dener.
func TestAccessorRetriesOn429(t *testing.T) {
	var mu sync.Mutex
	var hits []time.Time
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		hits = append(hits, time.Now())
		mu.Unlock()
		w.Header().Set("Retry-After", "2")
		w.WriteHeader(http.StatusTooManyRequests)
	}))
	defer srv.Close()
	a := NewAccessor(SessionConfig{ServerAddr: strings.TrimPrefix(srv.URL, "http://"), Insecure: true, Token: "t",
		Log: slog.New(slog.NewTextHandler(io.Discard, nil))})
	ctx, cancel := context.WithTimeout(context.Background(), 3500*time.Millisecond)
	defer cancel()
	if err := a.Run(ctx); err != nil {
		t.Fatalf("gecici hatada Run kalici hatayla donmemeli: %v", err)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(hits) != 2 {
		t.Fatalf("Retry-After=2 ile 3.5 sn'de 2 deneme beklenirdi, %d", len(hits))
	}
	if gap := hits[1].Sub(hits[0]); gap < 2*time.Second {
		t.Fatalf("Retry-After'a uyulmadi: %s", gap)
	}
}
