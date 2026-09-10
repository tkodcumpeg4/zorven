// Package autostart, uygulamanin bilgisayar acilisinda otomatik baslamasini
// yonetir.
//
// Her isletim sisteminin mekanizmasi farklidir ve harici bagimlilik
// istemedigimiz icin ucu de elle uygulandi:
//
//	Windows : HKCU\Software\Microsoft\Windows\CurrentVersion\Run kayit defteri
//	macOS   : ~/Library/LaunchAgents/<label>.plist
//	Linux   : ~/.config/autostart/<name>.desktop (XDG Autostart)
//
// Ucu de KULLANICI duzeyinde calisir — yonetici/root yetkisi GEREKMEZ.
// Sistem geneli kurulum bilincli olarak desteklenmiyor: bu bir kullanici
// uygulamasi, servis degil.
package autostart

import (
	"fmt"
	"os"
	"path/filepath"
)

// AppName, autostart kaydinda kullanilan ad. Bosluk icermemeli.
const AppName = "zorven"

// DisplayName, kullaniciya gorunen ad.
const DisplayName = "Zorven"

// Manager, autostart durumunu okur ve degistirir.
type Manager struct {
	// ExecPath, kayda yazilacak calistirilabilir dosya yolu.
	ExecPath string
}

// New, calisan programin yoluyla bir Manager doner.
func New() (*Manager, error) {
	exe, err := os.Executable()
	if err != nil {
		return nil, fmt.Errorf("calistirilabilir dosya yolu bulunamadi: %w", err)
	}
	// Sembolik baglantilari coz: aksi halde kayit, tasinabilir bir bagin
	// arkasindaki gecici yola isaret edebilir.
	if resolved, err := filepath.EvalSymlinks(exe); err == nil {
		exe = resolved
	}
	return &Manager{ExecPath: exe}, nil
}

// Enabled, autostart'in acik olup olmadigini doner.
func (m *Manager) Enabled() (bool, error) { return m.enabled() }

// Enable, acilista otomatik baslatmayi acar. Zaten aciksa hata vermez.
func (m *Manager) Enable() error { return m.enable() }

// Disable, otomatik baslatmayi kapatir. Zaten kapaliysa hata vermez.
func (m *Manager) Disable() error { return m.disable() }

// Set, istenen duruma getirir.
func (m *Manager) Set(on bool) error {
	if on {
		return m.Enable()
	}
	return m.Disable()
}
