package agent

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
)

// Windows Mod B (W3) icin wintun.dll dagitimi. wintun.dll (WireGuard LLC, imzali,
// degistirilmemis) zorven.exe ile BIRLIKTE GOMULMEZ; yayin zamaninda
// scripts/fetch-wintun.sh ile indirilip sunucuda /bin/wintun-<arch>.dll olarak
// servis edilir ve IMZALI manifest.json'a ("windows/<arch>+wintun") sha256 ile
// eklenir. Istemci DLL yoksa ayni sunucudan, ayni imza dogrulama yoluyla
// (fetchManifest) indirip zorven.exe'nin yanina koyar. Lisans metni de
// (wintun-LICENSE.txt) yanina yazilir.

const (
	wintunDLLName     = "wintun.dll"
	wintunLicenseFile = "wintun-LICENSE.txt"
	wintunMaxDLL      = 4 << 20
	wintunMaxLicense  = 64 << 10
)

var wintunServedRe = regexp.MustCompile(`^wintun-(amd64|arm64)\.dll$`)

// WintunPath, bu ikilinin yanindaki wintun.dll yolu ("" = ikili yolu bulunamadi).
func WintunPath() string {
	exe, err := os.Executable()
	if err != nil {
		return ""
	}
	return filepath.Join(filepath.Dir(exe), wintunDLLName)
}

// EnsureWintun, Windows'ta wintun.dll yoksa sunucudan indirip dogrular ve
// zorven.exe'nin yanina koyar. Diger platformlarda hicbir sey yapmaz. Yazma
// yonetici/SYSTEM yetkisi ister (Program Files).
func (a *Agent) EnsureWintun(ctx context.Context) error {
	if runtime.GOOS != "windows" {
		return nil
	}
	p := WintunPath()
	if p == "" {
		return fmt.Errorf("zorven.exe yolu bulunamadi; wintun.dll yerlestirilemedi")
	}
	return a.ensureWintunIn(ctx, filepath.Dir(p), runtime.GOOS, runtime.GOARCH, verifyAuthenticode)
}

// ensureWintunIn, dir icine dogrulanmis wintun.dll koyar (testlenebilir cekirdek).
// verify, indirilen dosyanin (gecici yol) ek dogrulamasidir (Windows: Authenticode).
func (a *Agent) ensureWintunIn(ctx context.Context, dir, goos, goarch string, verify func(path string) error) error {
	dst := filepath.Join(dir, wintunDLLName)
	if fi, err := os.Stat(dst); err == nil && !fi.IsDir() {
		return nil // mevcut (kurulum veya kullanici koydu)
	}
	if err := a.setupTLS(); err != nil {
		return err
	}
	m, err := a.fetchManifest(ctx)
	if err != nil {
		return fmt.Errorf("wintun.dll indirilemedi: surum bilgisi/imza dogrulanamadi: %w", err)
	}
	f, ok := m.Files[goos+"/"+goarch+"+wintun"]
	if !ok || f.Name == "" || f.SHA256 == "" {
		return fmt.Errorf("wintun.dll bu sunucuda %s/%s icin yayinlanmamis; wintun.dll'i (wintun.net, imzali surum) zorven.exe ile ayni klasore elle koyun", goos, goarch)
	}
	if !wintunServedRe.MatchString(f.Name) {
		return fmt.Errorf("manifestteki wintun dosya adi gecersiz: %q", f.Name)
	}
	data, err := a.downloadMax(ctx, "/bin/"+f.Name, wintunMaxDLL)
	if err != nil {
		return fmt.Errorf("wintun.dll indirilemedi: %w", err)
	}
	if err := checkSHA(data, f.SHA256); err != nil {
		return fmt.Errorf("wintun.dll reddedildi: %w", err)
	}
	// Lisans metni (varsa) ayni yolla; bildirim korunsun.
	var lic []byte
	if lf, ok := m.Files["any+wintun-license"]; ok && lf.Name == wintunLicenseFile {
		b, err := a.downloadMax(ctx, "/bin/"+lf.Name, wintunMaxLicense)
		if err == nil && checkSHA(b, lf.SHA256) == nil {
			lic = b
		}
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("wintun.dll yazilamadi (%s): %w", dir, err)
	}
	tmp, err := os.CreateTemp(dir, "wintun-*.tmp")
	if err != nil {
		return fmt.Errorf("wintun.dll yazilamadi (%s): yonetici yetkisi gerekli olabilir: %w", dir, err)
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return fmt.Errorf("wintun.dll yazilamadi: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("wintun.dll yazilamadi: %w", err)
	}
	if verify != nil {
		if err := verify(tmpName); err != nil {
			return fmt.Errorf("wintun.dll imzasi (Authenticode) dogrulanamadi: %w", err)
		}
	}
	if err := os.Rename(tmpName, dst); err != nil {
		return fmt.Errorf("wintun.dll yerlestirilemedi: %w", err)
	}
	if lic != nil {
		_ = os.WriteFile(filepath.Join(dir, wintunLicenseFile), lic, 0o644)
	}
	a.Log.Info("wintun.dll kuruldu", "yol", dst, "surum", m.Version)
	return nil
}

func checkSHA(data []byte, want string) error {
	sum := sha256.Sum256(data)
	if got := hex.EncodeToString(sum[:]); got != want {
		return fmt.Errorf("sha256 uyusmuyor (beklenen %s, alinan %s)", want, got)
	}
	return nil
}
