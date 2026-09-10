//go:build darwin

package autostart

import (
	"fmt"
	"os"
	"path/filepath"
)

// plistLabel, LaunchAgent etiketi. Ters-DNS bicimi macOS'ta beklenen kalip.
const plistLabel = "com.rpshell.client"

func (m *Manager) plistPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("ev dizini bulunamadi: %w", err)
	}
	return filepath.Join(home, "Library", "LaunchAgents", plistLabel+".plist"), nil
}

func (m *Manager) enabled() (bool, error) {
	path, err := m.plistPath()
	if err != nil {
		return false, err
	}
	_, err = os.Stat(path)
	if os.IsNotExist(err) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("LaunchAgent okunamadi: %w", err)
	}
	return true, nil
}

func (m *Manager) enable() error {
	path, err := m.plistPath()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("LaunchAgents dizini olusturulamadi: %w", err)
	}

	// RunAtLoad: oturum acilisinda baslat.
	// KeepAlive YOK: kullanici uygulamayi kapatirsa geri acilmamali.
	plist := `<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
	<key>Label</key>
	<string>` + plistLabel + `</string>
	<key>ProgramArguments</key>
	<array>
		<string>` + m.ExecPath + `</string>
	</array>
	<key>RunAtLoad</key>
	<true/>
</dict>
</plist>
`
	if err := os.WriteFile(path, []byte(plist), 0o644); err != nil {
		return fmt.Errorf("LaunchAgent yazilamadi: %w", err)
	}
	return nil
}

func (m *Manager) disable() error {
	path, err := m.plistPath()
	if err != nil {
		return err
	}
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("LaunchAgent silinemedi: %w", err)
	}
	return nil
}
