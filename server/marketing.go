package main

import (
	"embed"
	"io/fs"
	"mime"
	"net/http"
	"path"
	"strings"
)

// marketingFS, herkese acik tanitim (pazarlama) sitesini gomer.
//
// Panel (dashboard) webdist'te ve panel.zorven.app'te servis edilir; kok domain
// (zorven.app / www) bu statik tanitim sitesini gosterir. Tam icerikli statik
// HTML oldugu icin SEO acisindan idealdir (SPA'dan iyi). TR kok dizinde, EN
// /en/ altinda; temiz URL (uzantisiz) ve dizin->index.html desteklenir.
//
//go:embed all:marketing
var marketingFS embed.FS

// contentType, dosya uzantisindan MIME turunu belirler (statik varliklar icin).
func contentType(name string) string {
	if ct := mime.TypeByExtension(path.Ext(name)); ct != "" {
		return ct
	}
	switch strings.ToLower(path.Ext(name)) {
	case ".html":
		return "text/html; charset=utf-8"
	case ".css":
		return "text/css; charset=utf-8"
	case ".js":
		return "text/javascript; charset=utf-8"
	case ".svg":
		return "image/svg+xml"
	case ".json":
		return "application/json"
	case ".xml":
		return "application/xml; charset=utf-8"
	case ".txt":
		return "text/plain; charset=utf-8"
	default:
		return "application/octet-stream"
	}
}

func marketingHandler() http.Handler {
	sub, err := fs.Sub(marketingFS, "marketing")
	if err != nil {
		return http.NotFoundHandler()
	}
	index, _ := fs.ReadFile(sub, "index.html")

	// write, gomulu dosyayi dogru Content-Type ile yazar. http.FileServer
	// KULLANILMAZ: o, "index.html" ve dizinler icin 301 kanonik yonlendirme
	// yapar ve bizim dizin->index eslemesiyle birlikte yonlendirme dongusu olur.
	write := func(w http.ResponseWriter, name string, b []byte) {
		w.Header().Set("Content-Type", contentType(name))
		// HTML, CSS ve JS revalidate edilir ki icerik/stil guncellemeleri
		// kullaniciya ANINDA yansisin. Diger statikler (svg, font, txt, xml)
		// 1 saat cache'lenir. (Onceden css/js de 1 saat cache'leniyordu; bu,
		// yeni ozelliklerin gec gorunmesine yol aciyordu.)
		ext := strings.ToLower(path.Ext(name))
		if !strings.HasPrefix(name, "_") && (ext == ".html" || ext == ".css" || ext == ".js") {
			w.Header().Set("Cache-Control", "no-cache")
		} else {
			w.Header().Set("Cache-Control", "public, max-age=3600")
		}
		w.WriteHeader(http.StatusOK)
		w.Write(b)
	}

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		clean := strings.Trim(strings.TrimPrefix(r.URL.Path, "/"), "/")
		if clean == "" {
			clean = "index.html"
		}
		// Dizin (or. "en", "en/legal") -> icindeki index.html.
		if fi, err := fs.Stat(sub, clean); err == nil && fi.IsDir() {
			clean += "/index.html"
		}
		// Tam dosya.
		if fi, err := fs.Stat(sub, clean); err == nil && !fi.IsDir() {
			if b, err := fs.ReadFile(sub, clean); err == nil {
				write(w, clean, b)
				return
			}
		}
		// Temiz URL: "/legal/terms" -> "legal/terms.html", "/en/docs" -> "en/docs.html".
		if !strings.Contains(clean, ".") {
			if fi, err := fs.Stat(sub, clean+".html"); err == nil && !fi.IsDir() {
				if b, err := fs.ReadFile(sub, clean+".html"); err == nil {
					write(w, clean+".html", b)
					return
				}
			}
		}
		// Bulunamadi: kok tanitim sayfasina dus (gecici).
		write(w, "index.html", index)
	})
}
