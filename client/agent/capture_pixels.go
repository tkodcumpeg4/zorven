package agent

import (
	"errors"
	"fmt"
	"image"
)

// CGImage bit duzeni sabitleri (CGImage.h). Platformdan bagimsiz saf mantik
// oldugu icin macOS disinda da test edilebilir.
const (
	cgAlphaInfoMask      = 0x1f
	cgByteOrderMask      = 0x7000
	cgByteOrder32Little  = 2 << 12
	cgByteOrder32Big     = 4 << 12
	cgAlphaPremultLast   = 1
	cgAlphaPremultFirst  = 2
	cgAlphaLast          = 3
	cgAlphaFirst         = 4
	cgAlphaNoneSkipLast  = 5
	cgAlphaNoneSkipFirst = 6
)

// macPixelOrder, bitmapInfo'dan bellekteki bayt sirasini cikarir:
// bgra=true => B,G,R,A(X); false => R,G,B,A(X). 32 bit/piksel disi desteklenmez.
func macPixelOrder(bitmapInfo uint32, bitsPerPixel int) (bgra bool, err error) {
	if bitsPerPixel != 32 {
		return false, fmt.Errorf("desteklenmeyen piksel bicimi: %d bit/piksel", bitsPerPixel)
	}
	alpha := bitmapInfo & cgAlphaInfoMask
	order := bitmapInfo & cgByteOrderMask
	alphaFirst := alpha == cgAlphaPremultFirst || alpha == cgAlphaFirst || alpha == cgAlphaNoneSkipFirst
	alphaLast := alpha == cgAlphaPremultLast || alpha == cgAlphaLast || alpha == cgAlphaNoneSkipLast
	switch {
	case order == cgByteOrder32Little && alphaFirst:
		return true, nil // ARGB degeri little-endian => bellekte B,G,R,A
	case (order == cgByteOrder32Big || order == 0) && alphaLast:
		return false, nil // bellekte R,G,B,A
	}
	return false, fmt.Errorf("desteklenmeyen bitmapInfo 0x%x", bitmapInfo)
}

// macPixelsToRGBA, CGImage ham baytlarini (stride'li) sikisik *image.RGBA'ya
// cevirir. Ekran opaktir; alfa her zaman 255 yapilir (premultiplied/skip fark
// etmez). src, en az (height-1)*stride + width*4 bayt olmalidir.
func macPixelsToRGBA(src []byte, width, height, stride int, bgra bool) (*image.RGBA, error) {
	if width <= 0 || height <= 0 {
		return nil, errors.New("gecersiz goruntu boyutu")
	}
	if stride < width*4 {
		return nil, fmt.Errorf("gecersiz stride %d (genislik %d)", stride, width)
	}
	if need := (height-1)*stride + width*4; len(src) < need {
		return nil, fmt.Errorf("piksel verisi kisa: %d < %d", len(src), need)
	}
	img := image.NewRGBA(image.Rect(0, 0, width, height))
	for y := 0; y < height; y++ {
		s := src[y*stride : y*stride+width*4]
		d := img.Pix[y*img.Stride : y*img.Stride+width*4]
		if bgra {
			for x := 0; x < len(s); x += 4 {
				d[x], d[x+1], d[x+2], d[x+3] = s[x+2], s[x+1], s[x], 255
			}
		} else {
			for x := 0; x < len(s); x += 4 {
				d[x], d[x+1], d[x+2], d[x+3] = s[x], s[x+1], s[x+2], 255
			}
		}
	}
	return img, nil
}
