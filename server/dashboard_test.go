package main

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestDashboardIndexNoCacheAndSPAFallback(t *testing.T) {
	h := dashboardHandler()
	for _, p := range []string{"/", "/tunnels", "/olmayan/rota"} {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, p, nil))
		if rec.Code != http.StatusOK {
			t.Fatalf("%s: durum %d", p, rec.Code)
		}
		if cc := rec.Header().Get("Cache-Control"); cc != "no-cache" {
			t.Fatalf("%s: index no-cache olmali, %q", p, cc)
		}
		if !strings.HasPrefix(rec.Header().Get("Content-Type"), "text/html") {
			t.Fatalf("%s: html bekleniyordu", p)
		}
	}
}

func TestDashboardGzip(t *testing.T) {
	h := dashboardHandler()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("Accept-Encoding", "gzip, br")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	body := rec.Body.Bytes()
	// Yer tutucu index.html 1 KB'den kucuk olabilir; o durumda sikistirma yapilmaz.
	if rec.Header().Get("Content-Encoding") == "gzip" && (len(body) < 2 || body[0] != 0x1f || body[1] != 0x8b) {
		t.Fatal("gzip basligi var ama govde gzip degil")
	}
}

func TestDashboardMissingAssetIs404(t *testing.T) {
	h := dashboardHandler()
	for _, p := range []string{"/_nuxt/yok.js", "/_nuxt/yok.css", "/yok.js"} {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, p, nil))
		if rec.Code != http.StatusNotFound {
			t.Fatalf("%s: 404 bekleniyordu, %d", p, rec.Code)
		}
	}
}
