//go:build !darwin

package agent

import (
	"image"

	"github.com/kbinani/screenshot"
)

// Ekran yakalama indirgemesi: macOS disinda kbinani/screenshot kullanilir.
// macOS'ta kutuphane cgo ister (CGO_ENABLED=0'da 0 ekran bildirir); orada
// capture_darwin.go saf Go (purego) ile CoreGraphics'i cagirir.

func platNumDisplays() int { return screenshot.NumActiveDisplays() }

func platBounds(d int) image.Rectangle { return screenshot.GetDisplayBounds(d) }

func platCapture(d int) (*image.RGBA, error) {
	return screenshot.CaptureRect(screenshot.GetDisplayBounds(d))
}
