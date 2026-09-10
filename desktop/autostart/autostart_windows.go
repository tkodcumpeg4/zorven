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
		return false, fmt.Errorf("kayit defteri acilamadi: %w", err)
	}
	defer k.Close()

	if _, _, err := k.GetStringValue(AppName); err != nil {
		if errors.Is(err, registry.ErrNotExist) {
			return false, nil
		}
		return false, fmt.Errorf("kayit okunamadi: %w", err)
	}
	return true, nil
}

func (m *Manager) enable() error {
	k, _, err := registry.CreateKey(registry.CURRENT_USER, runKey, registry.SET_VALUE)
	if err != nil {
		return fmt.Errorf("kayit defteri anahtari olusturulamadi: %w", err)
	}
	defer k.Close()

	// Yol bosluk icerebilir (or. "C:\Program Files\..."), tirnak zorunlu.
	return k.SetStringValue(AppName, `"`+m.ExecPath+`"`)
}

func (m *Manager) disable() error {
	k, err := registry.OpenKey(registry.CURRENT_USER, runKey, registry.SET_VALUE)
	if err != nil {
		if errors.Is(err, registry.ErrNotExist) {
			return nil // kayit yoksa yapacak bir sey yok
		}
		return fmt.Errorf("kayit defteri acilamadi: %w", err)
	}
	defer k.Close()

	if err := k.DeleteValue(AppName); err != nil && !errors.Is(err, registry.ErrNotExist) {
		return fmt.Errorf("kayit silinemedi: %w", err)
	}
	return nil
}
