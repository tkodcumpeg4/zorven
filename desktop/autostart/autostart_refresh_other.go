//go:build !windows

package autostart

// refresh: Windows disinda kayit yolu dosya icerigidir; Enable zaten guncel yazar.
func (m *Manager) refresh() error { return nil }
