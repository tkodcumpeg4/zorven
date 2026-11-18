package agent

import (
	"bytes"
	"encoding/binary"
	"image"
	"image/color"
	"image/png"
	"testing"

	"github.com/tkodcumpeg4/zorven/shared/protocol"
)

func encPNG(t testing.TB, img image.Image) []byte {
	t.Helper()
	var b bytes.Buffer
	if err := png.Encode(&b, img); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}

func decPNG(t testing.TB, b []byte) *image.NRGBA {
	t.Helper()
	im, err := png.Decode(bytes.NewReader(b))
	if err != nil {
		t.Fatal(err)
	}
	out := image.NewNRGBA(im.Bounds())
	for y := 0; y < im.Bounds().Dy(); y++ {
		for x := 0; x < im.Bounds().Dx(); x++ {
			out.Set(x, y, im.At(x, y))
		}
	}
	return out
}

func testImage(w, h int, alpha bool) *image.NRGBA {
	im := image.NewNRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			a := uint8(255)
			if alpha {
				a = uint8(40 + (x*37+y*11)%200)
			}
			im.SetNRGBA(x, y, color.NRGBA{uint8(x*53 + 7), uint8(y*91 + 3), uint8(x*y + 100), a})
		}
	}
	return im
}

// dib uretir: baslik + (varsa maske) + pikseller.
func mkDIB(w, h, bpp int, comp uint32, hsz int, masks [4]uint32, topDown bool, px func(x, y int) []byte) []byte {
	le := binary.LittleEndian
	hdr := make([]byte, hsz)
	le.PutUint32(hdr[0:], uint32(hsz))
	le.PutUint32(hdr[4:], uint32(int32(w)))
	hh := int32(h)
	if topDown {
		hh = -hh
	}
	le.PutUint32(hdr[8:], uint32(hh))
	le.PutUint16(hdr[12:], 1)
	le.PutUint16(hdr[14:], uint16(bpp))
	le.PutUint32(hdr[16:], comp)
	out := hdr
	if comp == biBitfields {
		if hsz == 40 {
			ext := make([]byte, 12)
			for i := 0; i < 3; i++ {
				le.PutUint32(ext[i*4:], masks[i])
			}
			out = append(out, ext...)
		} else {
			for i := 0; i < 4; i++ {
				le.PutUint32(out[40+i*4:], masks[i])
			}
		}
	}
	stride := (w*bpp + 31) / 32 * 4
	rows := make([][]byte, h)
	for y := 0; y < h; y++ {
		row := make([]byte, stride)
		for x := 0; x < w; x++ {
			copy(row[x*bpp/8:], px(x, y))
		}
		rows[y] = row
	}
	if topDown {
		for _, r := range rows {
			out = append(out, r...)
		}
	} else {
		for y := h - 1; y >= 0; y-- {
			out = append(out, rows[y]...)
		}
	}
	return out
}

func TestPNGDIBRoundTripOpaque(t *testing.T) {
	for _, sz := range [][2]int{{1, 1}, {2, 3}, {3, 2}, {5, 5}, {7, 4}, {16, 9}, {33, 17}} {
		src := testImage(sz[0], sz[1], false)
		dib, err := pngToDIB(encPNG(t, src))
		if err != nil {
			t.Fatal(sz, err)
		}
		if int(binary.LittleEndian.Uint16(dib[14:])) != 32 || len(dib) != 40+sz[0]*sz[1]*4 {
			t.Fatalf("%v: dib boyutu/bit %d", sz, len(dib))
		}
		back, err := dibToPNG(dib)
		if err != nil {
			t.Fatal(sz, err)
		}
		got := decPNG(t, back)
		if got.Bounds() != src.Bounds() {
			t.Fatalf("%v: sinir %v", sz, got.Bounds())
		}
		for y := 0; y < sz[1]; y++ {
			for x := 0; x < sz[0]; x++ {
				if got.NRGBAAt(x, y) != src.NRGBAAt(x, y) {
					t.Fatalf("%v: (%d,%d) %v != %v", sz, x, y, got.NRGBAAt(x, y), src.NRGBAAt(x, y))
				}
			}
		}
	}
}

func TestPNGToDIBAlphaCompositesOverWhite(t *testing.T) {
	src := image.NewNRGBA(image.Rect(0, 0, 2, 1))
	src.SetNRGBA(0, 0, color.NRGBA{255, 0, 0, 0}) // tam saydam kirmizi -> beyaz
	src.SetNRGBA(1, 0, color.NRGBA{0, 0, 0, 255}) // opak siyah
	dib, err := pngToDIB(encPNG(t, src))
	if err != nil {
		t.Fatal(err)
	}
	px := dib[40:]
	if px[0] != 255 || px[1] != 255 || px[2] != 255 {
		t.Fatalf("saydam piksel beyaz olmali: %v", px[:4])
	}
	if px[4] != 0 || px[5] != 0 || px[6] != 0 {
		t.Fatalf("opak siyah korunmali: %v", px[4:8])
	}
}

func TestDIB24PaddingAndOrder(t *testing.T) {
	for _, w := range []int{1, 2, 3, 4, 5} { // tek/cift genislik: satir dolgusu
		for _, topDown := range []bool{false, true} {
			h := 3
			colorAt := func(x, y int) color.NRGBA {
				return color.NRGBA{uint8(10 + x*20), uint8(30 + y*40), uint8(200 - x*10 - y), 255}
			}
			d := mkDIB(w, h, 24, biRGB, 40, [4]uint32{}, topDown, func(x, y int) []byte {
				c := colorAt(x, y)
				return []byte{c.B, c.G, c.R}
			})
			p, err := dibToPNG(d)
			if err != nil {
				t.Fatalf("w=%d td=%v: %v", w, topDown, err)
			}
			got := decPNG(t, p)
			for y := 0; y < h; y++ {
				for x := 0; x < w; x++ {
					if got.NRGBAAt(x, y) != colorAt(x, y) {
						t.Fatalf("w=%d td=%v (%d,%d): %v != %v", w, topDown, x, y, got.NRGBAAt(x, y), colorAt(x, y))
					}
				}
			}
		}
	}
}

func TestDIB32Bitfields(t *testing.T) {
	want := func(x, y int) color.NRGBA {
		return color.NRGBA{uint8(x * 60), uint8(y * 70), uint8(5 + x + y), uint8(100 + x*30)}
	}
	px := func(x, y int) []byte {
		c := want(x, y)
		var b [4]byte
		binary.LittleEndian.PutUint32(b[:], uint32(c.A)<<24|uint32(c.R)<<16|uint32(c.G)<<8|uint32(c.B))
		return b[:]
	}
	masks := [4]uint32{0x00FF0000, 0x0000FF00, 0x000000FF, 0xFF000000}
	for _, hsz := range []int{56, 108, 124} { // V5/V4: alfa maskeli
		d := mkDIB(3, 2, 32, biBitfields, hsz, masks, false, px)
		p, err := dibToPNG(d)
		if err != nil {
			t.Fatal(hsz, err)
		}
		got := decPNG(t, p)
		for y := 0; y < 2; y++ {
			for x := 0; x < 3; x++ {
				if got.NRGBAAt(x, y) != want(x, y) {
					t.Fatalf("hsz=%d (%d,%d): %v != %v", hsz, x, y, got.NRGBAAt(x, y), want(x, y))
				}
			}
		}
	}
	// 40 baslikli BI_BITFIELDS: alfa maskesi yok -> opak; RGB sirasi maskeden.
	rgbMasks := [4]uint32{0x000000FF, 0x0000FF00, 0x00FF0000, 0} // RGB sirasi (ters)
	d := mkDIB(2, 2, 32, biBitfields, 40, rgbMasks, true, func(x, y int) []byte {
		return []byte{byte(10 + x), byte(20 + y), byte(30), 0} // R=10+x, G=20+y, B=30
	})
	p, err := dibToPNG(d)
	if err != nil {
		t.Fatal(err)
	}
	got := decPNG(t, p)
	if c := got.NRGBAAt(1, 1); c != (color.NRGBA{11, 21, 30, 255}) {
		t.Fatalf("maske sirasi/opak: %v", c)
	}
}

func TestDIBAlphaMaskAllZeroIsOpaque(t *testing.T) {
	masks := [4]uint32{0x00FF0000, 0x0000FF00, 0x000000FF, 0xFF000000}
	d := mkDIB(2, 1, 32, biBitfields, 124, masks, false, func(x, y int) []byte { return []byte{1, 2, 3, 0} })
	p, err := dibToPNG(d)
	if err != nil {
		t.Fatal(err)
	}
	if c := decPNG(t, p).NRGBAAt(0, 0); c.A != 255 {
		t.Fatalf("alfa hep 0 ise opak sayilmali: %v", c)
	}
}

func TestDIBRejectsBad(t *testing.T) {
	ok := mkDIB(4, 4, 24, biRGB, 40, [4]uint32{}, false, func(x, y int) []byte { return []byte{1, 2, 3} })
	if _, err := dibToPNG(ok); err != nil {
		t.Fatal(err)
	}
	cases := map[string][]byte{
		"bos":      nil,
		"kisa":     ok[:20],
		"kesik":    ok[:len(ok)-1],
		"core":     append([]byte{12, 0, 0, 0}, make([]byte, 40)...),
		"baslik":   append([]byte{41, 0, 0, 0}, ok[4:]...),
		"8bit":     mkDIB(4, 4, 8, biRGB, 40, [4]uint32{}, false, func(x, y int) []byte { return []byte{1} }),
		"rle":      mkDIB(4, 4, 24, 1, 40, [4]uint32{}, false, func(x, y int) []byte { return []byte{1, 2, 3} }),
		"bf24":     mkDIB(4, 4, 24, biBitfields, 40, [4]uint32{0xFF, 0xFF00, 0xFF0000}, false, func(x, y int) []byte { return []byte{1, 2, 3} }),
		"maske0":   mkDIB(2, 2, 32, biBitfields, 40, [4]uint32{}, false, func(x, y int) []byte { return []byte{1, 2, 3, 4} }),
		"sifirgen": mkDIB(0, 4, 24, biRGB, 40, [4]uint32{}, false, func(x, y int) []byte { return nil }),
	}
	for name, d := range cases {
		if _, err := dibToPNG(d); err == nil {
			t.Errorf("%s reddedilmeli", name)
		}
	}
	// Boyut siniri: baslik devasa boyut iddia eder, veri yok.
	huge := append([]byte{}, ok[:40]...)
	binary.LittleEndian.PutUint32(huge[4:], 20000)
	if _, err := dibToPNG(huge); err != errClipImageTooLarge {
		t.Errorf("devasa genislik: %v", err)
	}
	binary.LittleEndian.PutUint32(huge[4:], 8000)
	binary.LittleEndian.PutUint32(huge[8:], 8000)
	if _, err := dibToPNG(huge); err != errClipImageTooLarge {
		t.Errorf("piksel siniri: %v", err)
	}
	// Veri eksik: dolgu hesabi tasmadan kesik olarak reddedilir.
	small := append([]byte{}, ok[:40]...)
	binary.LittleEndian.PutUint32(small[4:], 4000)
	binary.LittleEndian.PutUint32(small[8:], 4000)
	if _, err := dibToPNG(small); err == nil {
		t.Error("veri eksik reddedilmeli")
	}
}

func TestPNGToDIBRejects(t *testing.T) {
	if _, err := pngToDIB([]byte("png degil")); err == nil {
		t.Error("png degil reddedilmeli")
	}
	good := encPNG(t, testImage(4, 4, false))
	if _, err := pngToDIB(good[:len(good)-20]); err == nil {
		t.Error("kesik png reddedilmeli")
	}
	// IHDR devasa boyut iddia eder.
	big := append([]byte{}, good...)
	binary.BigEndian.PutUint32(big[16:], protocol.MaxClipImageDim+1)
	if _, err := pngToDIB(big); err != errClipImageTooLarge {
		t.Errorf("devasa: %v", err)
	}
}

func TestDIBToPNGTooLargeOutput(t *testing.T) {
	// Gurultulu buyuk gorsel PNG sinirini asar (8 MiB): rastgele piksel sikismaz.
	w, h := 2600, 1200 // 9.4 MB ham, sikismaz
	px := make([]byte, w*h*3)
	seed := uint32(12345)
	for i := range px {
		seed = seed*1664525 + 1013904223
		px[i] = byte(seed >> 24)
	}
	d := mkDIB(w, h, 24, biRGB, 40, [4]uint32{}, false, func(x, y int) []byte {
		i := (y*w + x) * 3
		return px[i : i+3]
	})
	if _, err := dibToPNG(d); err != errClipImageTooLarge {
		t.Fatalf("beklenen too large, gelen %v", err)
	}
}
