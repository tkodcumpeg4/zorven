package tunsec

// Bot korumasi (B1-B3) ve hiz siniri ayari: dogrulama, varsayilanlar, derleme.
// Uygulama (PoW, cerez, skor, limiter) botguard paketinde; burada yalniz ayar.

import (
	"fmt"
	"strings"
)

// Bot kipleri.
const (
	BotOff        = "off"
	BotSuspicious = "suspicious" // yalniz B3 skoru esigi asanlar sorgulanir (tum planlar)
	BotAlways     = "always"     // cerezi olmayan herkes sorgulanir (Pro+)
)

// Hiz siniri eylemleri.
const (
	RateBlock     = "block"
	RateChallenge = "challenge"
)

// Sinirlar ve varsayilanlar.
const (
	MinDifficulty     = 12
	MaxDifficulty     = 22
	DefaultDifficulty = 18
	MinTTLMinutes     = 5
	MaxTTLMinutes     = 10080
	DefaultTTLMinutes = 60

	MaxExcludePaths = 32
	maxPathLen      = 200

	MaxRateRequests      = 1_000_000
	DefaultRateRequests  = 120
	MaxRateWindowSeconds = 3600
	DefaultRateWindow    = 60
)

// BotConfig, tunel bot korumasi ayari.
type BotConfig struct {
	Mode              string   `json:"mode"`                // off | suspicious | always
	Difficulty        int      `json:"difficulty"`          // PoW bit sayisi 12-22 (vars. 18)
	TTLMinutes        int      `json:"ttl_minutes"`         // __zv_bot cerez omru, 5-10080 (vars. 60)
	ExcludePaths      []string `json:"exclude_paths"`       // tam yol veya sonda * ile on-ek; en fazla 32
	AllowVerifiedBots bool     `json:"allow_verified_bots"` // dogrulanmis arama botlari sorgulanmaz
	BlockEmptyUA      bool     `json:"block_empty_ua"`      // bos User-Agent = 403 (bot:block)
}

// RateConfig, tunel+IP basina hiz siniri.
type RateConfig struct {
	Enabled       bool     `json:"enabled"`
	Requests      int      `json:"requests"`       // pencere basina istek (= patlama kapasitesi)
	WindowSeconds int      `json:"window_seconds"` // 1-3600
	Action        string   `json:"action"`         // block | challenge
	ExcludePaths  []string `json:"exclude_paths"`
}

// normalizeBot, BotConfig'i dogrular/kanonik yapar. Bolum tamamen bos (sifir deger)
// ise varsayilanlar uygulanir (allow_verified_bots = true dahil).
func normalizeBot(b BotConfig) (BotConfig, error) {
	zero := b.Mode == "" && b.Difficulty == 0 && b.TTLMinutes == 0 && len(b.ExcludePaths) == 0 &&
		!b.AllowVerifiedBots && !b.BlockEmptyUA
	if zero {
		b.AllowVerifiedBots = true
	}
	b.Mode = strings.ToLower(strings.TrimSpace(b.Mode))
	switch b.Mode {
	case "":
		b.Mode = BotOff
	case BotOff, BotSuspicious, BotAlways:
	default:
		return b, fmt.Errorf("bot.mode: off, suspicious veya always olmali")
	}
	if b.Difficulty == 0 {
		b.Difficulty = DefaultDifficulty
	}
	if b.Difficulty < MinDifficulty || b.Difficulty > MaxDifficulty {
		return b, fmt.Errorf("bot.difficulty: %d-%d araliginda olmali", MinDifficulty, MaxDifficulty)
	}
	if b.TTLMinutes == 0 {
		b.TTLMinutes = DefaultTTLMinutes
	}
	if b.TTLMinutes < MinTTLMinutes || b.TTLMinutes > MaxTTLMinutes {
		return b, fmt.Errorf("bot.ttl_minutes: %d-%d araliginda olmali", MinTTLMinutes, MaxTTLMinutes)
	}
	ex, err := normalizePaths("bot.exclude_paths", b.ExcludePaths)
	if err != nil {
		return b, err
	}
	b.ExcludePaths = ex
	return b, nil
}

func normalizeRate(r RateConfig) (RateConfig, error) {
	r.Action = strings.ToLower(strings.TrimSpace(r.Action))
	switch r.Action {
	case "":
		r.Action = RateBlock
	case RateBlock, RateChallenge:
	default:
		return r, fmt.Errorf("rate.action: block veya challenge olmali")
	}
	if r.Requests == 0 {
		r.Requests = DefaultRateRequests
	}
	if r.Requests < 1 || r.Requests > MaxRateRequests {
		return r, fmt.Errorf("rate.requests: 1-%d araliginda olmali", MaxRateRequests)
	}
	if r.WindowSeconds == 0 {
		r.WindowSeconds = DefaultRateWindow
	}
	if r.WindowSeconds < 1 || r.WindowSeconds > MaxRateWindowSeconds {
		return r, fmt.Errorf("rate.window_seconds: 1-%d araliginda olmali", MaxRateWindowSeconds)
	}
	ex, err := normalizePaths("rate.exclude_paths", r.ExcludePaths)
	if err != nil {
		return r, err
	}
	r.ExcludePaths = ex
	return r, nil
}

// normalizePaths: "/" ile baslar, '*' yalniz sonda, tekrarsiz, sira korunur.
// Bos liste her zaman bos dilim doner (JSON'da [] ; null degil).
func normalizePaths(field string, in []string) ([]string, error) {
	if len(in) > MaxExcludePaths {
		return nil, fmt.Errorf("%s: en fazla %d yol", field, MaxExcludePaths)
	}
	out := make([]string, 0, len(in))
	seen := map[string]bool{}
	for _, p := range in {
		p = strings.TrimSpace(p)
		if p == "" || seen[p] {
			continue
		}
		if len(p) > maxPathLen || p[0] != '/' {
			return nil, fmt.Errorf("%s: %q gecersiz (\"/\" ile baslamali, en fazla %d karakter)", field, p, maxPathLen)
		}
		if i := strings.IndexByte(p, '*'); i >= 0 && i != len(p)-1 {
			return nil, fmt.Errorf("%s: %q gecersiz ('*' yalniz sonda olabilir)", field, p)
		}
		if strings.ContainsAny(p, "\x00\r\n") {
			return nil, fmt.Errorf("%s: %q kontrol karakteri iceremez", field, p)
		}
		seen[p] = true
		out = append(out, p)
	}
	return out, nil
}

// PathMatch, basit glob: tam eslesme ya da sonda '*' ise on-ek eslesmesi.
func PathMatch(pattern, path string) bool {
	if pre, ok := strings.CutSuffix(pattern, "*"); ok {
		return strings.HasPrefix(path, pre)
	}
	return pattern == path
}

// PathMatchAny, listedeki desenlerden biri eslesiyor mu.
func PathMatchAny(patterns []string, path string) bool {
	for _, p := range patterns {
		if PathMatch(p, path) {
			return true
		}
	}
	return false
}

// BotSettings, derlenmis bot ayari (ingress sicak yolu).
type BotSettings struct {
	Mode          string
	Difficulty    int
	TTLMinutes    int
	ExcludePaths  []string
	AllowVerified bool
	BlockEmptyUA  bool
}

// RateSettings, derlenmis hiz siniri ayari.
type RateSettings struct {
	Enabled       bool
	Requests      int
	WindowSeconds int
	Action        string
	ExcludePaths  []string
}

// BotActive, bot korumasi off degil mi.
func (c *Compiled) BotActive() bool { return c != nil && c.Bot.Mode != BotOff && c.Bot.Mode != "" }

// RateActive, hiz siniri etkin mi.
func (c *Compiled) RateActive() bool { return c != nil && c.Rate.Enabled }

// ChallengeAvailable, /.zorven/challenge ucu anlamli mi (bot acik veya rate=challenge).
func (c *Compiled) ChallengeAvailable() bool {
	return c.BotActive() || (c.RateActive() && c.Rate.Action == RateChallenge)
}

// BotExcluded, yol bot sorgusundan muaf mi.
func (c *Compiled) BotExcluded(path string) bool { return PathMatchAny(c.Bot.ExcludePaths, path) }

// RateExcluded, yol hiz sinirindan muaf mi.
func (c *Compiled) RateExcluded(path string) bool { return PathMatchAny(c.Rate.ExcludePaths, path) }
