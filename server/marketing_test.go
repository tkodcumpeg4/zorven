package main

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestWWWRedirect(t *testing.T) {
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusTeapot) })
	h := wwwRedirect(next)

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("GET", "http://www.example.com:443/updates?x=1", nil))
	if rec.Code != http.StatusMovedPermanently || rec.Header().Get("Location") != "https://example.com/updates?x=1" {
		t.Fatalf("www yonlendirmesi: %d %q", rec.Code, rec.Header().Get("Location"))
	}

	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("GET", "http://example.com/updates", nil))
	if rec.Code != http.StatusTeapot {
		t.Fatalf("kok alan adi gecmeli: %d", rec.Code)
	}
}
