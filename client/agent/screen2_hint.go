package agent

import (
	"bytes"
	"image"
)

// Kirli bolge ipuclari (DXGI Desktop Duplication).
//
// Bazi yakalayicilar (DXGI) hangi bolgelerin degistigini isletim sisteminden
// bilir. Bu bilgi boru hattinda iki yerde bellek karsilastirmasini (memcmp)
// sinirlar veya tamamen atlatir:
//   - olcekleyici: kaynakta degisen bolgeleri tum kareyi karsilastirmadan bulur
//   - karo farki: yalniz ipucu karolari karsilastirilir
//
// DOGRULUK: ipucu yalniz "bu bolge disinda hicbir sey degismedi" iddiasidir.
// Ipucunun kapsami eksikse ekran bozulur; bu yuzden yakalayici emin degilse
// (ilk kare, yeniden kurulum, hata) ipucu vermez (hinted=false) ya da tum
// ekrani verir.

// hintCapturer, kirli bolge ipucu verebilen yakalayici (Capturer'a ek).
type hintCapturer interface {
	// CaptureHinted: hinted=false => ipucu yok, img tam goruntu (Capture gibi).
	// hinted=true ve len(hint)==0 => onceki yakalamadan beri degisim yok (img
	// nil olabilir; cagiran son goruntuyu kullanir). hinted=true ve hint dolu =>
	// yalniz bu dikdortgenler degisti; img tam ve cagirana aittir.
	CaptureHinted(display int) (img *image.RGBA, hint []image.Rectangle, hinted bool, err error)
}

// srcHint, bir yakalama turunun ipucu. ok=false => ipucu yok.
type srcHint struct {
	ok    bool
	rects []image.Rectangle
}

// hintTiles, kaynak (sw x sh) uzayindaki dikdortgenleri hedef (dw x dh) karo
// izgarasindaki bayraklara cevirir. Hedef kaynaktan kucukse (olcekleme) her
// dikdortgen margin piksel genisletilir (olcekleme cekirdegi komsu pikselleri
// etkiler). Karo izgarasi hedefin boyutundandir (diffTiles ile ayni).
func hintTiles(rects []image.Rectangle, sw, sh, dw, dh, margin int) (mask []bool, tx, ty int) {
	tx = (dw + tileSize - 1) / tileSize
	ty = (dh + tileSize - 1) / tileSize
	mask = make([]bool, tx*ty)
	if sw <= 0 || sh <= 0 {
		return
	}
	for _, r := range rects {
		r = r.Intersect(image.Rect(0, 0, sw, sh))
		if r.Empty() {
			continue
		}
		x0 := r.Min.X*dw/sw - margin
		x1 := (r.Max.X*dw+sw-1)/sw + margin
		y0 := r.Min.Y*dh/sh - margin
		y1 := (r.Max.Y*dh+sh-1)/sh + margin
		x0, y0 = max(x0, 0), max(y0, 0)
		x1, y1 = min(x1, dw), min(y1, dh)
		if x0 >= x1 || y0 >= y1 {
			continue
		}
		for j := y0 / tileSize; j <= (y1-1)/tileSize; j++ {
			for i := x0 / tileSize; i <= (x1-1)/tileSize; i++ {
				mask[j*tx+i] = true
			}
		}
	}
	return
}

// diffTilesMasked, diffTiles gibidir ama yalniz mask'te isaretli karolari
// karsilastirir. prev nil/boyut farkli => tum karolar kirli (mask yok sayilir).
// mask karo izgarasi cur'unkiyle ayni olmali (hintTiles uretir).
func diffTilesMasked(prev, cur *image.RGBA, mask []bool) (dirty []bool, tx, ty int) {
	w, h := cur.Rect.Dx(), cur.Rect.Dy()
	tx = (w + tileSize - 1) / tileSize
	ty = (h + tileSize - 1) / tileSize
	dirty = make([]bool, tx*ty)
	if prev == nil || prev.Rect.Dx() != w || prev.Rect.Dy() != h || len(mask) != tx*ty {
		for i := range dirty {
			dirty[i] = true
		}
		return
	}
	for j := 0; j < ty; j++ {
		y0, y1 := j*tileSize, min((j+1)*tileSize, h)
		for i := 0; i < tx; i++ {
			if !mask[j*tx+i] {
				continue
			}
			x0, x1 := i*tileSize, min((i+1)*tileSize, w)
			for y := y0; y < y1; y++ {
				po, co := y*prev.Stride, y*cur.Stride
				if !bytes.Equal(prev.Pix[po+x0*4:po+x1*4], cur.Pix[co+x0*4:co+x1*4]) {
					dirty[j*tx+i] = true
					break
				}
			}
		}
	}
	return
}

// scaleMargin, olceklenmis hedefte kirli bolge genisletmesi (px); frameScaler
// dirtyRegions ile ayni.
const scaleMargin = 2

// diffFrame, onceki ve yeni (olceklenmis) kare arasindaki degisen karolari bulur;
// ipucu varsa yalniz ipucu karolarini karsilastirir.
func diffFrame(prev, cur *image.RGBA, h srcHint, srcW, srcH int) (dirty []bool, tx, ty int) {
	if !h.ok || prev == nil {
		return diffTiles(prev, cur)
	}
	dw, dh := cur.Rect.Dx(), cur.Rect.Dy()
	margin := 0
	if dw != srcW || dh != srcH {
		margin = scaleMargin
	}
	mask, _, _ := hintTiles(h.rects, srcW, srcH, dw, dh, margin)
	return diffTilesMasked(prev, cur, mask)
}
