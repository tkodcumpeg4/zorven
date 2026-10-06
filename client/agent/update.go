package agent

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"runtime"
	"time"

	"github.com/minio/selfupdate"
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
	a.maybeUpdate(ctx, "elle")
	return nil
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
	if a.NoAutoUpdate || !a.settings.autoUpdateEnabled() || a.Version == "" || a.Version == "dev" {
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
		a.Log.Debug("guncelleme: manifest alinamadi", "hata", err)
		return
	}
	if m.Version == "" || m.Version == a.Version {
		return // zaten guncel
	}

	key := runtime.GOOS + "/" + runtime.GOARCH
	f, ok := m.Files[key]
	if !ok || f.Name == "" {
		a.Log.Warn("guncelleme: bu platform icin ikili yok", "platform", key, "surum", m.Version)
		return
	}
	a.Log.Info("guncelleme bulundu, indiriliyor",
		"mevcut", a.Version, "yeni", m.Version, "sebep", reason)

	data, err := a.download(ctx, "/bin/"+f.Name)
	if err != nil {
		a.Log.Warn("guncelleme: indirme basarisiz", "hata", err)
		return
	}
	sum := sha256.Sum256(data)
	if got := hex.EncodeToString(sum[:]); got != f.SHA256 {
		a.Log.Warn("guncelleme: sha256 uyusmuyor, iptal", "beklenen", f.SHA256, "alinan", got)
		return
	}
	// selfupdate calisan ikiliyi guvenli sekilde degistirir (Windows'ta
	// rename-hilesi + hata halinde rollback).
	if err := selfupdate.Apply(bytes.NewReader(data), selfupdate.Options{}); err != nil {
		a.Log.Error("guncelleme: uygulanamadi", "hata", err)
		return
	}
	a.Log.Info("guncelleme uygulandi, yeniden baslatiliyor", "surum", m.Version)
	a.restartSelf(exePath)
}

func (a *Agent) fetchManifest(ctx context.Context) (updateManifest, error) {
	var m updateManifest
	cctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(cctx, http.MethodGet, a.httpBase()+"/bin/manifest.json", nil)
	if err != nil {
		return m, err
	}
	resp, err := a.updateHTTPClient().Do(req)
	if err != nil {
		return m, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return m, fmt.Errorf("manifest http %d", resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return m, err
	}
	if err := json.Unmarshal(body, &m); err != nil {
		return m, err
	}
	return m, nil
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
