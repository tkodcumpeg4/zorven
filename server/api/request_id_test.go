package api

import (
	"bytes"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// A5 #7: Middleware X-Request-Id'yi yalniz gecerliyse yankilar; s.fail kimligi
// ERROR loguna ve 5xx govdesine koyar.
func TestMiddlewareRequestIDValidatedAndInFailPath(t *testing.T) {
	mw, _, tok := newGuardMW(t)
	var logBuf bytes.Buffer
	s := &Server{Logger: slog.New(slog.NewTextHandler(&logBuf, nil))}
	mw.Next = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s.fail(w, errors.New("beklenmeyen db hatasi"))
	})
	send := func(rid string) *httptest.ResponseRecorder {
		w := httptest.NewRecorder()
		r := httptest.NewRequest("GET", "/api/v1/tunnels", nil)
		r.RemoteAddr = "198.51.100.9:1"
		r.Header.Set("Authorization", "Bearer "+tok)
		if rid != "" {
			r.Header.Set("X-Request-Id", rid)
		}
		mw.ServeHTTP(w, r)
		return w
	}
	w := send("destek-42")
	if w.Code != 500 || w.Header().Get("X-Request-Id") != "destek-42" {
		t.Fatalf("gecerli kimlik korunmadi: %d %q", w.Code, w.Header().Get("X-Request-Id"))
	}
	if !strings.Contains(w.Body.String(), `"request_id":"destek-42"`) || !strings.Contains(logBuf.String(), "request_id=destek-42") {
		t.Fatalf("kimlik govdede/logda yok: %s | %s", w.Body.String(), logBuf.String())
	}
	bad := "evil-<script>-" + strings.Repeat("A", 300)
	w = send(bad)
	got := w.Header().Get("X-Request-Id")
	if got == bad || strings.Contains(w.Body.String(), "<script>") || !strings.HasPrefix(got, "req_") {
		t.Fatalf("gecersiz kimlik yankilandi: %q", got)
	}
	// Ust katman (InstrumentAPI) kimligi zaten yazdiysa Middleware ona dokunmaz.
	w = httptest.NewRecorder()
	w.Header().Set("X-Request-Id", "ust-katman")
	r := httptest.NewRequest("GET", "/api/v1/tunnels", nil)
	r.Header.Set("Authorization", "Bearer "+tok)
	r.Header.Set("X-Request-Id", "baska")
	mw.ServeHTTP(w, r)
	if w.Header().Get("X-Request-Id") != "ust-katman" {
		t.Fatalf("ust katman kimligi ezildi: %q", w.Header().Get("X-Request-Id"))
	}
	// 4xx govdesinde kimlik yok (yalniz 5xx).
	w = httptest.NewRecorder()
	writeJSONError(w, 404, "not_found", "x")
	if strings.Contains(w.Body.String(), "request_id") {
		t.Fatal("4xx govdesinde request_id olmamali")
	}
}
