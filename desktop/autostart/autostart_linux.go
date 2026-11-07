//go:build linux

package autostart

import (
	"fmt"
	"os"
	"path/filepath"
)

// desktopPath, XDG Autostart spesifikasyonuna gore .desktop dosyasinin yolu.
// XDG_CONFIG_HOME ayarliysa ona saygi gosterilir.
func (m *Manager) desktopPath() (string, error) {
	base := os.Getenv("XDG_CONFIG_HOME")
	if base == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", fmt.Errorf("home directory not found: %w", err)
		}
		base = filepath.Join(home, ".config")
	}
	return filepath.Join(base, "autostart", AppName+".desktop"), nil
}

func (m *Manager) enabled() (bool, error) {
	path, err := m.desktopPath()
	if err != nil {
		return false, err
	}
	_, err = os.Stat(path)
	if os.IsNotExist(err) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("could not read autostart entry: %w", err)
	}
	return true, nil
}

func (m *Manager) enable() error {
	path, err := m.desktopPath()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("could not create autostart directory: %w", err)
	}

	// X-GNOME-Autostart-enabled: GNOME'un girdiyi yok saymamasi icin.
	entry := "[Desktop Entry]\n" +
		"Type=Application\n" +
		"Name=" + DisplayName + "\n" +
		"Exec=" + m.ExecPath + " --hidden\n" +
		"Terminal=false\n" +
		"X-GNOME-Autostart-enabled=true\n"

	if err := os.WriteFile(path, []byte(entry), 0o644); err != nil {
		return fmt.Errorf("could not write autostart entry: %w", err)
	}
	return nil
}

func (m *Manager) disable() error {
	path, err := m.desktopPath()
	if err != nil {
		return err
	}
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("could not remove autostart entry: %w", err)
	}
	return nil
}
