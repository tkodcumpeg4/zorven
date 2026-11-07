package main

import (
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"strconv"
	"strings"
	"sync"
)

// compressible: gzip'lenecek metin turleri (html/js/css/json/svg/xml/txt).
// Zaten sikisik turler (png, woff2, zip, ikili) dahil DEGILDIR.
func compressible(ct string) bool {
	return strings.HasPrefix(ct, "text/") || strings.Contains(ct, "javascript") ||
		strings.Contains(ct, "json") || strings.Contains(ct, "svg") || strings.Contains(ct, "xml")
}

// gzipCache, gomulu dosyalarin gzip halini ilk istekte uretip saklar
// (gomulu icerik degismez; bellek kullanimi gomulu boyutla sinirli).
type gzipCache struct {
	mu sync.RWMutex
	m  map[string][]byte
}

func newGzipCache() *gzipCache { return &gzipCache{m: map[string][]byte{}} }

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

// acceptsGzip: Accept-Encoding gzip kabul ediyor mu (q=0 reddi dahil).
func acceptsGzip(h string) bool {
	for _, part := range strings.Split(h, ",") {
		name, params, _ := strings.Cut(strings.TrimSpace(part), ";")
		if !strings.EqualFold(strings.TrimSpace(name), "gzip") {
			continue
		}
		params = strings.ReplaceAll(params, " ", "")
		if q, ok := strings.CutPrefix(params, "q="); ok {
			if f, err := strconv.ParseFloat(q, 64); err == nil && f <= 0 {
				return false
			}
		}
		return true
	}
	return false
}

// serveStaticBytes, Content-Type ve Cache-Control ZATEN ayarlanmis bir yanita
// govdeyi yazar: ETag + If-None-Match (304), uygunsa gzip + Vary. Yanitta
// Content-Encoding zaten varsa dokunmaz. Yalniz gomulu statik dosyalar icindir
// (/bin/ indirmeleri bu yoldan GECMEZ: Range desteklemeli, sikistirilmaz).
func serveStaticBytes(w http.ResponseWriter, r *http.Request, key string, data []byte, gz *gzipCache) {
	h := w.Header()
	ct := h.Get("Content-Type")
	sum := sha256.Sum256(data)
	etag := `"` + hex.EncodeToString(sum[:8]) + `"`
	h.Set("ETag", etag)
	canGzip := h.Get("Content-Encoding") == "" && compressible(ct) && len(data) > 1024
	if canGzip {
		h.Add("Vary", "Accept-Encoding")
	}
	if inm := r.Header.Get("If-None-Match"); inm != "" && (r.Method == http.MethodGet || r.Method == http.MethodHead) {
		for _, t := range strings.Split(inm, ",") {
			t = strings.TrimPrefix(strings.TrimSpace(t), "W/")
			if t == etag || t == "*" {
				w.WriteHeader(http.StatusNotModified)
				return
			}
		}
	}
	if canGzip && acceptsGzip(r.Header.Get("Accept-Encoding")) {
		if z := gz.get(key, data); z != nil {
			h.Set("Content-Encoding", "gzip")
			h.Set("Content-Length", strconv.Itoa(len(z)))
			w.WriteHeader(http.StatusOK)
			if r.Method != http.MethodHead {
				w.Write(z)
			}
			return
		}
	}
	h.Set("Content-Length", strconv.Itoa(len(data)))
	w.WriteHeader(http.StatusOK)
	if r.Method != http.MethodHead {
		w.Write(data)
	}
}
