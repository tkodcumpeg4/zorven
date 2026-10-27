package agent

import (
	"fmt"
	"image"
	"math/rand"
	"os"
	"runtime"
	"sort"
	"testing"
	"time"

	"github.com/tkodcumpeg4/zorven/shared/protocol"
)

// Gercekci 1920x1080 sentetik masaustu: gradyan duvar kagidi, duz renkli
// pencere/gorev cubugu, metin benzeri iki renkli bloklar. Sahneler: kucuk
// degisiklik (yazma), pencere surukleme (%20 alan), tam ekran degisimi.

func synthDesktop(w, h int, rng *rand.Rand) *image.RGBA {
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		row := img.Pix[y*img.Stride : y*img.Stride+w*4]
		for x := 0; x < w; x++ {
			o := x * 4
			row[o] = uint8(20 + 60*x/w)
			row[o+1] = uint8(40 + 80*y/h)
			row[o+2] = uint8(120 + 100*(x+y)/(w+h))
			row[o+3] = 255
		}
	}
	fill(img, image.Rect(0, h-48, w, h), 32, 32, 36)
	drawWindow(img, image.Rect(120, 80, 120+880, 80+470), rng)
	return img
}

// drawWindow: acik zemin + baslik cubugu + metin benzeri bloklar.
func drawWindow(img *image.RGBA, r image.Rectangle, rng *rand.Rand) {
	fill(img, r, 250, 250, 250)
	fill(img, image.Rect(r.Min.X, r.Min.Y, r.Max.X, r.Min.Y+32), 60, 90, 160)
	for y := r.Min.Y + 48; y+14 < r.Max.Y-8; y += 20 {
		for x := r.Min.X + 16; x+8 < r.Max.X-16; x += 10 {
			if rng.Intn(3) != 0 {
				fill(img, image.Rect(x, y, x+7, y+12), 30, 30, 30)
			}
		}
	}
}

type benchScene struct {
	name string
	// gen, i. kareyi uretir (her cagri yeni goruntu; yakalayici sozlesmesi).
	gen func(i int) *image.RGBA
}

func benchScenes() []benchScene {
	const w, h = 1920, 1080
	base := synthDesktop(w, h, rand.New(rand.NewSource(1)))
	clone := func(src *image.RGBA) *image.RGBA {
		c := image.NewRGBA(src.Rect)
		copy(c.Pix, src.Pix)
		return c
	}
	return []benchScene{
		{"kucuk-degisiklik", func(i int) *image.RGBA {
			c := clone(base)
			fill(c, image.Rect(300+(i%20)*8, 300, 340+(i%20)*8, 316), uint8(i), 9, 9)
			return c
		}},
		{"pencere-surukleme", func(i int) *image.RGBA {
			c := clone(base)
			x := 120 + (i%40)*24
			y := 80 + (i%20)*14
			drawWindow(c, image.Rect(x, y, x+880, y+470), rand.New(rand.NewSource(2)))
			return c
		}},
		{"tam-ekran", func(i int) *image.RGBA {
			// her piksel degisir: gradyan fazi kayar + pencere metni yenilenir
			c := synthDesktop(w, h, rand.New(rand.NewSource(int64(10+i))))
			for k := 0; k < len(c.Pix); k += 4 {
				c.Pix[k] += uint8(i * 7)
			}
			return c
		}},
	}
}

// stageTimes, bir sahnenin asama basina ortalama suresi.
type stageTimes struct {
	scale, diff, encode, pack, total time.Duration
	bytes, rects                     int
}

// measureScene, sirali (tek is parcacikli) yolun asamalarini n kare icin olcer.
func measureScene(sc benchScene, n, workers int) stageTimes {
	enc := newGoTileEncoder()
	var fs frameScaler
	frames := make([]*image.RGBA, n+1)
	for i := range frames {
		frames[i] = sc.gen(i)
	}
	prev := fs.Scale(frames[0], defaultMaxWidth)
	var t stageTimes
	for i := 1; i <= n; i++ {
		t0 := time.Now()
		cur := fs.Scale(frames[i], defaultMaxWidth)
		t1 := time.Now()
		dirty, tx, ty := diffTiles(prev, cur)
		var rects []image.Rectangle
		for _, r := range mergeDirty(dirty, tx, ty, cur.Rect.Dx(), cur.Rect.Dy()) {
			rects = append(rects, splitRect(r, screen2MaxRectDim)...)
		}
		t2 := time.Now()
		wire, _ := encodeRectsParallel(enc, cur, rects, 70, workers)
		t3 := time.Now()
		msgs, _ := protocol.PackScreen2(1, uint32(i), false, uint16(cur.Rect.Dx()), uint16(cur.Rect.Dy()), wire, 0)
		t4 := time.Now()
		for _, m := range msgs {
			t.bytes += len(m)
		}
		t.rects += len(rects)
		t.scale += t1.Sub(t0)
		t.diff += t2.Sub(t1)
		t.encode += t3.Sub(t2)
		t.pack += t4.Sub(t3)
		t.total += t4.Sub(t0)
		prev = cur
	}
	d := time.Duration(n)
	t.scale /= d
	t.diff /= d
	t.encode /= d
	t.pack /= d
	t.total /= d
	t.bytes /= n
	t.rects /= n
	return t
}

// TestScreen2StageReport, ZORVEN_S2_REPORT=1 ile asama bazli olcum tablosu basar:
// ZORVEN_S2_REPORT=1 go test -run TestScreen2StageReport -v ./agent
func TestScreen2StageReport(t *testing.T) {
	if os.Getenv("ZORVEN_S2_REPORT") == "" {
		t.Skip("ZORVEN_S2_REPORT ayarli degil")
	}
	ms := func(d time.Duration) string { return fmt.Sprintf("%6.2f", float64(d.Microseconds())/1000) }
	fmt.Printf("GOMAXPROCS=%d, kodlama is parcacigi=%d\n", runtime.GOMAXPROCS(0), screen2EncodeWorkers())
	fmt.Println("sahne               scale   diff  encode   pack  toplam(ms)  seri~fps  boru~fps  KB/kare  rect")
	for _, sc := range benchScenes() {
		var runs []stageTimes
		for k := 0; k < 3; k++ {
			runs = append(runs, measureScene(sc, 12, screen2EncodeWorkers()))
		}
		sort.Slice(runs, func(i, j int) bool { return runs[i].total < runs[j].total })
		r := runs[1]
		fps := 1000 / (float64(r.total.Microseconds()) / 1000)
		// boru hatti: A = scale+diff, B = encode+pack; hiz = 1/max(A,B)
		slowest := max(r.scale+r.diff, r.encode+r.pack)
		pfps := 1000 / (float64(slowest.Microseconds()) / 1000)
		fmt.Printf("%-18s %s %s %s %s  %s  %7.1f  %8.1f  %7d  %4d\n", sc.name,
			ms(r.scale), ms(r.diff), ms(r.encode), ms(r.pack), ms(r.total), fps, pfps, r.bytes/1024, r.rects)
	}
}

func BenchmarkScaleRGBA1080p(b *testing.B) {
	src := synthDesktop(1920, 1080, rand.New(rand.NewSource(1)))
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		scaleRGBA(src, defaultMaxWidth)
	}
}

func BenchmarkBGRAToRGBA1080p(b *testing.B) {
	src := make([]byte, 1920*1080*4)
	dst := image.NewRGBA(image.Rect(0, 0, 1920, 1080))
	b.SetBytes(int64(len(src)))
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		bgraToRGBA(dst.Pix, src, 1920, 1080, 1920*4, dst.Stride)
	}
}
