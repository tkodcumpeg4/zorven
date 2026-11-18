package agent

import (
	"bytes"
	"encoding/binary"
	"errors"
	"image"
	"image/draw"
	"image/png"
	"math/bits"

	"github.com/tkodcumpeg4/zorven/shared/protocol"
)

// PNG <-> Windows DIB donusumu. SAF Go ve platformdan bagimsiz: Win32 cagrilari
// screen_clipboard_windows.go'da, burada yalniz bayt dizileri islenir (Linux'ta
// birim testlenir).
//
// Yazma (pngToDIB): CF_DIB = BITMAPINFOHEADER(40) + 32 bit BI_RGB, alt-ust satir
// sirasi, saydamlik BEYAZ uzerine birlestirilir (BI_RGB'de alfa kanalini
// uygulamalar yok sayar; saydam piksel siyah gorunmesin). Alfa korunmasi icin
// ayrica kayitli "PNG" bicimi yazilir.
//
// Okuma (dibToPNG): BITMAPINFOHEADER / V4 / V5 basliklari, 24 ve 32 bit,
// BI_RGB ve BI_BITFIELDS, alt-ust ve ust-alt (negatif yukseklik) satir sirasi,
// 4 bayta hizali satir dolgusu, palet atlama (biClrUsed).

var (
	errClipImageInvalid  = errors.New("pano gorseli gecersiz")
	errClipImageTooLarge = errors.New("pano gorseli sinirlari asiyor (8 MiB / 24 MP)")
)

const (
	biRGB       = 0
	biBitfields = 3

	dibHeaderMin = 40
)

// pngToDIB, PNG'yi CF_DIB icerigine (BITMAPINFOHEADER + pikseller) cevirir.
func pngToDIB(pngData []byte) ([]byte, error) {
	w, h, err := protocol.ClipImageInfo(pngData)
	if err != nil {
		return nil, errClipImageTooLarge
	}
	src, err := png.Decode(bytes.NewReader(pngData))
	if err != nil {
		return nil, errClipImageInvalid
	}
	b := src.Bounds()
	if b.Dx() != w || b.Dy() != h {
		return nil, errClipImageInvalid
	}
	nr := image.NewNRGBA(image.Rect(0, 0, w, h))
	draw.Draw(nr, nr.Bounds(), src, b.Min, draw.Src)

	stride := w * 4
	out := make([]byte, dibHeaderMin+stride*h)
	le := binary.LittleEndian
	le.PutUint32(out[0:], dibHeaderMin)
	le.PutUint32(out[4:], uint32(int32(w)))
	le.PutUint32(out[8:], uint32(int32(h))) // pozitif: alt-ust
	le.PutUint16(out[12:], 1)
	le.PutUint16(out[14:], 32)
	le.PutUint32(out[16:], biRGB)
	le.PutUint32(out[20:], uint32(stride*h))
	px := out[dibHeaderMin:]
	for y := 0; y < h; y++ {
		row := nr.Pix[y*nr.Stride : y*nr.Stride+stride]
		dst := px[(h-1-y)*stride:][:stride]
		for x := 0; x < w; x++ {
			r, g, bl, a := uint32(row[x*4]), uint32(row[x*4+1]), uint32(row[x*4+2]), uint32(row[x*4+3])
			if a != 255 {
				r = (r*a + 255*(255-a) + 127) / 255
				g = (g*a + 255*(255-a) + 127) / 255
				bl = (bl*a + 255*(255-a) + 127) / 255
			}
			dst[x*4], dst[x*4+1], dst[x*4+2], dst[x*4+3] = byte(bl), byte(g), byte(r), 255
		}
	}
	return out, nil
}

// maskChan, bit maskesinden 8 bitlik kanal degeri cikarir.
type maskChan struct {
	mask  uint32
	shift uint
	nbits int
}

func newMaskChan(m uint32) maskChan {
	if m == 0 {
		return maskChan{}
	}
	sh := uint(bits.TrailingZeros32(m))
	return maskChan{mask: m, shift: sh, nbits: bits.OnesCount32(m >> sh)}
}

func (c maskChan) get(px uint32) byte {
	if c.nbits == 0 {
		return 0
	}
	v := (px & c.mask) >> c.shift
	switch {
	case c.nbits == 8:
		return byte(v)
	case c.nbits > 8:
		return byte(v >> uint(c.nbits-8))
	default:
		return byte(v * 255 / (1<<uint(c.nbits) - 1))
	}
}

// dibToPNG, CF_DIB / CF_DIBV5 icerigini PNG'ye cevirir. Bozuk/kesik girdi ya da
// sinirlari asan gorsel hata verir.
func dibToPNG(dib []byte) ([]byte, error) {
	le := binary.LittleEndian
	if len(dib) < dibHeaderMin {
		return nil, errClipImageInvalid
	}
	hsz := int(le.Uint32(dib[0:]))
	switch hsz {
	case 40, 52, 56, 108, 124:
	default:
		return nil, errClipImageInvalid
	}
	if len(dib) < hsz {
		return nil, errClipImageInvalid
	}
	w64 := int64(int32(le.Uint32(dib[4:])))
	h64 := int64(int32(le.Uint32(dib[8:])))
	topDown := h64 < 0
	if topDown {
		h64 = -h64
	}
	if w64 <= 0 || h64 <= 0 || w64 > protocol.MaxClipImageDim || h64 > protocol.MaxClipImageDim ||
		w64*h64 > protocol.MaxClipImagePixels {
		return nil, errClipImageTooLarge
	}
	w, h := int(w64), int(h64)
	bpp := int(le.Uint16(dib[14:]))
	comp := le.Uint32(dib[16:])
	clrUsed := int64(le.Uint32(dib[32:]))
	if (bpp != 24 && bpp != 32) || (comp != biRGB && comp != biBitfields) || (comp == biBitfields && bpp != 32) {
		return nil, errClipImageInvalid
	}
	if clrUsed > 256 {
		return nil, errClipImageInvalid
	}

	// Renk maskeleri.
	rM, gM, bM, aM := uint32(0x00FF0000), uint32(0x0000FF00), uint32(0x000000FF), uint32(0)
	off := int64(hsz) + clrUsed*4
	if comp == biBitfields {
		if hsz == 40 {
			if len(dib) < 52 {
				return nil, errClipImageInvalid
			}
			rM, gM, bM = le.Uint32(dib[40:]), le.Uint32(dib[44:]), le.Uint32(dib[48:])
			off = 52 + clrUsed*4
		} else {
			rM, gM, bM = le.Uint32(dib[40:]), le.Uint32(dib[44:]), le.Uint32(dib[48:])
			if hsz >= 56 {
				aM = le.Uint32(dib[52:])
			}
		}
		if rM == 0 || gM == 0 || bM == 0 {
			return nil, errClipImageInvalid
		}
	}

	stride := int64((w*bpp+31)/32) * 4
	if off+stride*int64(h) > int64(len(dib)) {
		return nil, errClipImageInvalid // kesik veri
	}
	data := dib[off:]

	img := image.NewNRGBA(image.Rect(0, 0, w, h))
	rC, gC, bC, aC := newMaskChan(rM), newMaskChan(gM), newMaskChan(bM), newMaskChan(aM)
	anyAlpha := false
	for y := 0; y < h; y++ {
		sy := y
		if !topDown {
			sy = h - 1 - y
		}
		row := data[int64(sy)*stride:][:stride]
		dst := img.Pix[y*img.Stride:][:w*4]
		for x := 0; x < w; x++ {
			var r, g, b, a byte
			switch {
			case bpp == 24:
				b, g, r, a = row[x*3], row[x*3+1], row[x*3+2], 255
			case comp == biRGB:
				b, g, r, a = row[x*4], row[x*4+1], row[x*4+2], 255
			default:
				px := le.Uint32(row[x*4:])
				r, g, b = rC.get(px), gC.get(px), bC.get(px)
				a = 255
				if aC.nbits > 0 {
					a = aC.get(px)
					if a != 0 {
						anyAlpha = true
					}
				}
			}
			dst[x*4], dst[x*4+1], dst[x*4+2], dst[x*4+3] = r, g, b, a
		}
	}
	// Alfa maskesi var ama hic piksel alfa tasimiyorsa (hepsi 0): opak say.
	if aC.nbits > 0 && !anyAlpha {
		for i := 3; i < len(img.Pix); i += 4 {
			img.Pix[i] = 255
		}
	}

	var buf bytes.Buffer
	enc := png.Encoder{CompressionLevel: png.BestSpeed}
	if err := enc.Encode(&buf, img); err != nil {
		return nil, errClipImageInvalid
	}
	if buf.Len() > protocol.MaxScreenClipboardImage {
		return nil, errClipImageTooLarge
	}
	return buf.Bytes(), nil
}
