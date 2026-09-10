package main

import (
	"embed"
	"io/fs"
	"net/http"
	"strings"
)

// webdist, statik olarak derlenmis Nuxt dashboard'ini gomer.
//
// "all:" oneki, Nuxt'in "_nuxt/" gibi alt cizgiyle baslayan dizinlerini de
// dahil eder (varsayilan embed onlari atlar). Dosyalar scripts/build-dashboard.sh
// ile uretilir; repoda yalnizca bir yer tutucu index.html commit'li.
//
//go:embed all:webdist
var webdist embed.FS

// dashboardHandler, gomulu SPA'yi servis eder.
//
// SPA fallback: dosya bulunamayan istekler index.html'e dusurulur ki istemci
// tarafi Vue Router (/terminal/x, /screen/x, /tunnels ...) calissin. Statik
// varliklar (_nuxt/... , .js, .css, .ico) bulunursa oldugu gibi servis edilir.
//
// Bu handler PUBLIC'tir (admin middleware'inden gecmez): statik arayuz dosyalari
// gizli degil, altindaki /api/v1 zaten admin anahtariyla korunuyor ve arayuz
// kendi icinde anahtar kapisi gosteriyor.
func dashboardHandler() http.Handler {
	sub, err := fs.Sub(webdist, "webdist")
	if err != nil {
		// Derleme zamani garantili; yine de guvenli bir bos handler don.
		return http.NotFoundHandler()
	}
	fileServer := http.FileServer(http.FS(sub))

	index, _ := fs.ReadFile(sub, "index.html")

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p := strings.TrimPrefix(r.URL.Path, "/")
		if p == "" {
			p = "index.html"
		}

		// Dosya varsa oldugu gibi servis et.
		if f, err := sub.Open(p); err == nil {
			f.Close()
			fileServer.ServeHTTP(w, r)
			return
		}

		// Bulunamadi: SPA rotasi olabilir -> index.html don.
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		// Statik SPA kabuk cachelenebilir degil; her zaman taze index.
		w.Header().Set("Cache-Control", "no-cache")
		w.WriteHeader(http.StatusOK)
		w.Write(index)
	})
}
