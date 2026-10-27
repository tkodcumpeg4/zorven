package agent

import (
	"encoding/binary"
	"fmt"
	"image"
	"runtime"
	"sync"
)

// H.264 yardimcilari (platformdan bagimsiz, birim testlenir).
//
// Kodlayici (Windows Media Foundation) Annex-B (00 00 01 baslangic kodlu) cikti
// verir. WebCodecs ise "avc" bicimini (uzunluk onekli NAL'lar + avcC
// description) bekler; bu dosya donusumu yapar. Ayrica BGRA/RGBA -> NV12.

// H.264 NAL turleri.
const (
	nalSlice    = 1
	nalIDR      = 5
	nalSEI      = 6
	nalSPS      = 7
	nalPPS      = 8
	nalAUD      = 9
	nalEndSeq   = 10
	nalEndStrm  = 11
	nalFiller   = 12
	nalTypeMask = 0x1f
)

// splitAnnexB, Annex-B akisini NAL birimlerine boler (baslangic kodlari
// atilir; sondaki sifir baytlar kirpilir). Bicimsiz girdi icin bos doner.
func splitAnnexB(b []byte) [][]byte {
	var nals [][]byte
	start := -1
	i := 0
	for i+2 < len(b) {
		if b[i] == 0 && b[i+1] == 0 && b[i+2] == 1 {
			if start >= 0 {
				nals = appendNAL(nals, b[start:i])
			}
			i += 3
			start = i
			continue
		}
		i++
	}
	if start >= 0 && start <= len(b) {
		nals = appendNAL(nals, b[start:])
	}
	return nals
}

func appendNAL(nals [][]byte, n []byte) [][]byte {
	// 4 bayt baslangic kodunun ilk sifiri onceki NAL'in sonuna yapisir: kirp.
	for len(n) > 0 && n[len(n)-1] == 0 {
		n = n[:len(n)-1]
	}
	if len(n) == 0 {
		return nals
	}
	return append(nals, n)
}

// h264AU, bir erisim biriminin (kare) ayristirilmis hali.
type h264AU struct {
	SPS, PPS []byte // son gorulenler (yoksa nil)
	IDR      bool
	AVCC     []byte // uzunluk onekli kesit NAL'lari (SPS/PPS/AUD/dolgu haric)
}

// parseAnnexBAU, bir kodlayici ciktisini WebCodecs chunk verisine donusturur.
func parseAnnexBAU(b []byte) h264AU {
	var au h264AU
	for _, n := range splitAnnexB(b) {
		switch n[0] & nalTypeMask {
		case nalSPS:
			au.SPS = n
		case nalPPS:
			au.PPS = n
		case nalAUD, nalFiller, nalEndSeq, nalEndStrm:
		case nalIDR:
			au.IDR = true
			au.AVCC = appendAVCC(au.AVCC, n)
		default:
			au.AVCC = appendAVCC(au.AVCC, n)
		}
	}
	return au
}

func appendAVCC(dst, nal []byte) []byte {
	dst = binary.BigEndian.AppendUint32(dst, uint32(len(nal)))
	return append(dst, nal...)
}

// avcDecoderConfig, ISO 14496-15 AVCDecoderConfigurationRecord (avcC) olusturur
// (WebCodecs VideoDecoderConfig.description). NAL uzunluk alani 4 bayt.
func avcDecoderConfig(sps, pps []byte) ([]byte, error) {
	if len(sps) < 4 || len(pps) < 1 || len(sps) > 0xffff || len(pps) > 0xffff {
		return nil, fmt.Errorf("gecersiz SPS/PPS (%d/%d bayt)", len(sps), len(pps))
	}
	b := []byte{1, sps[1], sps[2], sps[3], 0xff, 0xe1}
	b = binary.BigEndian.AppendUint16(b, uint16(len(sps)))
	b = append(b, sps...)
	b = append(b, 1)
	b = binary.BigEndian.AppendUint16(b, uint16(len(pps)))
	b = append(b, pps...)
	return b, nil
}

// avcCodecString, SPS'ten WebCodecs kodek dizesini uretir ("avc1.PPCCLL").
func avcCodecString(sps []byte) string {
	if len(sps) < 4 {
		return ""
	}
	return fmt.Sprintf("avc1.%02x%02x%02x", sps[1], sps[2], sps[3])
}

// --- RGBA -> NV12 ------------------------------------------------------------

// nv12Size, w x h (cift) NV12 tampon boyutu.
func nv12Size(w, h int) int { return w*h + w*h/2 }

// rgbaToNV12 donusumu: img'nin sol-ust w x h bolgesi (w,h cift) dst'ye
// (nv12Size(w,h) bayt) yazilir. Sinirli aralik (16-235/16-240); h >= 720 icin
// BT.709, aksi halde BT.601 (tarayicilarin H.264 icin varsayimi). Satir
// bantlari paralel islenir.
func rgbaToNV12(dst []byte, img *image.RGBA, w, h int) {
	if w <= 0 || h <= 0 || w&1 != 0 || h&1 != 0 || len(dst) < nv12Size(w, h) ||
		img.Rect.Dx() < w || img.Rect.Dy() < h {
		return
	}
	bt709 := h >= 720
	workers := min(runtime.GOMAXPROCS(0), 4, h/16)
	if workers < 2 {
		nv12Rows(dst, img, w, h, 0, h, bt709)
		return
	}
	var wg sync.WaitGroup
	per := (h / workers) &^ 1
	for k := 0; k < workers; k++ {
		y0, y1 := k*per, (k+1)*per
		if k == workers-1 {
			y1 = h
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			nv12Rows(dst, img, w, h, y0, y1, bt709)
		}()
	}
	wg.Wait()
}

// nv12Rows, [y0,y1) satirlarini (y0,y1 cift) donusturur.
func nv12Rows(dst []byte, img *image.RGBA, w, h, y0, y1 int, bt709 bool) {
	// Katsayilar x256.
	yr, yg, yb := 66, 129, 25
	ur, ug, ub := -38, -74, 112
	vr, vg, vb := 112, -94, -18
	if bt709 {
		yr, yg, yb = 47, 157, 16
		ur, ug, ub = -26, -87, 112
		vr, vg, vb = 112, -102, -10
	}
	yPlane := dst[:w*h]
	uv := dst[w*h:]
	for y := y0; y < y1; y += 2 {
		r0 := img.Pix[img.PixOffset(img.Rect.Min.X, img.Rect.Min.Y+y):]
		r1 := img.Pix[img.PixOffset(img.Rect.Min.X, img.Rect.Min.Y+y+1):]
		yo0, yo1 := y*w, (y+1)*w
		uo := (y / 2) * w
		for x := 0; x < w; x += 2 {
			i := x * 4
			ra, ga, ba := int(r0[i]), int(r0[i+1]), int(r0[i+2])
			rb, gb, bb := int(r0[i+4]), int(r0[i+5]), int(r0[i+6])
			rc, gc, bc := int(r1[i]), int(r1[i+1]), int(r1[i+2])
			rd, gd, bd := int(r1[i+4]), int(r1[i+5]), int(r1[i+6])
			yPlane[yo0+x] = clamp8(((yr*ra + yg*ga + yb*ba + 128) >> 8) + 16)
			yPlane[yo0+x+1] = clamp8(((yr*rb + yg*gb + yb*bb + 128) >> 8) + 16)
			yPlane[yo1+x] = clamp8(((yr*rc + yg*gc + yb*bc + 128) >> 8) + 16)
			yPlane[yo1+x+1] = clamp8(((yr*rd + yg*gd + yb*bd + 128) >> 8) + 16)
			r := (ra + rb + rc + rd + 2) >> 2
			g := (ga + gb + gc + gd + 2) >> 2
			b := (ba + bb + bc + bd + 2) >> 2
			uv[uo+x] = clamp8(((ur*r + ug*g + ub*b + 128) >> 8) + 128)
			uv[uo+x+1] = clamp8(((vr*r + vg*g + vb*b + 128) >> 8) + 128)
		}
	}
}

func clamp8(v int) byte {
	if v < 0 {
		return 0
	}
	if v > 255 {
		return 255
	}
	return byte(v)
}
