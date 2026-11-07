//go:build windows

package autostart

import (
	"errors"
	"fmt"

	"golang.org/x/sys/windows/registry"
)

// runKey, kullanici oturum acinca calistirilacak programlarin kaydi.
// HKEY_CURRENT_USER kullaniliyor: yonetici yetkisi gerektirmez ve yalnizca
// bu kullaniciyi etkiler.
const runKey = `Software\Microsoft\Windows\CurrentVersion\Run`

func (m *Manager) enabled() (bool, error) {
	k, err := registry.OpenKey(registry.CURRENT_USER, runKey, registry.QUERY_VALUE)
	if err != nil {
		if errors.Is(err, registry.ErrNotExist) {
			return false, nil
		}
		return false, fmt.Errorf("could not open registry key: %w", err)
	}
	defer k.Close()

	if _, _, err := k.GetStringValue(AppName); err != nil {
		if errors.Is(err, registry.ErrNotExist) {
			return false, nil
		}
		return false, fmt.Errorf("could not read registry value: %w", err)
	}
	return true, nil
}

func (m *Manager) enable() error {
	k, _, err := registry.CreateKey(registry.CURRENT_USER, runKey, registry.SET_VALUE)
	if err != nil {
		return fmt.Errorf("could not create registry key: %w", err)
	}
	defer k.Close()

	return k.SetStringValue(AppName, m.command())
}

// command, Run kaydina yazilacak komut satiri. Yol bosluk icerebilir (or.
// "C:\Program Files\..."), tirnak zorunlu. --hidden: oturum acilisinda pencere
// acmadan tepsiye baslar.
func (m *Manager) command() string {
	return `"` + m.ExecPath + `" --hidden`
}

// refresh, kayit varsa ama farkli bir yolu/komutu gosteriyorsa (uygulama
// tasindi ya da guncellendi) guncel komutla yeniden yazar; kayit yoksa dokunmaz.
func (m *Manager) refresh() error {
	k, err := registry.OpenKey(registry.CURRENT_USER, runKey, registry.QUERY_VALUE|registry.SET_VALUE)
	if err != nil {
		if errors.Is(err, registry.ErrNotExist) {
			return nil
		}
		return fmt.Errorf("could not open registry key: %w", err)
	}
	defer k.Close()
	cur, _, err := k.GetStringValue(AppName)
	if err != nil {
		return nil // kayit yok: otomatik baslatma kapali
	}
	if cur == m.command() {
		return nil
	}
	return k.SetStringValue(AppName, m.command())
}

func (m *Manager) disable() error {
	k, err := registry.OpenKey(registry.CURRENT_USER, runKey, registry.SET_VALUE)
	if err != nil {
		if errors.Is(err, registry.ErrNotExist) {
			return nil // kayit yoksa yapacak bir sey yok
		}
		return fmt.Errorf("could not open registry key: %w", err)
	}
	defer k.Close()

	if err := k.DeleteValue(AppName); err != nil && !errors.Is(err, registry.ErrNotExist) {
		return fmt.Errorf("could not delete registry value: %w", err)
	}
	return nil
}
