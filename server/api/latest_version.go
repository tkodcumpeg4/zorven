package api

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Istemci surum karsilastirmasi: sunucunun dagittigi /bin/manifest.json (CLI)
// ve /bin/desktop.json (masaustu) dosyalarindaki "version", panelde
// "guncelleme var" ipucunun kaynagidir. Dosyalar serveBinary ile ayni
// konumlardan okunur ve kisa sure onbelleklenir.

const latestVersionTTL = 30 * time.Second

type latestCacheEntry struct {
	version string
	at      time.Time
}

var (
	latestMu    sync.Mutex
	latestCache = map[string]latestCacheEntry{}
)

// latestClientVersion, verilen manifest dosyasindaki (or. "manifest.json")
// en son yayinlanmis surumu doner; yoksa "".
func latestClientVersion(manifestFile string) string {
	latestMu.Lock()
	defer latestMu.Unlock()
	if e, ok := latestCache[manifestFile]; ok && time.Since(e.at) < latestVersionTTL {
		return e.version
	}
	v := ""
	for _, p := range []string{
		filepath.Join("server", "bin", manifestFile),
		filepath.Join("bin", manifestFile),
	} {
		b, err := os.ReadFile(p)
		if err != nil {
			continue
		}
		var m struct {
			Version string `json:"version"`
		}
		if json.Unmarshal(b, &m) == nil && m.Version != "" {
			v = strings.TrimSpace(m.Version)
			break
		}
	}
	latestCache[manifestFile] = latestCacheEntry{version: v, at: time.Now()}
	return v
}

// manifestFor, istemci turune gore manifest dosyasini secer.
func manifestFor(appKind string) string {
	if appKind == "desktop" {
		return "desktop.json"
	}
	return "manifest.json"
}

// parseSemver, "v0.2.2" / "0.2.2-rc1" gibi degerleri [3]int'e cevirir.
func parseSemver(v string) ([3]int, bool) {
	var out [3]int
	v = strings.TrimPrefix(strings.TrimSpace(v), "v")
	if i := strings.IndexAny(v, "-+"); i >= 0 {
		v = v[:i]
	}
	parts := strings.Split(v, ".")
	if v == "" || len(parts) > 3 {
		return out, false
	}
	for i, p := range parts {
		n, err := strconv.Atoi(p)
		if err != nil || n < 0 {
			return out, false
		}
		out[i] = n
	}
	return out, true
}

// isOutdated, current surumun latest'ten KESIN eski olup olmadigini soyler.
// Ayristirilamayan ("dev", bos) surumler asla "eski" sayilmaz.
func isOutdated(current, latest string) bool {
	c, ok1 := parseSemver(current)
	l, ok2 := parseSemver(latest)
	if !ok1 || !ok2 {
		return false
	}
	for i := 0; i < 3; i++ {
		if c[i] != l[i] {
			return c[i] < l[i]
		}
	}
	return false
}
