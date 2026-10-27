package agent

import (
	"bytes"
	"image"
	"runtime"
	"sync"
)

// Hizli, artimli (incremental) olcekleme.
//
// Eski yol her karede x/image/draw.ApproxBiLinear ile TUM kareyi kucultuyordu
// (1080p -> 1600: ~41 ms). Burada:
//   - Cekirdek alan ortalamasi (box filtre, kesirli kapsama agirliklariyla):
//     ayrilabilir iki gecis, sabit noktali tamsayi aritmetigi, satir bantlari
//     uzerinde paralel.
//   - Oncesi kare ile ayni boyutta ise kaynakta degisen 64 px karolar bulunur
//     (memcmp) ve YALNIZ onlarin hedef karsiligi yeniden hesaplanir; gerisi
//     onceki olcekli kareden kopyalanir. Her hedef piksel kaynagin saf bir
//     fonksiyonu oldugundan sonuc tam olcekleme ile BIT BIT aynidir.
//
// Hedef alfa her zaman 255'tir (ekran opaktir).

// scaleAxis, tek eksen icin onceden hesaplanmis kapsama agirliklari.
type scaleAxis struct {
	srcN, dstN int
	taps       int      // hedef basina en fazla tap sayisi
	start      []int32  // hedef i icin ilk kaynak indeksi
	cnt        []uint8  // hedef i icin tap sayisi
	w          []uint16 // [i*taps+k]; her hedefin toplami 256
}

func newScaleAxis(srcN, dstN int) *scaleAxis {
	taps := (srcN+dstN-1)/dstN + 1
	a := &scaleAxis{srcN: srcN, dstN: dstN, taps: taps,
		start: make([]int32, dstN), cnt: make([]uint8, dstN), w: make([]uint16, dstN*taps)}
	for i := 0; i < dstN; i++ {
		lo := int64(i) * int64(srcN) // hedef i, olcek birimi [lo, hi)
		hi := lo + int64(srcN)
		j0 := int(lo / int64(dstN))
		j1 := int((hi + int64(dstN) - 1) / int64(dstN)) // dislayici
		if j1 > srcN {
			j1 = srcN
		}
		a.start[i] = int32(j0)
		a.cnt[i] = uint8(j1 - j0)
		sum, big := 0, 0
		for j := j0; j < j1; j++ {
			ovLo := max(lo, int64(j)*int64(dstN))
			ovHi := min(hi, int64(j+1)*int64(dstN))
			w := int((ovHi - ovLo) * 256 / int64(srcN))
			a.w[i*taps+(j-j0)] = uint16(w)
			sum += w
			if w > int(a.w[i*taps+big]) {
				big = j - j0
			}
		}
		a.w[i*taps+big] += uint16(256 - sum) // toplam tam 256
	}
	return a
}

// scaleRegion, hedef dikdortgen dr'yi src'den hesaplayip dst'ye yazar.
func scaleRegion(dst, src *image.RGBA, ax, ay *scaleAxis, dr image.Rectangle) {
	if dr.Empty() {
		return
	}
	workers := min(runtime.GOMAXPROCS(0), 8)
	rows := dr.Dy()
	if workers > 1 && rows*dr.Dx() >= 64*1024 {
		workers = min(workers, rows/8)
	} else {
		workers = 1
	}
	if workers <= 1 {
		scaleBand(dst, src, ax, ay, dr)
		return
	}
	var wg sync.WaitGroup
	per := (rows + workers - 1) / workers
	for y := dr.Min.Y; y < dr.Max.Y; y += per {
		band := image.Rect(dr.Min.X, y, dr.Max.X, min(y+per, dr.Max.Y))
		wg.Add(1)
		go func() {
			defer wg.Done()
			scaleBand(dst, src, ax, ay, band)
		}()
	}
	wg.Wait()
}

// scaleBand, tek is parcaciginde bir bandi hesaplar. Yatay gecis sonucu satir
// basina bir kez hesaplanir ve komsu hedef satirlari arasinda paylasilir.
func scaleBand(dst, src *image.RGBA, ax, ay *scaleAxis, dr image.Rectangle) {
	w := dr.Dx()
	ring := ay.taps + 1
	rowBuf := make([]uint32, ring*w*3)
	tag := make([]int, ring)
	for i := range tag {
		tag[i] = -1
	}
	hrow := func(sy int) []uint32 {
		slot := sy % ring
		out := rowBuf[slot*w*3 : (slot+1)*w*3]
		if tag[slot] == sy {
			return out
		}
		tag[slot] = sy
		srow := src.Pix[sy*src.Stride:]
		o := 0
		for dx := dr.Min.X; dx < dr.Max.X; dx++ {
			s0 := int(ax.start[dx])
			n := int(ax.cnt[dx])
			ws := ax.w[dx*ax.taps : dx*ax.taps+n]
			var r, g, b uint32
			p := s0 * 4
			for _, wt := range ws {
				ww := uint32(wt)
				r += uint32(srow[p]) * ww
				g += uint32(srow[p+1]) * ww
				b += uint32(srow[p+2]) * ww
				p += 4
			}
			out[o], out[o+1], out[o+2] = r, g, b
			o += 3
		}
		return out
	}
	rows := make([][]uint32, 0, ay.taps)
	for dy := dr.Min.Y; dy < dr.Max.Y; dy++ {
		s0 := int(ay.start[dy])
		n := int(ay.cnt[dy])
		ws := ay.w[dy*ay.taps : dy*ay.taps+n]
		drow := dst.Pix[dy*dst.Stride+dr.Min.X*4 : dy*dst.Stride+dr.Max.X*4]
		if n == 1 { // tek satir: dogrudan
			h := hrow(s0)
			o := 0
			for x := 0; x < len(drow); x += 4 {
				drow[x] = uint8((h[o] + 128) >> 8)
				drow[x+1] = uint8((h[o+1] + 128) >> 8)
				drow[x+2] = uint8((h[o+2] + 128) >> 8)
				drow[x+3] = 255
				o += 3
			}
			continue
		}
		rows = rows[:0]
		for k := 0; k < n; k++ {
			rows = append(rows, hrow(s0+k))
		}
		o := 0
		for x := 0; x < len(drow); x += 4 {
			var r, g, b uint32
			for k := 0; k < n; k++ {
				ww := uint32(ws[k])
				h := rows[k]
				r += h[o] * ww
				g += h[o+1] * ww
				b += h[o+2] * ww
			}
			drow[x] = uint8((r + 32768) >> 16)
			drow[x+1] = uint8((g + 32768) >> 16)
			drow[x+2] = uint8((b + 32768) >> 16)
			drow[x+3] = 255
			o += 3
		}
	}
}

// frameScaler, bir akis (ya da katman) icin artimli olceklemeyi surdurur.
// Goroutine-guvenli DEGIL; tek yakalama dongusu kullanir.
type frameScaler struct {
	prevSrc *image.RGBA
	prevDst *image.RGBA
	ax, ay  *scaleAxis
}

// scaleIncrementalMaxFrac: kirli kaynak alani bu orani asarsa tam olcekleme
// (kopya + bolge bolge hesaplama avantajini yitirir).
const scaleIncrementalMaxFrac = 0.6

// Scale, src'yi maxWidth'e indirger (kucukse src'nin kendisini doner). Donen
// goruntu cagirana aittir: sonraki cagrilarda yeniden KULLANILMAZ.
func (fs *frameScaler) Scale(src *image.RGBA, maxWidth int) *image.RGBA {
	return fs.ScaleHinted(src, maxWidth, srcHint{})
}

// ScaleHinted, Scale gibidir; ipucu varsa kaynakta degisen bolgeler tum kareyi
// karsilastirmadan ipucundan bulunur (bkz. screen2_hint.go).
func (fs *frameScaler) ScaleHinted(src *image.RGBA, maxWidth int, hint srcHint) *image.RGBA {
	if maxWidth <= 0 {
		maxWidth = defaultMaxWidth
	}
	sw, sh := src.Rect.Dx(), src.Rect.Dy()
	if sw <= maxWidth || sw == 0 || sh == 0 {
		fs.prevSrc, fs.prevDst = nil, nil
		return src
	}
	dw := maxWidth
	dh := max(sh*maxWidth/sw, 1)
	if fs.ax == nil || fs.ax.srcN != sw || fs.ax.dstN != dw || fs.ay.srcN != sh || fs.ay.dstN != dh {
		fs.ax, fs.ay = newScaleAxis(sw, dw), newScaleAxis(sh, dh)
		fs.prevSrc, fs.prevDst = nil, nil
	}
	dst := image.NewRGBA(image.Rect(0, 0, dw, dh))
	full := image.Rect(0, 0, dw, dh)

	if p := fs.prevSrc; p != nil && p.Rect.Dx() == sw && p.Rect.Dy() == sh {
		if regs, ok := fs.dirtyRegions(p, src, dw, dh, hint); ok {
			copy(dst.Pix, fs.prevDst.Pix)
			for _, r := range regs {
				scaleRegion(dst, src, fs.ax, fs.ay, r)
			}
			fs.prevSrc, fs.prevDst = src, dst
			return dst
		}
	}
	scaleRegion(dst, src, fs.ax, fs.ay, full)
	fs.prevSrc, fs.prevDst = src, dst
	return dst
}

// dirtyRegions, kaynakta degisen alanlarin hedef karsiliklarini (kenarlarda
// 2 px genisletilmis) doner. Cok fazla degismisse ok=false.
func (fs *frameScaler) dirtyRegions(prev, cur *image.RGBA, dw, dh int, hint srcHint) (regs []image.Rectangle, ok bool) {
	sw, sh := cur.Rect.Dx(), cur.Rect.Dy()
	tx := (sw + tileSize - 1) / tileSize
	ty := (sh + tileSize - 1) / tileSize
	dirty := make([]bool, tx*ty)
	nd := 0
	if hint.ok {
		// Kaynak izgarasinda ipucu karolari (bellek karsilastirmasi yok).
		dirty, _, _ = hintTiles(hint.rects, sw, sh, sw, sh, 0)
		for _, d := range dirty {
			if d {
				nd++
			}
		}
	}
	for j := 0; j < ty && !hint.ok; j++ {
		y0, y1 := j*tileSize, min((j+1)*tileSize, sh)
		for i := 0; i < tx; i++ {
			x0, x1 := i*tileSize, min((i+1)*tileSize, sw)
			for y := y0; y < y1; y++ {
				po, co := y*prev.Stride, y*cur.Stride
				if !bytes.Equal(prev.Pix[po+x0*4:po+x1*4], cur.Pix[co+x0*4:co+x1*4]) {
					dirty[j*tx+i] = true
					nd++
					break
				}
			}
		}
	}
	if float64(nd) > scaleIncrementalMaxFrac*float64(tx*ty) {
		return nil, false
	}
	for _, r := range mergeDirty(dirty, tx, ty, sw, sh) {
		x0 := r.Min.X*dw/sw - 2
		x1 := (r.Max.X*dw+sw-1)/sw + 2
		y0 := r.Min.Y*dh/sh - 2
		y1 := (r.Max.Y*dh+sh-1)/sh + 2
		regs = append(regs, image.Rect(max(x0, 0), max(y0, 0), min(x1, dw), min(y1, dh)))
	}
	return regs, true
}

// scaleRGBA, tek seferlik (durumsuz) olcekleme; testler/yardimcilar icin.
func scaleRGBA(src *image.RGBA, maxWidth int) *image.RGBA {
	var fs frameScaler
	return fs.Scale(src, maxWidth)
}
