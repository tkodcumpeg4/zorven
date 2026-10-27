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
	"crypto"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"time"

	"github.com/minio/selfupdate"
	"github.com/tkodcumpeg4/zorven/shared/updatesig"
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
	body, err := fetchUpdateFile(ctx, a.updateBase()+"/bin/desktop.json")
	if err != nil {
		return desktopManifest{}, err
	}
	sig, err := fetchUpdateFile(ctx, a.updateBase()+"/bin/desktop.json.sig")
	if err != nil {
		return desktopManifest{}, errors.New(tm("upd.sigFail"))
	}
	return parseSignedManifest(body, sig, nil)
}

// parseSignedManifest, desktop.json baytlarini imza dogrulandiktan SONRA
// ayristirir (keys nil ise gomulu acik anahtarlar).
func parseSignedManifest(body, sig []byte, keys []string) (desktopManifest, error) {
	var m desktopManifest
	if err := updatesig.Verify(body, sig, keys); err != nil {
		return m, errors.New(tm("upd.notApplied", updatesig.Describe(err)))
	}
	if err := json.Unmarshal(body, &m); err != nil {
		return m, errors.New(tm("upd.readFail"))
	}
	return m, nil
}

func fetchUpdateFile(ctx context.Context, url string) ([]byte, error) {
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	resp, err := updateHTTP.Do(req)
	if err != nil {
		return nil, errors.New(tm("upd.unreachable"))
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, errors.New(tm("upd.httpFail", resp.StatusCode))
	}
	return io.ReadAll(io.LimitReader(resp.Body, 1<<20))
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
	// Downgrade / ayni surum engeli (imzali olsa bile eski manifest tekrar sunulabilir).
	if !newerVersion(m.Version, version) {
		return tm("upd.latest")
	}
	f, ok := m.Files[runtime.GOOS+"/"+runtime.GOARCH]
	if !ok || f.Name == "" || f.SHA256 == "" {
		a.OpenBrowser(desktopDownloadPage)
		return tm("upd.noPlatform")
	}
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, a.updateBase()+"/bin/"+f.Name, nil)
	resp, err := updateHTTP.Do(req)
	if err != nil {
		return tm("upd.dlFail")
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return tm("upd.dlFailHTTP", resp.StatusCode)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, 200<<20))
	if err != nil {
		return tm("upd.dlFail")
	}
	if msg := verifyDownload(data, f.SHA256, f.Size); msg != "" {
		return msg
	}

	exe, err := os.Executable()
	if err != nil {
		return tm("upd.noExe")
	}
	if err := selfupdate.Apply(bytes.NewReader(data), selfupdate.Options{}); err != nil {
		// Genellikle kurulum klasorune yazma izni yoktur (or. Program Files).
		// Indirme sayfasina atmak yerine yonetici onayiyla ayni ikiliye
		// uygulatmayi dene; o da olmazsa nedeniyle birlikte sayfayi ac.
		if runtime.GOOS == "windows" && !processElevated() {
			return a.applyUpdateElevated(exe, data, f.SHA256)
		}
		a.OpenBrowser(desktopDownloadPage)
		return tm("upd.applyFail", err.Error())
	}

	// Baglantiyi temiz kapat, yeni surumu baslat, bu process'i sonlandir.
	a.Disconnect()
	if err := exec.Command(exe).Start(); err != nil {
		return tm("upd.restartHand")
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

// updateApplyFlag, yukseltilmis yardimci kipini secer:
//
//	Zorven.exe --zv-apply-update <indirilen-dosya> <sha256>
//
// Calisan (kurulu, yalniz yoneticinin yazabildigi) ikili UAC ile bu kipte
// acilir; dosyayi bellege okuyup sha256'yi YENIDEN dogrular, kendi yerine
// koyar ve yeni surumu baslatir. Gecici dosya kullanici yazilabilir oldugu
// icin dogrulama yukseltilmis surecte yapilir (arada degistirilirse reddedilir).
const updateApplyFlag = "--zv-apply-update"

// applyUpdateElevated, dogrulanmis ikiliyi gecici dosyaya yazar ve kurulu
// ikiliyi yonetici olarak yardimci kipte baslatir. Bos donerse yardimci
// basladi ve bu surec kapanir.
func (a *App) applyUpdateElevated(exe string, data []byte, sha string) string {
	tmp, err := os.CreateTemp("", "zorven-update-*.exe")
	if err != nil {
		return tm("upd.tmpFail")
	}
	_, werr := tmp.Write(data)
	cerr := tmp.Close()
	if werr != nil || cerr != nil {
		_ = os.Remove(tmp.Name())
		return tm("upd.tmpFail")
	}
	params := fmt.Sprintf("%s \"%s\" %s", updateApplyFlag, tmp.Name(), sha)
	if err := runElevated(exe, params); err != nil {
		_ = os.Remove(tmp.Name())
		return tm("upd.needAdmin", err.Error())
	}
	a.Disconnect()
	go func() {
		time.Sleep(300 * time.Millisecond)
		if a.ctx != nil {
			wailsrt.Quit(a.ctx)
		}
		os.Exit(0)
	}()
	return ""
}

// runApplyUpdate, yardimci kip: args = [dosya, sha256]. Basarida yeni surumu
// baslatir. Cikis kodu dondurur.
func runApplyUpdate(args []string) int {
	if len(args) != 2 {
		return 2
	}
	path, sha := args[0], args[1]
	defer os.Remove(path)
	data, err := os.ReadFile(path)
	if err != nil || verifyDownload(data, sha, 0) != "" {
		return 3
	}
	sum, _ := hex.DecodeString(sha)
	exe, err := os.Executable()
	if err != nil {
		return 4
	}
	// Eski (kullanici) sureci kapanana kadar kisa bir sure tanir; Windows'ta
	// calisan ikili yeniden adlandirilabildigi icin Apply yine de calisir.
	time.Sleep(700 * time.Millisecond)
	if err := selfupdate.Apply(bytes.NewReader(data), selfupdate.Options{
		TargetPath: exe, Checksum: sum, Hash: crypto.SHA256,
	}); err != nil {
		return 5
	}
	if err := exec.Command(exe).Start(); err != nil {
		return 6
	}
	return 0
}

// verifyDownload, indirilen verinin sha256'sini, (verildiyse) boyutunu ve
// Windows'ta calistirilabilir (MZ) olup olmadigini denetler. Bos donerse
// dosya uygulanabilir; aksi halde kullaniciya gosterilecek hata metni.
func verifyDownload(data []byte, wantSHA string, wantSize int64) string {
	sum := sha256.Sum256(data)
	if !strings.EqualFold(hex.EncodeToString(sum[:]), wantSHA) {
		return tm("upd.shaMismatch")
	}
	if wantSize > 0 && int64(len(data)) != wantSize {
		return tm("upd.sizeMismatch")
	}
	if runtime.GOOS == "windows" && !bytes.HasPrefix(data, []byte("MZ")) {
		return tm("upd.notExe")
	}
	return ""
}

// newerVersion, a > b ise true (ortak updatesig.Newer).
func newerVersion(a, b string) bool { return updatesig.Newer(a, b) }
