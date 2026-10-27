//go:build !windows

package agent

import "errors"

// Windows disinda H.264 kodlayici yok (CGO'suz saf Go ile uygun kodlayici yok):
// screen2_video ilan edilmez, karo modu kullanilir. Baska platformlar icin
// videoEncoder arayuzunu uygulayan bir fabrika buraya eklenebilir.
var errVideoUnsupported = errors.New("bu platformda H.264 kodlayici yok")

func platNewVideoEncoder(w, h, fps, bps int, preferSW bool) (videoEncoder, error) {
	return nil, errVideoUnsupported
}

// platVideoThreadInit, kodlama goroutine'inin baslangic kancasi (Windows'ta COM).
func platVideoThreadInit() {}
