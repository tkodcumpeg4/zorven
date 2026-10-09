package agent

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"runtime"
	"time"

	"github.com/minio/selfupdate"
	"github.com/tkodcumpeg4/zorven/shared/updatesig"
)

// updateManifest, sunucunun /bin/manifest.json ciktisidir. build-clients.sh
// tarafindan uretilir; her hedef icin dosya adi + sha256 tasir.
type updateManifest struct {
	Version string `json:"version"`
	Files   map[string]struct {
		Name   string `json:"name"`
		SHA256 string `json:"sha256"`
		Size   int64  `json:"size"`
	} `json:"files"`
}

const updateCheckInterval = 6 * time.Hour

// CheckUpdateNow, tek seferlik (on-plan) bir guncelleme kontrolu yapar: tunel
// baglantisi kurmadan yalnizca manifest'e bakip gerekiyorsa gunceller. `zorven
// update` komutu ve testler icin. Guncelleme uygulanirsa process yeniden baslar.
func (a *Agent) CheckUpdateNow(ctx context.Context) error {
	if err := a.setupTLS(); err != nil {
		return err
	}
	a.forceUpdate.Store(true) // elle: sunucu ayari (otomatik guncelleme) yuklenmemis olsa da kontrol et
	defer a.forceUpdate.Store(false)
	a.maybeUpdate(ctx, "elle")
	return a.lastUpdateErr
}

func (a *Agent) httpBase() string {
	scheme := "https"
	if a.Insecure {
		scheme = "http"
	}
	return scheme + "://" + a.ServerAddr
}

func (a *Agent) updateHTTPClient() *http.Client {
	if a.httpClient != nil {
		return a.httpClient
	}
	return http.DefaultClient
}

// runUpdater, acilis + periyodik guncelleme kontrolunu yurutur. ctx bitince
// durur. WSS "update_available" sinyali ayrica readLoop'tan maybeUpdate'i tetikler.
func (a *Agent) runUpdater(ctx context.Context) {
	// Etkin ayar: yerel bayrak VEYA uzak ayar kapattiysa guncelleme yapilmaz.
	if a.NoAutoUpdate || !a.settings.autoUpdateEnabled() {
		return
	}
	// Once baglanti otursun; sonra ilk kontrol.
	select {
	case <-ctx.Done():
		return
	case <-time.After(10 * time.Second):
	}
	a.maybeUpdate(ctx, "acilis")

	t := time.NewTicker(updateCheckInterval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			a.maybeUpdate(ctx, "periyodik")
		}
	}
}

// maybeUpdate, manifest'i kontrol eder; yeni surum varsa indirir, sha256
// dogrular, uygular ve process'i yeniden baslatir.
//
// TASARIM: hicbir hata process'i dusurmez — guncelleme "best-effort"tur;
// basarisiz olursa mevcut surumle calismaya devam eder (asla brick etmez).
func (a *Agent) maybeUpdate(ctx context.Context, reason string) {
	// Gelistirme ikilisi (surum yok/"dev") veya kapaliysa hicbir sey yapma.
	forced := a.forceUpdate.Load()
	a.lastUpdateErr = nil
	// Masaustu uygulamasinin gomulu ajani ASLA kendini guncellemez: calisan exe
	// masaustu uygulamasidir; CLI ikilisini onun uzerine yazmak uygulamayi CLI'ye
	// cevirir (2026-10-09 olayi). Masaustunun kendi guncelleyicisi var (desktop.json).
	if a.AppKind == "desktop" {
		return
	}
	if a.Version == "" || a.Version == "dev" {
		if forced {
			a.lastUpdateErr = fmt.Errorf("gelistirme surumu (%q) guncellenmez", a.Version)
		}
		return
	}
	// Otomatik yol sunucu ayarina bagli; elle `zorven update` her zaman kontrol eder
	// (eskiden ayarlar baglanti olmadan yuklenmedigi icin sessizce "guncel" diyordu).
	if !forced && (a.NoAutoUpdate || !a.settings.autoUpdateEnabled()) {
		return
	}
	// Es zamanli tetikleri (acilis/periyodik/WSS) serilestir.
	if !a.updating.CompareAndSwap(false, true) {
		return
	}
	defer a.updating.Store(false)

	// Executable yolunu APPLY'DAN ONCE yakala: Linux'ta selfupdate eski inode'u
	// unlink eder, sonrasinda os.Executable() "/path (deleted)" doner ve exec
	// ENOENT verir. Simdi yakalarsak yol gercek ve calisan dosyaya isaret eder.
	exePath, err := os.Executable()
	if err != nil {
		a.Log.Warn("guncelleme: executable yolu alinamadi", "hata", err)
		return
	}

	m, err := a.fetchManifest(ctx)
	if err != nil {
		if errors.Is(err, errUpdateSignature) {
			// Imza hatasi sessiz gecilmez: ya yayin imzasiz ya da MITM/sahte sunucu.
			a.Log.Warn("guncelleme reddedildi", "hata", err)
		} else {
			a.Log.Debug("guncelleme: manifest alinamadi", "hata", err)
		}
		if forced {
			a.lastUpdateErr = fmt.Errorf("surum bilgisi alinamadi: %w", err)
		}
		return
	}
	// Imza gecerli ama surum mevcuttan buyuk degilse: guncel (esit) ya da
	// downgrade denemesi (kucuk) — ikisi de uygulanmaz.
	if m.Version == "" || !updatesig.Newer(m.Version, a.Version) {
		if m.Version != "" && m.Version != a.Version {
			a.Log.Warn("guncelleme: manifest surumu mevcuttan buyuk degil, reddedildi (downgrade engeli)",
				"mevcut", a.Version, "manifest", m.Version)
		}
		if forced {
			fmt.Printf("Zaten guncel: %s\n", a.Version)
		}
		return // zaten guncel
	}

	key := runtime.GOOS + "/" + runtime.GOARCH
	// "+cli" anahtari: eski masaustu surumlerinin (<=0.2.4, AppKind kontrolu
	// yok) gomulu ajani duz "windows/amd64" anahtarini okuyup CLI'yi masaustu
	// exe'sinin uzerine yaziyordu; build-clients.sh o hedefi yalniz "+cli"
	// altinda yayinlar ki eski masaustleri onu hic gormesin.
	f, ok := m.Files[key+"+cli"]
	if !ok || f.Name == "" {
		f, ok = m.Files[key]
	}
	if !ok || f.Name == "" {
		a.Log.Warn("guncelleme: bu platform icin ikili yok", "platform", key, "surum", m.Version)
		return
	}
	a.Log.Info("guncelleme bulundu, indiriliyor",
		"mevcut", a.Version, "yeni", m.Version, "sebep", reason)

	data, err := a.download(ctx, "/bin/"+f.Name)
	if err != nil {
		a.Log.Warn("guncelleme: indirme basarisiz", "hata", err)
		if forced {
			a.lastUpdateErr = fmt.Errorf("indirme basarisiz: %w", err)
		}
		return
	}
	sum := sha256.Sum256(data)
	if got := hex.EncodeToString(sum[:]); got != f.SHA256 {
		a.Log.Warn("guncelleme: sha256 uyusmuyor, iptal", "beklenen", f.SHA256, "alinan", got)
		if forced {
			a.lastUpdateErr = fmt.Errorf("indirilen dosya dogrulanamadi (sha256)")
		}
		return
	}
	// selfupdate calisan ikiliyi guvenli sekilde degistirir (Windows'ta
	// rename-hilesi + hata halinde rollback).
	if err := selfupdate.Apply(bytes.NewReader(data), selfupdate.Options{}); err != nil {
		a.Log.Error("guncelleme: uygulanamadi", "hata", err)
		if forced {
			a.lastUpdateErr = fmt.Errorf("uygulanamadi (yonetici/root yetkisi gerekebilir): %w", err)
		}
		return
	}
	a.Log.Info("guncelleme uygulandi, yeniden baslatiliyor", "surum", m.Version)
	if forced {
		fmt.Printf("Guncellendi: %s -> %s\n", a.Version, m.Version)
		return // elle: komut biter, yeni ikili bir sonraki calistirmada kullanilir
	}
	a.restartSelf(exePath)
}

// errUpdateSignature, manifest imzasinin eksik/gecersiz oldugunu belirtir.
var errUpdateSignature = errors.New("guncelleme imzasi")

// fetchManifest, manifest.json ve manifest.json.sig dosyalarini indirir; imza
// gomulu acik anahtarlarla DOGRULANMADAN manifest ayristirilmaz. --insecure /
// ozel CA modunda da imza zorunludur (ulasim guvenligine guvenilmez).
func (a *Agent) fetchManifest(ctx context.Context) (updateManifest, error) {
	var m updateManifest
	body, err := a.fetchBody(ctx, "/bin/manifest.json")
	if err != nil {
		return m, err
	}
	sig, err := a.fetchBody(ctx, "/bin/manifest.json.sig")
	if err != nil {
		return m, fmt.Errorf("%w: imza dosyasi alinamadi: %v", errUpdateSignature, err)
	}
	if err := updatesig.Verify(body, sig, a.updateKeys); err != nil {
		return m, fmt.Errorf("%w: %s", errUpdateSignature, updatesig.Describe(err))
	}
	if err := json.Unmarshal(body, &m); err != nil {
		return m, err
	}
	return m, nil
}

func (a *Agent) fetchBody(ctx context.Context, path string) ([]byte, error) {
	cctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(cctx, http.MethodGet, a.httpBase()+path, nil)
	if err != nil {
		return nil, err
	}
	resp, err := a.updateHTTPClient().Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("%s http %d", path, resp.StatusCode)
	}
	return io.ReadAll(io.LimitReader(resp.Body, 1<<20))
}

func (a *Agent) download(ctx context.Context, path string) ([]byte, error) {
	cctx, cancel := context.WithTimeout(ctx, 5*time.Minute)
	defer cancel()
	req, err := http.NewRequestWithContext(cctx, http.MethodGet, a.httpBase()+path, nil)
	if err != nil {
		return nil, err
	}
	resp, err := a.updateHTTPClient().Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("indirme http %d", resp.StatusCode)
	}
	return io.ReadAll(io.LimitReader(resp.Body, 200<<20)) // 200 MB tavan
}
