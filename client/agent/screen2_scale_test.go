package agent

import (
	"bytes"
	"image"
	"math"
	"math/rand"
	"testing"

	"golang.org/x/image/draw"
)

func TestScaleAxisWeightsSum256(t *testing.T) {
	for _, c := range [][2]int{{1920, 1600}, {3840, 1600}, {1080, 900}, {2560, 1280}, {1921, 1600}, {7680, 1600}, {1000, 999}} {
		a := newScaleAxis(c[0], c[1])
		for i := 0; i < c[1]; i++ {
			sum := 0
			n := int(a.cnt[i])
			if n == 0 || n > a.taps {
				t.Fatalf("%v: i=%d tap=%d", c, i, n)
			}
			if int(a.start[i])+n > c[0] {
				t.Fatalf("%v: i=%d kaynak disina tasti", c, i)
			}
			for k := 0; k < n; k++ {
				sum += int(a.w[i*a.taps+k])
			}
			if sum != 256 {
				t.Fatalf("%v: i=%d toplam=%d", c, i, sum)
			}
		}
	}
}

func TestScaleSolidAndSize(t *testing.T) {
	src := image.NewRGBA(image.Rect(0, 0, 1920, 1080))
	fill(src, src.Rect, 10, 200, 99)
	dst := scaleRGBA(src, 1600)
	if dst.Rect.Dx() != 1600 || dst.Rect.Dy() != 900 {
		t.Fatalf("boyut %v", dst.Rect)
	}
	for i := 0; i < len(dst.Pix); i += 4 {
		if dst.Pix[i] != 10 || dst.Pix[i+1] != 200 || dst.Pix[i+2] != 99 || dst.Pix[i+3] != 255 {
			t.Fatalf("duz renk bozuldu @%d: %v", i, dst.Pix[i:i+4])
		}
	}
	// kucuk kare: ayni goruntu
	small := image.NewRGBA(image.Rect(0, 0, 800, 600))
	if scaleRGBA(small, 1600) != small {
		t.Fatal("kucuk goruntu oldugu gibi donmeli")
	}
}

// Yeni olcekleyici, eski ApproxBiLinear'a yakin kalitede olmali (PSNR).
func TestScaleQualityVsBilinear(t *testing.T) {
	src := synthDesktop(1920, 1080, rand.New(rand.NewSource(3)))
	got := scaleRGBA(src, 1600)
	ref := image.NewRGBA(image.Rect(0, 0, 1600, 900))
	draw.ApproxBiLinear.Scale(ref, ref.Bounds(), src, src.Bounds(), draw.Src, nil)
	var se float64
	n := 0
	for i := 0; i < len(got.Pix); i += 4 {
		for c := 0; c < 3; c++ {
			d := float64(got.Pix[i+c]) - float64(ref.Pix[i+c])
			se += d * d
			n++
		}
	}
	psnr := 10 * math.Log10(255*255/(se/float64(n)+1e-9))
	t.Logf("PSNR vs ApproxBiLinear: %.1f dB", psnr)
	if psnr < 28 {
		t.Fatalf("PSNR dusuk: %.1f", psnr)
	}
}

// Artimli olcekleme, tam olceklemeyle BIT BIT ayni olmali.
func TestScaleIncrementalMatchesFull(t *testing.T) {
	rng := rand.New(rand.NewSource(5))
	for _, dims := range [][3]int{{1920, 1080, 1600}, {1366, 768, 1000}, {3840, 2160, 1600}, {1923, 1081, 1600}} {
		var fs frameScaler
		src := synthDesktop(dims[0], dims[1], rng)
		fs.Scale(src, dims[2])
		for iter := 0; iter < 6; iter++ {
			next := image.NewRGBA(src.Rect)
			copy(next.Pix, src.Pix)
			// rastgele kucuk/orta dikdortgenler (kenarlar dahil)
			for k := 0; k < 1+rng.Intn(3); k++ {
				x0, y0 := rng.Intn(dims[0]-1), rng.Intn(dims[1]-1)
				r := image.Rect(x0, y0, min(x0+1+rng.Intn(300), dims[0]), min(y0+1+rng.Intn(200), dims[1]))
				fill(next, r, uint8(rng.Intn(256)), uint8(rng.Intn(256)), uint8(rng.Intn(256)))
			}
			if iter == 3 { // sag-alt kosesi
				fill(next, image.Rect(dims[0]-3, dims[1]-3, dims[0], dims[1]), 1, 2, 3)
			}
			inc := fs.Scale(next, dims[2])
			full := scaleRGBA(next, dims[2])
			if !bytes.Equal(inc.Pix, full.Pix) {
				t.Fatalf("%v iter %d: artimli sonuc tam olceklemeden farkli", dims, iter)
			}
			src = next
		}
	}
}

func BenchmarkScaleIncrementalSmall(b *testing.B) {
	var fs frameScaler
	a := synthDesktop(1920, 1080, rand.New(rand.NewSource(1)))
	fs.Scale(a, 1600)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		n := image.NewRGBA(a.Rect)
		copy(n.Pix, a.Pix)
		fill(n, image.Rect(300, 300, 340, 316), uint8(i), 9, 9)
		fs.Scale(n, 1600)
		a = n
	}
}
