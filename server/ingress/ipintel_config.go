package ingress

import (
	"context"
	"log/slog"
	"strings"
	"time"
)

// IPIntelConfig, isletmeci tarafindan saglanan IP liste kaynaklarini tasir.
// Zorven hicbir liste DAGITMAZ; kaynaklar ZORVEN_IPSET_* ile disaridan gelir.
type IPIntelConfig struct {
	// Tor, tor_exit kosulu icin kaynak listesi.
	Tor []string
	// Hosting, hosting kosulu icin kaynak listesi.
	Hosting []string
	// Reputation, ad -> kaynaklar. Ad, eslesmede geri doner.
	Reputation map[string][]string
	// CacheDir bos ise disk onbellegi kullanilmaz.
	CacheDir string
	// Refresh sifir ise defaultIPSetRefresh kullanilir.
	Refresh time.Duration
}

// SplitSources, virgulle ayrilmis kaynak listesini bosluklardan arindirip doner.
func SplitSources(v string) []string {
	var out []string
	for _, s := range strings.Split(v, ",") {
		if s = strings.TrimSpace(s); s != "" {
			out = append(out, s)
		}
	}
	return out
}

// ParseReputationSources, "<ad>=<kaynak>,<ad>=<kaynak>" bicimini cozer.
// Ayni ad birden cok kez gecerse kaynaklar birlestirilir. Adsiz girisler atlanir.
func ParseReputationSources(v string) map[string][]string {
	out := map[string][]string{}
	for _, part := range strings.Split(v, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		name, src, ok := strings.Cut(part, "=")
		name = strings.ToLower(strings.TrimSpace(name))
		src = strings.TrimSpace(src)
		if !ok || name == "" || src == "" {
			continue
		}
		out[name] = append(out[name], src)
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// Empty, hicbir kaynak tanimli degilse true.
func (c IPIntelConfig) Empty() bool {
	return len(c.Tor) == 0 && len(c.Hosting) == 0 && len(c.Reputation) == 0
}

// SetupIPIntel, yapilandirmadan saglayicilari kurar, router'a baglar ve arka
// plan yenilemesini baslatir. Kaynak yoksa hicbir sey yapmaz ve 0 doner.
// Donus degeri kurulan saglayici sayisidir (gunlukleme icin).
func (r *Router) SetupIPIntel(ctx context.Context, cfg IPIntelConfig, log *slog.Logger) int {
	if cfg.Empty() {
		return 0
	}
	x := &ipIntel{}
	n := 0
	if len(cfg.Tor) > 0 {
		x.tor = newIPListProvider("tor", cfg.Tor, cfg.CacheDir, log)
		n++
	}
	if len(cfg.Hosting) > 0 {
		x.hosting = newIPListProvider("hosting", cfg.Hosting, cfg.CacheDir, log)
		n++
	}
	for name, srcs := range cfg.Reputation {
		x.reputation = append(x.reputation, newIPListProvider(name, srcs, cfg.CacheDir, log))
		n++
	}
	every := cfg.Refresh
	if every <= 0 {
		every = defaultIPSetRefresh
	}
	r.SetIPIntel(x)
	x.start(ctx, every)
	return n
}
