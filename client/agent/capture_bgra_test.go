package agent

import (
	"image"
	"math/rand"
	"testing"
)

// bgraToRGBA, bayt bayt referans donusumle ayni olmali (tek/cift sutun, stride'li, paralel yol).
func TestBGRAToRGBAMatchesReference(t *testing.T) {
	rng := rand.New(rand.NewSource(7))
	for _, d := range [][2]int{{1, 1}, {3, 5}, {17, 4}, {64, 33}, {1920, 1080}, {1001, 777}} {
		w, h := d[0], d[1]
		srcStride := w*4 + 8 // fazladan dolgu
		src := make([]byte, srcStride*h)
		rng.Read(src)
		dst := image.NewRGBA(image.Rect(0, 0, w, h))
		bgraToRGBA(dst.Pix, src, w, h, srcStride, dst.Stride)
		for y := 0; y < h; y++ {
			for x := 0; x < w; x++ {
				s := src[y*srcStride+x*4:]
				o := y*dst.Stride + x*4
				if dst.Pix[o] != s[2] || dst.Pix[o+1] != s[1] || dst.Pix[o+2] != s[0] || dst.Pix[o+3] != 255 {
					t.Fatalf("%dx%d (%d,%d): got %v want [%d %d %d 255]", w, h, x, y, dst.Pix[o:o+4], s[2], s[1], s[0])
				}
			}
		}
	}
}
