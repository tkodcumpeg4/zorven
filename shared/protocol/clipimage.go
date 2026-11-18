package protocol

import (
	"bytes"
	"encoding/binary"
	"errors"
)

// Gorsel panosu (FeatureScreenClipboardImage). PNG, FrameClipImage ikili
// cerceveleriyle parcalanarak akar (WS okuma siniri 1 MiB oldugu icin tek
// cerceve 8 MiB tasiyamaz). Panel WS'inde tek ikili mesaj: ilk bayt
// PanelClipImageMarker, kalani PNG.

const (
	// MaxScreenClipboardImage, tek PNG'nin en buyuk bayt boyutu.
	MaxScreenClipboardImage = 8 << 20
	// ClipImageChunk, tek FrameClipImage yukunun en buyuk boyutu.
	ClipImageChunk = 256 << 10
	// MaxClipImagePixels, kabul edilen en buyuk gorsel (genislik*yukseklik).
	MaxClipImagePixels = 24_000_000
	// MaxClipImageDim, tek kenarin en buyuk degeri.
	MaxClipImageDim = 16384
	// PanelClipImageMarker, panel WS ikili mesajinin ilk baytidir (ekran kareleri
	// 6/9 ile baslar; cakismaz). Iki yonludur.
	PanelClipImageMarker byte = 0x20
)

var pngSig = []byte{0x89, 'P', 'N', 'G', '\r', '\n', 0x1a, '\n'}

// ErrClipImage, gecersiz/asiri buyuk pano gorselini bildirir.
var ErrClipImage = errors.New("pano gorseli gecersiz veya sinir asiyor")

// ClipImageInfo, PNG imzasini ve IHDR'i (genislik/yukseklik) dogrular; tam
// cozmeden pahali gorselleri reddetmeye yarar.
func ClipImageInfo(png []byte) (w, h int, err error) {
	if len(png) < 33 || len(png) > MaxScreenClipboardImage || !bytes.HasPrefix(png, pngSig) {
		return 0, 0, ErrClipImage
	}
	if string(png[12:16]) != "IHDR" {
		return 0, 0, ErrClipImage
	}
	uw, uh := binary.BigEndian.Uint32(png[16:20]), binary.BigEndian.Uint32(png[20:24])
	if uw == 0 || uh == 0 || uw > MaxClipImageDim || uh > MaxClipImageDim || uint64(uw)*uint64(uh) > MaxClipImagePixels {
		return 0, 0, ErrClipImage
	}
	return int(uw), int(uh), nil
}

// ClipImageOK, PNG'nin pano sinirlarina uydugunu soyler.
func ClipImageOK(png []byte) bool {
	_, _, err := ClipImageInfo(png)
	return err == nil
}

// SplitClipImage, PNG'yi ClipImageChunk'lik parcalara boler (bos girdi => nil).
func SplitClipImage(png []byte) [][]byte {
	var out [][]byte
	for len(png) > 0 {
		n := min(len(png), ClipImageChunk)
		out = append(out, png[:n])
		png = png[n:]
	}
	return out
}

// ClipImageAssembler, tek akisin parcalarini birlestirir. Sira bozulursa ya da
// sinir asilirsa o aktarim atilir (Add hata doner, durum sifirlanir). Seq 0 her
// zaman yeni aktarim baslatir. Es zamanli KULLANILMAZ.
type ClipImageAssembler struct {
	buf  []byte
	next uint32
	live bool
}

// Add, bir parcayi ekler; eof ise tamamlanmis PNG doner (done=true).
func (a *ClipImageAssembler) Add(seq uint32, eof bool, payload []byte) (png []byte, done bool, err error) {
	if seq == 0 {
		a.buf, a.next, a.live = a.buf[:0], 0, true
	}
	if !a.live || seq != a.next || len(payload) > ClipImageChunk || len(a.buf)+len(payload) > MaxScreenClipboardImage {
		a.Reset()
		return nil, false, ErrClipImage
	}
	a.buf = append(a.buf, payload...)
	a.next++
	if !eof {
		return nil, false, nil
	}
	out := a.buf
	a.buf, a.live = nil, false
	if !ClipImageOK(out) {
		return nil, false, ErrClipImage
	}
	return out, true, nil
}

// Reset, bekleyen aktarimi atar.
func (a *ClipImageAssembler) Reset() { a.buf, a.next, a.live = nil, 0, false }
