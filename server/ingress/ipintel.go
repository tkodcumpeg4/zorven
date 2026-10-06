package ingress

// IP Intelligence saglayicilari (FAZ 1 Part 2 / F03b-d).
//
// Tor cikis dugumleri, hosting/datacenter araliklari ve itibar listeleri hep
// LISTE olarak dagitilir. Bu yuzden hepsi ayni saglayici tipini kullanir:
//
//	kaynaklari cek -> tek ipSet'te birlestir -> ATOMIK olarak degistir
//
// Sicak yol kurali korunur: istek basina DIS CAGRI YOKTUR. Arama tamamen
// bellek icindedir; yenileme arka planda calisir.
//
// Fail-stale: yenileme basarisiz olursa ESKI snapshot kullanilmaya devam eder
// ve uyari loglanir. Sessizce bos kumeye dusmek, korunuyor sanilirken
// korunmamak demek olurdu.
//
// Zorven hicbir liste DAGITMAZ; kaynaklar isletmeci tarafindan ZORVEN_IPSET_*
// ile verilir (URL veya yerel dosya).

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/netip"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"time"
)

const (
	// ipListFetchTimeout, tek bir kaynagin indirilmesi icin ust sinir.
	ipListFetchTimeout = 60 * time.Second
	// ipListMaxBytes, tek bir kaynaktan okunacak azami bayt (bellek korumasi).
	ipListMaxBytes = 64 << 20 // 64 MB
	// defaultIPSetRefresh, varsayilan yenileme araligi.
	defaultIPSetRefresh = 6 * time.Hour
)

// ipListProvider, adlandirilmis bir IP listesi kaynagi.
type ipListProvider struct {
	name     string
	sources  []string // URL veya yerel dosya yolu
	cacheDir string
	log      *slog.Logger
	set      atomic.Pointer[ipSet]
}

func newIPListProvider(name string, sources []string, cacheDir string, log *slog.Logger) *ipListProvider {
	p := &ipListProvider{name: name, sources: sources, cacheDir: cacheDir, log: log}
	p.set.Store(newIPSet())
	return p
}

// contains, adresin listede olup olmadigini soyler. Kilitlemesizdir.
func (p *ipListProvider) contains(a netip.Addr) bool {
	if p == nil {
		return false
	}
	return p.set.Load().contains(a)
}

func (p *ipListProvider) size() int {
	if p == nil {
		return 0
	}
	return p.set.Load().size()
}

// cachePath, bu saglayicinin disk onbellegi yolu.
func (p *ipListProvider) cachePath() string {
	if p.cacheDir == "" {
		return ""
	}
	return filepath.Join(p.cacheDir, "ipset-"+p.name+".txt")
}

// loadFromCache, diskteki onbellekten yukler. Acilista agi beklememek icindir.
func (p *ipListProvider) loadFromCache() bool {
	cp := p.cachePath()
	if cp == "" {
		return false
	}
	f, err := os.Open(cp)
	if err != nil {
		return false
	}
	defer f.Close()
	set := parseIPSet(f)
	if set.size() == 0 {
		return false
	}
	p.set.Store(set)
	p.log.Info("ip listesi onbellekten yuklendi", "liste", p.name, "prefix", set.size())
	return true
}

// refresh, tum kaynaklari ceker, birlestirir ve snapshot'i atomik degistirir.
// Hicbir kaynak okunamazsa eski snapshot KORUNUR (fail-stale) ve hata doner.
func (p *ipListProvider) refresh(ctx context.Context) error {
	var merged strings.Builder
	okCount := 0
	var lastErr error

	for _, src := range p.sources {
		body, err := fetchIPList(ctx, src)
		if err != nil {
			lastErr = err
			p.log.Warn("ip listesi kaynagi okunamadi", "liste", p.name, "kaynak", src, "hata", err)
			continue
		}
		merged.WriteString(body)
		merged.WriteString("\n")
		okCount++
	}

	if okCount == 0 {
		return fmt.Errorf("liste %s: hicbir kaynak okunamadi: %w", p.name, lastErr)
	}

	set := parseIPSet(strings.NewReader(merged.String()))
	if set.size() == 0 {
		return fmt.Errorf("liste %s: kaynaklar okundu ama hic prefix cozulemedi", p.name)
	}

	p.set.Store(set) // ATOMIK: akan istekler eski veya yeni kumeyi gorur, ikisi de tutarli
	p.log.Info("ip listesi guncellendi", "liste", p.name, "prefix", set.size(),
		"kaynak", okCount, "toplam_kaynak", len(p.sources))

	if cp := p.cachePath(); cp != "" {
		if err := os.MkdirAll(p.cacheDir, 0o755); err == nil {
			// Once gecici dosyaya yaz, sonra tasi: yarim dosya birakmamak icin.
			tmp := cp + ".tmp"
			if werr := os.WriteFile(tmp, []byte(merged.String()), 0o644); werr == nil {
				_ = os.Rename(tmp, cp)
			}
		}
	}
	return nil
}

// run, arka planda periyodik yenileme dongusunu calistirir.
func (p *ipListProvider) run(ctx context.Context, every time.Duration) {
	if every <= 0 {
		every = defaultIPSetRefresh
	}
	// Acilista bir kez dene; onbellek zaten yuklenmis olabilir.
	if err := p.refresh(ctx); err != nil {
		p.log.Warn("ip listesi ilk yenileme basarisiz, mevcut snapshot korunuyor",
			"liste", p.name, "prefix", p.size(), "hata", err)
	}
	t := time.NewTicker(every)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			if err := p.refresh(ctx); err != nil {
				p.log.Warn("ip listesi yenileme basarisiz, ESKI snapshot korunuyor",
					"liste", p.name, "prefix", p.size(), "hata", err)
			}
		}
	}
}

// fetchIPList, kaynagi okur. http/https ise indirir, aksi halde yerel dosya sayar.
func fetchIPList(ctx context.Context, src string) (string, error) {
	src = strings.TrimSpace(src)
	if src == "" {
		return "", fmt.Errorf("bos kaynak")
	}
	if !strings.HasPrefix(src, "http://") && !strings.HasPrefix(src, "https://") {
		b, err := os.ReadFile(src)
		if err != nil {
			return "", err
		}
		return string(b), nil
	}
	cctx, cancel := context.WithTimeout(ctx, ipListFetchTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(cctx, http.MethodGet, src, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("User-Agent", "Zorven-IPIntel/1.0")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	b, err := io.ReadAll(io.LimitReader(resp.Body, ipListMaxBytes))
	if err != nil {
		return "", err
	}
	return string(b), nil
}

// intelInfo, tek bir IP icin liste sonuclari. Istek basina BIR KEZ hesaplanir.
type intelInfo struct {
	torExit    bool
	hosting    bool
	reputation []string // bulundugu itibar listelerinin adlari
}

// lookup, adresi tum listelerde arar.
func (x *ipIntel) lookup(ip string) intelInfo {
	if x == nil {
		return intelInfo{}
	}
	a, err := netip.ParseAddr(strings.TrimSpace(ip))
	if err != nil {
		return intelInfo{}
	}
	a = a.Unmap()
	return intelInfo{
		torExit:    x.isTor(a),
		hosting:    x.isHosting(a),
		reputation: x.reputationHits(a),
	}
}

// ipIntel, tum liste saglayicilarini bir arada tutar.
type ipIntel struct {
	tor        *ipListProvider
	hosting    *ipListProvider
	reputation []*ipListProvider // ad'lar kural eslesmesinde kullanilir
}

// reputationHits, adresin bulundugu itibar listelerinin adlarini doner.
func (x *ipIntel) reputationHits(a netip.Addr) []string {
	if x == nil || len(x.reputation) == 0 {
		return nil
	}
	var out []string
	for _, p := range x.reputation {
		if p.contains(a) {
			out = append(out, p.name)
		}
	}
	return out
}

func (x *ipIntel) isTor(a netip.Addr) bool {
	if x == nil {
		return false
	}
	return x.tor.contains(a)
}

func (x *ipIntel) isHosting(a netip.Addr) bool {
	if x == nil {
		return false
	}
	return x.hosting.contains(a)
}

// enabled, en az bir saglayici tanimli mi.
func (x *ipIntel) enabled() bool {
	return x != nil && (x.tor != nil || x.hosting != nil || len(x.reputation) > 0)
}

// start, tanimli tum saglayicilar icin onbellek yuklemesi + arka plan dongusunu baslatir.
func (x *ipIntel) start(ctx context.Context, every time.Duration) {
	if x == nil {
		return
	}
	all := make([]*ipListProvider, 0, 2+len(x.reputation))
	if x.tor != nil {
		all = append(all, x.tor)
	}
	if x.hosting != nil {
		all = append(all, x.hosting)
	}
	all = append(all, x.reputation...)
	for _, p := range all {
		p.loadFromCache()
		go p.run(ctx, every)
	}
}
