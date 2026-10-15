//go:build !windows && !linux

package agent

// Imlec katmani bu platformda yok (macOS: desteklenmiyor); Hello'da ilan edilmez.

func cursorSupported() bool { return false }

func newCursorSource() cursorSource { return nil }
