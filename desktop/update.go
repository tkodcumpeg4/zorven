package main

// Masaustu uygulamasinin kendi surum bilgisi ve guncellemesi.
//
// Gomulu ajanin oto-guncellemesi (client/agent/update.go) KULLANILMAZ: o, CLI
// ikilisini indirip calisan dosyanin yerine koyar; masaustunde bu Zorven.exe'yi
// CLI ile ezerdi. Bu yuzden masaustu ayri bir manifest (/bin/desktop.json)
// okur ve kendi ikilisini (Zorven-desktop-<os>-<arch>) uygular.

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
	"os/exec"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/minio/selfupdate"
	wailsrt "github.com/wailsapp/wails/v2/pkg/runtime"
)

// version, derleme sirasinda -ldflags "-X main.version=..." ile yazilir.
var version = "dev"

const desktopDownloadPage = "https://zorven.app/download"

type desktopManifest struct {
	Version string `json:"version"`
	Files   map[string]struct {
		Name   string `json:"name"`
		SHA256 string `json:"sha256"`
		Size   int64  `json:"size"`
	} `json:"files"`
}

// UpdateInfo, arayuze donen guncelleme durumu.
type UpdateInfo struct {
	Current   string `json:"current"`
	Latest    string `json:"latest"`
	Available bool   `json:"available"`
	CanApply  bool   `json:"can_apply"` // bu platform icin ikili var mi
	Error     string `json:"error,omitempty"`
}

// GetVersion, calisan uygulamanin surumu.
func (a *App) GetVersion() string { return version }

func (a *App) updateBase() string {
	a.mu.Lock()
	addr := a.cfg.ServerAddr
	a.mu.Unlock()
	if addr == "" || addr == "localhost:8443" {
		addr = "zorven.app:443"
	}
	return "https://" + strings.TrimSuffix(addr, ":443")
}

var updateHTTP = &http.Client{Timeout: 60 * time.Second}

func (a *App) fetchDesktopManifest(ctx context.Context) (desktopManifest, error) {
	var m desktopManifest
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, a.updateBase()+"/bin/desktop.json", nil)
	resp, err := updateHTTP.Do(req)
	if err != nil {
		return m, fmt.Errorf("sunucuya ulaşılamadı")
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return m, fmt.Errorf("sürüm bilgisi alınamadı (HTTP %d)", resp.StatusCode)
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&m); err != nil {
		return m, fmt.Errorf("sürüm bilgisi okunamadı")
	}
	return m, nil
}

// CheckForUpdate, sunucudaki en son masaustu surumunu sorgular.
func (a *App) CheckForUpdate() UpdateInfo {
	info := UpdateInfo{Current: version}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	m, err := a.fetchDesktopManifest(ctx)
	if err != nil {
		info.Error = err.Error()
		return info
	}
	info.Latest = m.Version
	info.Available = version != "dev" && newerVersion(m.Version, version)
	_, info.CanApply = m.Files[runtime.GOOS+"/"+runtime.GOARCH]
	return info
}

// InstallUpdate, yeni surumu indirir, sha256'yi dogrular, calisan ikilinin
// yerine koyar ve uygulamayi yeniden baslatir. Bos donerse yeniden baslatma
// basladi demektir; hata metni donerse calisan surum degismemistir.
func (a *App) InstallUpdate() string {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	m, err := a.fetchDesktopManifest(ctx)
	if err != nil {
		return err.Error()
	}
	f, ok := m.Files[runtime.GOOS+"/"+runtime.GOARCH]
	if !ok || f.Name == "" || f.SHA256 == "" {
		a.OpenBrowser(desktopDownloadPage)
		return "Bu platform için otomatik güncelleme yok; indirme sayfası açıldı."
	}
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, a.updateBase()+"/bin/"+f.Name, nil)
	resp, err := updateHTTP.Do(req)
	if err != nil {
		return "Güncelleme indirilemedi."
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Sprintf("Güncelleme indirilemedi (HTTP %d).", resp.StatusCode)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, 200<<20))
	if err != nil {
		return "Güncelleme indirilemedi."
	}
	if msg := verifyDownload(data, f.SHA256, f.Size); msg != "" {
		return msg
	}

	exe, err := os.Executable()
	if err != nil {
		return "Uygulama yolu bulunamadı."
	}
	if err := selfupdate.Apply(bytes.NewReader(data), selfupdate.Options{}); err != nil {
		// Genellikle kurulum klasorune yazma izni yoktur (or. Program Files).
		a.OpenBrowser(desktopDownloadPage)
		return "Güncelleme uygulanamadı (yazma izni olmayabilir); indirme sayfası açıldı."
	}

	// Baglantiyi temiz kapat, yeni surumu baslat, bu process'i sonlandir.
	a.Disconnect()
	if err := exec.Command(exe).Start(); err != nil {
		return "Güncelleme kuruldu; uygulamayı elle yeniden başlatın."
	}
	go func() {
		time.Sleep(300 * time.Millisecond)
		if a.ctx != nil {
			wailsrt.Quit(a.ctx)
		}
		os.Exit(0)
	}()
	return ""
}

// verifyDownload, indirilen verinin sha256'sini, (verildiyse) boyutunu ve
// Windows'ta calistirilabilir (MZ) olup olmadigini denetler. Bos donerse
// dosya uygulanabilir; aksi halde kullaniciya gosterilecek hata metni.
func verifyDownload(data []byte, wantSHA string, wantSize int64) string {
	sum := sha256.Sum256(data)
	if !strings.EqualFold(hex.EncodeToString(sum[:]), wantSHA) {
		return "İndirilen dosya doğrulanamadı (sha256 uyuşmuyor); güncelleme uygulanmadı."
	}
	if wantSize > 0 && int64(len(data)) != wantSize {
		return "İndirilen dosya boyutu beklenenle uyuşmuyor; güncelleme uygulanmadı."
	}
	if runtime.GOOS == "windows" && !bytes.HasPrefix(data, []byte("MZ")) {
		return "İndirilen dosya geçerli bir uygulama değil; güncelleme uygulanmadı."
	}
	return ""
}

// newerVersion, a > b ise true ("0.2.0" > "0.1.6"; "v" oneki ve on-surum
// eki yok sayilir). Sayisal olmayan parca 0 sayilir.
func newerVersion(a, b string) bool {
	pa, pb := versionParts(a), versionParts(b)
	for i := 0; i < 3; i++ {
		if pa[i] != pb[i] {
			return pa[i] > pb[i]
		}
	}
	return false
}

func versionParts(v string) [3]int {
	v = strings.TrimPrefix(strings.TrimSpace(v), "v")
	if i := strings.IndexAny(v, "-+"); i >= 0 {
		v = v[:i]
	}
	var out [3]int
	for i, p := range strings.SplitN(v, ".", 3) {
		n, _ := strconv.Atoi(p)
		out[i] = n
	}
	return out
}
