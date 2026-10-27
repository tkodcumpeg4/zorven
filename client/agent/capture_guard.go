package agent

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
)

// DXGI cokme korumasi.
//
// DXGI Desktop Duplication COM cagrilari surucuye bagimlidir; sorunlu bir
// surucu surecin kendisini dusurebilir (Go bunu yakalayamaz). Bu yuzden ilk
// DXGI yakalamasindan ONCE bir "sentinel" dosyasi yazilir (surum + zaman +
// pid); guardGoodFrames basarili kareden sonra ya da temiz kapanista silinir.
// Sonraki acilista, sahibi artik yasamayan bir sentinel varsa onceki surecin
// erken DXGI doneminde oldugu anlasilir: DXGI guardSkipFor boyunca (24 saat)
// atlanir ve kbinani yoluna dusulur.
//
// Mantik saf islevlerle (dizin, saat ve pid yoklamasi enjekte) test edilir.

const (
	guardGoodFrames  = 120
	guardSkipFor     = 24 * time.Hour
	guardSentinelPfx = "dxgi-"
	guardSentinelExt = ".sentinel"
	guardSkipFile    = "dxgi-skip.json"
)

type guardRecord struct {
	Version string    `json:"version"`
	Started time.Time `json:"started"`
	PID     int       `json:"pid"`
}

type guardSkip struct {
	Until  time.Time `json:"until"`
	Reason string    `json:"reason"`
}

type dxgiGuard struct {
	dir     string
	version string
	pid     int
	now     func() time.Time
	alive   func(pid int) bool

	mu     sync.Mutex
	active bool
	frames int
}

func newDXGIGuard(dir, version string, pid int, now func() time.Time, alive func(int) bool) *dxgiGuard {
	return &dxgiGuard{dir: dir, version: version, pid: pid, now: now, alive: alive}
}

func (g *dxgiGuard) sentinelPath(pid int) string {
	return filepath.Join(g.dir, guardSentinelPfx+strconv.Itoa(pid)+guardSentinelExt)
}

// Check, DXGI'ye izin verilip verilmedigini soyler. Sahibi olu sentinel'leri
// temizler ve atlama kaydi yazar. allowed=false ise reason nedeni tasir.
func (g *dxgiGuard) Check() (allowed bool, reason string) {
	now := g.now()
	entries, _ := os.ReadDir(g.dir)
	for _, e := range entries {
		n := e.Name()
		if !strings.HasPrefix(n, guardSentinelPfx) || !strings.HasSuffix(n, guardSentinelExt) {
			continue
		}
		p := filepath.Join(g.dir, n)
		var rec guardRecord
		b, err := os.ReadFile(p)
		if err != nil || json.Unmarshal(b, &rec) != nil || rec.PID <= 0 {
			_ = os.Remove(p) // bozuk kayit: yok say
			continue
		}
		if rec.PID == g.pid || g.alive(rec.PID) {
			continue // baska (canli) surecin sentinel'i
		}
		_ = os.Remove(p)
		sk := guardSkip{
			Until: now.Add(guardSkipFor),
			Reason: fmt.Sprintf("previous process %d (version %s, started %s) ended during early DXGI capture",
				rec.PID, rec.Version, rec.Started.UTC().Format(time.RFC3339)),
		}
		if b, err := json.Marshal(sk); err == nil {
			_ = os.MkdirAll(g.dir, 0o700)
			_ = os.WriteFile(filepath.Join(g.dir, guardSkipFile), b, 0o600)
		}
	}
	b, err := os.ReadFile(filepath.Join(g.dir, guardSkipFile))
	if err != nil {
		return true, ""
	}
	var sk guardSkip
	if json.Unmarshal(b, &sk) != nil || !now.Before(sk.Until) {
		_ = os.Remove(filepath.Join(g.dir, guardSkipFile))
		return true, ""
	}
	return false, sk.Reason + " (skipping DXGI until " + sk.Until.UTC().Format(time.RFC3339) + ")"
}

// Begin, ilk DXGI yakalamasindan once sentinel'i yazar (yinelenen cagri etkisiz).
func (g *dxgiGuard) Begin() error {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.active {
		return nil
	}
	b, err := json.Marshal(guardRecord{Version: g.version, Started: g.now(), PID: g.pid})
	if err != nil {
		return err
	}
	if err := os.MkdirAll(g.dir, 0o700); err != nil {
		return err
	}
	if err := os.WriteFile(g.sentinelPath(g.pid), b, 0o600); err != nil {
		return err
	}
	g.active, g.frames = true, 0
	return nil
}

// FrameOK, basarili bir DXGI karesini sayar; esikte sentinel silinir.
func (g *dxgiGuard) FrameOK() {
	g.mu.Lock()
	defer g.mu.Unlock()
	if !g.active {
		return
	}
	if g.frames++; g.frames >= guardGoodFrames {
		_ = os.Remove(g.sentinelPath(g.pid))
		g.active = false
	}
}

// Clear, temiz kapanista sentinel'i siler.
func (g *dxgiGuard) Clear() {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.active {
		_ = os.Remove(g.sentinelPath(g.pid))
		g.active = false
	}
}

// chooseCaptureMode, ZORVEN_SCREEN_CAPTURE degeri ve koruma sonucuyla yontemi secer:
// "dxgi" ve "gdi"/"kbinani" zorlama; bos/"auto" => koruma izin veriyorsa dxgi.
func chooseCaptureMode(env string, guardAllowed bool) string {
	switch strings.ToLower(strings.TrimSpace(env)) {
	case "dxgi":
		return "dxgi"
	case "gdi":
		return "gdi"
	case "kbinani":
		return "kbinani"
	}
	if guardAllowed {
		return "dxgi"
	}
	return "kbinani"
}
