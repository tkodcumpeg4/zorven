package main

import (
	"bytes"
	"compress/gzip"
	"embed"
	"io/fs"
	"mime"
	"net/http"
	"path"
	"strconv"
	"strings"
	"sync"
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
// Performans: _nuxt/ altindaki dosya adlari icerik ozetli oldugu icin bir yil
// "immutable" onbelleklenir; metin varliklari (js/css/html/json/svg) istemci
// destekliyorsa gzip'li gonderilir (sikistirma ilk istekte bir kez yapilip
// bellekte tutulur). index.html her zaman "no-cache" ki yeni surum aninda gelsin.
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
	index, err := fs.ReadFile(sub, "index.html")
	if err != nil {
		// Panel derlenmeden sunucu derlenmis (index.html yalniz derleme ciktisidir,
		// repoda tutulmaz ki eski bir kabuk yeni varliklarla karisip bos ekran
		// uretmesin). Bos sayfa yerine acik bir mesaj ver.
		index = []byte("<!doctype html><meta charset=utf-8><title>Zorven</title><p style=\"font:14px system-ui;padding:2rem\">Panel derlenmemis: scripts/build-dashboard.sh calistirip sunucuyu yeniden derleyin.</p>")
	}
	gz := &gzipCache{m: map[string][]byte{}}

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p := strings.TrimPrefix(r.URL.Path, "/")
		if p == "" {
			p = "index.html"
		}

		data, err := fs.ReadFile(sub, p)
		if err != nil && (strings.HasPrefix(p, "_nuxt/") || path.Ext(p) == ".js" || path.Ext(p) == ".css") {
			// Eksik derleme varligi SPA'ya dusurulMEZ: HTML'i JS/CSS diye donmek
			// tarayicida MIME hatasi + bos ekran uretir (index.html ile _nuxt
			// farkli derlemelerden gelirse). Acik 404 ver.
			http.NotFound(w, r)
			return
		}
		if err != nil {
			// Bulunamadi: SPA rotasi olabilir -> index.html don.
			p, data = "index.html", index
		}

		ct := mime.TypeByExtension(path.Ext(p))
		if ct == "" {
			ct = http.DetectContentType(data)
		}
		w.Header().Set("Content-Type", ct)
		if strings.HasPrefix(p, "_nuxt/") {
			w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
		} else if strings.HasSuffix(p, ".html") {
			w.Header().Set("Cache-Control", "no-cache")
		} else {
			w.Header().Set("Cache-Control", "public, max-age=3600")
		}

		if compressible(ct) && len(data) > 1024 {
			w.Header().Add("Vary", "Accept-Encoding")
			if strings.Contains(r.Header.Get("Accept-Encoding"), "gzip") {
				if z := gz.get(p, data); z != nil {
					w.Header().Set("Content-Encoding", "gzip")
					w.Header().Set("Content-Length", strconv.Itoa(len(z)))
					w.WriteHeader(http.StatusOK)
					if r.Method != http.MethodHead {
						w.Write(z)
					}
					return
				}
			}
		}
		w.Header().Set("Content-Length", strconv.Itoa(len(data)))
		w.WriteHeader(http.StatusOK)
		if r.Method != http.MethodHead {
			w.Write(data)
		}
	})
}

func compressible(ct string) bool {
	return strings.HasPrefix(ct, "text/") || strings.Contains(ct, "javascript") ||
		strings.Contains(ct, "json") || strings.Contains(ct, "svg")
}

// gzipCache, gomulu dosyalarin gzip halini ilk istekte uretip saklar
// (gomulu icerik degismez; bellek kullanimi panel boyutuyla sinirli).
type gzipCache struct {
	mu sync.RWMutex
	m  map[string][]byte
}

func (c *gzipCache) get(name string, data []byte) []byte {
	c.mu.RLock()
	z, ok := c.m[name]
	c.mu.RUnlock()
	if ok {
		return z
	}
	var buf bytes.Buffer
	zw, err := gzip.NewWriterLevel(&buf, gzip.BestCompression)
	if err != nil {
		return nil
	}
	if _, err := zw.Write(data); err != nil {
		return nil
	}
	if err := zw.Close(); err != nil {
		return nil
	}
	z = buf.Bytes()
	c.mu.Lock()
	c.m[name] = z
	c.mu.Unlock()
	return z
}
