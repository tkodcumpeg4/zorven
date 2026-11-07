package main

import (
	"crypto/sha256"
	"encoding/base64"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"testing/fstest"
)

func doGet(h http.Handler, path string, hdr map[string]string) *httptest.ResponseRecorder {
	r := httptest.NewRequest("GET", path, nil)
	for k, v := range hdr {
		r.Header.Set(k, v)
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w
}

func TestStaticNoDoubleEncodeAndBinaryTypes(t *testing.T) {
	big := []byte(strings.Repeat("a", 5000))
	r := httptest.NewRequest("GET", "/x", nil)
	r.Header.Set("Accept-Encoding", "gzip")
	w := httptest.NewRecorder()
	w.Header().Set("Content-Type", "text/css")
	w.Header().Set("Content-Encoding", "br")
	serveStaticBytes(w, r, "x", big, newGzipCache())
	if w.Header().Get("Content-Encoding") != "br" {
		t.Fatal("var olan Content-Encoding ezildi")
	}
	for _, ct := range []string{"image/png", "font/woff2", "application/octet-stream", "application/zip"} {
		w = httptest.NewRecorder()
		w.Header().Set("Content-Type", ct)
		serveStaticBytes(w, r, "x", big, newGzipCache())
		if w.Header().Get("Content-Encoding") != "" {
			t.Errorf("%s gzip'lenmemeli", ct)
		}
	}
	for _, ct := range []string{"text/html; charset=utf-8", "text/javascript", "image/svg+xml", "application/json", "application/xml", "text/plain"} {
		if !compressible(ct) {
			t.Errorf("%s sikistirilmali", ct)
		}
	}
}

func TestDashboardCacheHeaders(t *testing.T) {
	h := dashboardHandler()
	w := doGet(h, "/", map[string]string{"Accept-Encoding": "gzip"})
	if w.Header().Get("Cache-Control") != "no-cache" {
		t.Fatalf("index no-cache olmali: %v", w.Header())
	}
	ents, err := os.ReadDir("webdist/_nuxt")
	if err != nil {
		return // panel derlenmemis ortam
	}
	for _, e := range ents {
		if strings.HasSuffix(e.Name(), ".js") {
			w = doGet(h, "/_nuxt/"+e.Name(), nil)
			if w.Header().Get("Cache-Control") != "public, max-age=31536000, immutable" {
				t.Fatalf("_nuxt immutable degil: %v", w.Header())
			}
			break
		}
	}
}

func TestInlineScriptHashes(t *testing.T) {
	js := "window.a=1;"
	imap := `{"imports":{"#entry":"/_nuxt/x.js"}}`
	fsys := fstest.MapFS{
		"index.html": {Data: []byte(`<head><script type="importmap">` + imap + `</script><script>` + js + `</script>` +
			`<script type="application/ld+json">{"a":1}</script><script src="/x.js"></script><script></script></head>`)},
	}
	hs := inlineScriptHashes(fsys)
	want := func(s string) string {
		sum := sha256.Sum256([]byte(s))
		return "'sha256-" + base64.StdEncoding.EncodeToString(sum[:]) + "'"
	}
	if len(hs) != 2 {
		t.Fatalf("2 hash beklendi (importmap+js): %v", hs)
	}
	csp := buildCSP(nil, false, hs)
	for _, s := range []string{js, imap} {
		if !strings.Contains(csp, want(s)) {
			t.Errorf("CSP %s hash'ini icermiyor", s)
		}
	}
	if !strings.Contains(csp, "script-src-attr 'none'") {
		t.Error("script-src-attr 'none' eksik")
	}
	for _, d := range strings.Split(buildCSP(nil, false, nil), "; ") {
		if strings.HasPrefix(d, "script-src ") && strings.Contains(d, "unsafe-inline") {
			t.Errorf("hash'siz script-src unsafe-inline icermemeli: %s", d)
		}
	}
}

func TestMarketingNoInlineHandlers(t *testing.T) {
	for _, e := range []string{"index.html", "en/index.html", "ja/index.html", "docs/api.html", "pricing.html"} {
		b, err := marketingFS.ReadFile("marketing/" + e)
		if err != nil {
			continue
		}
		l := strings.ToLower(string(b))
		if strings.Contains(l, " onclick=") || strings.Contains(l, " onload=") || strings.Contains(l, " onsubmit=") {
			t.Errorf("%s satir ici olay isleyicisi iceriyor", e)
		}
	}
}

func TestCSPReportOnlyEnv(t *testing.T) {
	if cspModeFromEnvs("", "true") != "report" || cspModeFromEnvs("", "1") != "report" ||
		cspModeFromEnvs("", "") != "enforce" || cspModeFromEnvs("report", "") != "report" || cspModeFromEnvs("", "false") != "enforce" {
		t.Fatal("cspModeFromEnvs")
	}
}
