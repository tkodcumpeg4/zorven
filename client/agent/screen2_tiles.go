package agent

import (
	"bytes"
	"encoding/binary"
	"image"
	"image/jpeg"
	"image/png"

	"github.com/tkodcumpeg4/zorven/shared/protocol"
)

// screen2 karo boyutu ve dikdortgen bolme siniri.
const (
	tileSize = 64
	// screen2MaxRectDim, tek dikdortgenin en buyuk kenari: buyuk guncellemeler
	// (anahtar kare) bu boyutlu parcalara bolunur ki mesajlar kucuk kalsin ve
	// kodlama gerektiginde ince taneli bolunebilsin.
	screen2MaxRectDim = 512
	// screen2MaxPayload, tek dikdortgen yukunun hedef siniri; asilirsa dikdortgen
	// ikiye bolunerek yeniden kodlanir.
	screen2MaxPayload = 200 << 10
	// screen2PNGColors: ornekte bu kadar veya daha az renk varsa PNG (metin/UI).
	screen2PNGColors = 64
)

// diffTiles, iki kare arasinda degisen karolari bulur. prev nil veya boyutu
// farkliysa TUM karolar degismis sayilir. Satirlar bytes.Equal ile (memcmp)
// karsilastirilir.
func diffTiles(prev, cur *image.RGBA) (dirty []bool, tx, ty int) {
	w, h := cur.Rect.Dx(), cur.Rect.Dy()
	tx = (w + tileSize - 1) / tileSize
	ty = (h + tileSize - 1) / tileSize
	dirty = make([]bool, tx*ty)
	if prev == nil || prev.Rect.Dx() != w || prev.Rect.Dy() != h {
		for i := range dirty {
			dirty[i] = true
		}
		return
	}
	for j := 0; j < ty; j++ {
		y0, y1 := j*tileSize, min((j+1)*tileSize, h)
		for i := 0; i < tx; i++ {
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

// mergeDirty, kirli karolari dikdortgenlere birlestirir: once her karo satirinda
// ardisik kirli karolar bir serit olur, sonra ayni x/genislikteki seritler alt
// alta birlesir. Sonuc w x h tuval icine kirpilir.
func mergeDirty(dirty []bool, tx, ty, w, h int) []image.Rectangle {
	var done []image.Rectangle
	var open []image.Rectangle // onceki satirdan uzayabilecek dikdortgenler
	for j := 0; j < ty; j++ {
		y0, y1 := j*tileSize, min((j+1)*tileSize, h)
		var next []image.Rectangle
		for i := 0; i < tx; {
			if !dirty[j*tx+i] {
				i++
				continue
			}
			s := i
			for i < tx && dirty[j*tx+i] {
				i++
			}
			x0, x1 := s*tileSize, min(i*tileSize, w)
			ext := false
			for k, o := range open {
				if o.Min.X == x0 && o.Max.X == x1 && o.Max.Y == y0 {
					o.Max.Y = y1
					next = append(next, o)
					open[k].Max.X = -1 // tuketildi isareti
					ext = true
					break
				}
			}
			if !ext {
				next = append(next, image.Rect(x0, y0, x1, y1))
			}
		}
		for _, o := range open {
			if o.Max.X != -1 {
				done = append(done, o)
			}
		}
		open = next
	}
	return append(done, open...)
}

// splitRect, dikdortgeni en fazla maxDim kenarli parcalara boler.
func splitRect(r image.Rectangle, maxDim int) []image.Rectangle {
	var out []image.Rectangle
	for y := r.Min.Y; y < r.Max.Y; y += maxDim {
		for x := r.Min.X; x < r.Max.X; x += maxDim {
			out = append(out, image.Rect(x, y, min(x+maxDim, r.Max.X), min(y+maxDim, r.Max.Y)).Intersect(r))
		}
	}
	return out
}

// goTileEncoder, stdlib JPEG/PNG ile TileEncoder uygular.
type goTileEncoder struct {
	buf bytes.Buffer
	png png.Encoder
}

func newGoTileEncoder() *goTileEncoder {
	return &goTileEncoder{png: png.Encoder{CompressionLevel: png.BestSpeed}}
}

// Encode, r bolgesini kodlar: duz renk -> solid; az renk -> PNG; aksi JPEG.
// Donen yuk, sonraki cagriya kadar gecerli DEGIL: kopyadir.
func (e *goTileEncoder) Encode(img *image.RGBA, r image.Rectangle, quality int) (uint8, []byte, error) {
	sub := img.SubImage(r).(*image.RGBA)
	if c, ok := solidColor(sub); ok {
		p := make([]byte, 4)
		binary.BigEndian.PutUint32(p, c)
		return protocol.Screen2EncSolid, p, nil
	}
	e.buf.Reset()
	if fewColors(sub, screen2PNGColors) {
		// Yakalayicinin alfa kanali guvenilmez olabilir (GDI): opak kopya kodla.
		op := image.NewRGBA(image.Rect(0, 0, r.Dx(), r.Dy()))
		for y := 0; y < r.Dy(); y++ {
			src := sub.Pix[y*sub.Stride : y*sub.Stride+r.Dx()*4]
			dst := op.Pix[y*op.Stride : y*op.Stride+r.Dx()*4]
			copy(dst, src)
			for x := 3; x < len(dst); x += 4 {
				dst[x] = 0xff
			}
		}
		if err := e.png.Encode(&e.buf, op); err != nil {
			return 0, nil, err
		}
		return protocol.Screen2EncPNG, append([]byte(nil), e.buf.Bytes()...), nil
	}
	if err := jpeg.Encode(&e.buf, sub, &jpeg.Options{Quality: quality}); err != nil {
		return 0, nil, err
	}
	return protocol.Screen2EncJPEG, append([]byte(nil), e.buf.Bytes()...), nil
}

// solidColor, bolgenin tek renk olup olmadigini soyler; renk R,G,B,0xff (RGBA, big-endian).
func solidColor(img *image.RGBA) (uint32, bool) {
	w, h := img.Rect.Dx(), img.Rect.Dy()
	if w == 0 || h == 0 {
		return 0, false
	}
	first := img.Pix[0:4]
	for y := 0; y < h; y++ {
		row := img.Pix[y*img.Stride : y*img.Stride+w*4]
		for x := 0; x < len(row); x += 4 {
			if row[x] != first[0] || row[x+1] != first[1] || row[x+2] != first[2] {
				return 0, false
			}
		}
	}
	return uint32(first[0])<<24 | uint32(first[1])<<16 | uint32(first[2])<<8 | 0xff, true
}

// fewColors, bolgeden alinan ornekte en fazla limit farkli renk varsa true doner.
// Ornek ~400 noktadir (asal adimla; sutun hizalanmasindan kacinir).
func fewColors(img *image.RGBA, limit int) bool {
	w, h := img.Rect.Dx(), img.Rect.Dy()
	n := w * h
	step := n / 400
	if step < 1 {
		step = 1
	}
	if step%2 == 0 {
		step++
	}
	seen := make([]uint32, 0, limit+1)
	for i := 0; i < n; i += step {
		o := (i/w)*img.Stride + (i%w)*4
		c := uint32(img.Pix[o])<<16 | uint32(img.Pix[o+1])<<8 | uint32(img.Pix[o+2])
		dup := false
		for _, s := range seen {
			if s == c {
				dup = true
				break
			}
		}
		if !dup {
			seen = append(seen, c)
			if len(seen) > limit {
				return false
			}
		}
	}
	return true
}
