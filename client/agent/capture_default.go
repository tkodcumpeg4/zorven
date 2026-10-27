//go:build !darwin && !windows

package agent

import (
	"image"

	"github.com/kbinani/screenshot"
)

// Ekran yakalama indirgemesi: macOS ve Windows disinda kbinani/screenshot
// kullanilir. macOS'ta kutuphane cgo ister (CGO_ENABLED=0'da 0 ekran bildirir);
// orada capture_darwin.go saf Go (purego) ile CoreGraphics'i cagirir.
// Windows'ta capture_windows.go kalici DC+DIB ile yakalar (kbinani geri dusus).

func platNumDisplays() int { return screenshot.NumActiveDisplays() }

func platBounds(d int) image.Rectangle { return screenshot.GetDisplayBounds(d) }

// platMethod, yakalama yontemi adi (canli olcum panelinde gosterilir).
func platMethod() string { return "kbinani" }

// newPlatCapturer, oturum icin yakalayici olusturur.
func newPlatCapturer() Capturer { return goCapturer{} }

func platCapture(d int) (*image.RGBA, error) {
	return screenshot.CaptureRect(screenshot.GetDisplayBounds(d))
}
