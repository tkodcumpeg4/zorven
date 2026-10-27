package agent

import (
	"errors"
	"fmt"
	"image"
	"testing"
	"time"
	"unsafe"
)

// --- ABI sabitleri (64 bit; Windows amd64/arm64 ile ayni yerlesim) -----------

func TestDXGIStructLayout(t *testing.T) {
	if unsafe.Sizeof(uintptr(0)) != 8 {
		t.Skip("yerlesim sabitleri 64 bit icin")
	}
	check := func(name string, got, want uintptr) {
		t.Helper()
		if got != want {
			t.Errorf("%s = %d, beklenen %d", name, got, want)
		}
	}
	check("sizeof RECT", unsafe.Sizeof(dxgiRect{}), 16)
	check("sizeof MOVE_RECT", unsafe.Sizeof(dxgiMoveRect{}), 24)
	check("MOVE_RECT.Dst", unsafe.Offsetof(dxgiMoveRect{}.Dst), 8)
	check("sizeof OUTPUT_DESC", unsafe.Sizeof(dxgiOutputDesc{}), 96)
	check("OUTPUT_DESC.DesktopCoordinates", unsafe.Offsetof(dxgiOutputDesc{}.DesktopCoordinates), 64)
	check("OUTPUT_DESC.Attached", unsafe.Offsetof(dxgiOutputDesc{}.AttachedToDesktop), 80)
	check("OUTPUT_DESC.Rotation", unsafe.Offsetof(dxgiOutputDesc{}.Rotation), 84)
	check("OUTPUT_DESC.Monitor", unsafe.Offsetof(dxgiOutputDesc{}.Monitor), 88)
	check("sizeof OUTDUPL_DESC", unsafe.Sizeof(dxgiOutduplDesc{}), 36)
	check("OUTDUPL_DESC.Format", unsafe.Offsetof(dxgiOutduplDesc{}.Format), 16)
	check("OUTDUPL_DESC.Rotation", unsafe.Offsetof(dxgiOutduplDesc{}.Rotation), 28)
	check("sizeof FRAME_INFO", unsafe.Sizeof(dxgiFrameInfo{}), 48)
	check("FRAME_INFO.Accumulated", unsafe.Offsetof(dxgiFrameInfo{}.AccumulatedFrames), 16)
	check("FRAME_INFO.TotalMetadata", unsafe.Offsetof(dxgiFrameInfo{}.TotalMetadataBufferSize), 40)
	check("FRAME_INFO.PointerShape", unsafe.Offsetof(dxgiFrameInfo{}.PointerShapeBufferSize), 44)
	check("sizeof TEXTURE2D_DESC", unsafe.Sizeof(d3dTex2DDesc{}), 44)
	check("TEXTURE2D_DESC.Format", unsafe.Offsetof(d3dTex2DDesc{}.Format), 16)
	check("TEXTURE2D_DESC.Usage", unsafe.Offsetof(d3dTex2DDesc{}.Usage), 28)
	check("TEXTURE2D_DESC.CPUAccessFlags", unsafe.Offsetof(d3dTex2DDesc{}.CPUAccessFlags), 36)
	check("sizeof MAPPED_SUBRESOURCE", unsafe.Sizeof(d3dMapped{}), 16)
	check("MAPPED.RowPitch", unsafe.Offsetof(d3dMapped{}.RowPitch), 8)
	check("sizeof BOX", unsafe.Sizeof(d3dBox{}), 24)
}

func TestDXGIVtableIndices(t *testing.T) {
	// d3d11.h / dxgi*.h bildirim sirasindan elle turetilen indisler.
	want := map[string]int{
		"EnumAdapters": 7, "EnumOutputs": 7, "Output.GetDesc": 7, "DuplicateOutput": 22,
		"Dup.AcquireNextFrame": 8, "Dup.GetFrameDirtyRects": 9, "Dup.GetFrameMoveRects": 10, "Dup.ReleaseFrame": 14,
		"CreateTexture2D": 5, "Map": 14, "Unmap": 15, "CopySubresourceRegion": 46, "CopyResource": 47, "Tex2D.GetDesc": 10,
	}
	got := map[string]int{
		"EnumAdapters": vtFactoryEnumAdapters, "EnumOutputs": vtAdapterEnumOutputs, "Output.GetDesc": vtOutputGetDesc,
		"DuplicateOutput": vtOutput1DuplicateOutput, "Dup.AcquireNextFrame": vtDupAcquireNext,
		"Dup.GetFrameDirtyRects": vtDupGetDirtyRects, "Dup.GetFrameMoveRects": vtDupGetMoveRects,
		"Dup.ReleaseFrame": vtDupReleaseFrame, "CreateTexture2D": vtDeviceCreateTex2D, "Map": vtCtxMap,
		"Unmap": vtCtxUnmap, "CopySubresourceRegion": vtCtxCopySubresource, "CopyResource": vtCtxCopyResource,
		"Tex2D.GetDesc": vtTex2DGetDesc,
	}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("%s indisi %d, beklenen %d", k, got[k], v)
		}
	}
}

func TestClassifyHRESULT(t *testing.T) {
	cases := []struct {
		hr   uint32
		lost bool
		unsp bool
	}{
		{hrDXGIAccessLost, true, false},
		{hrDXGIDeviceRemoved, true, false},
		{hrAccessDenied, true, false},
		{hrDXGIUnsupported, false, true},
		{hrDXGINotCurrentlyAvail, false, true},
		{0x80004005, false, false},
	}
	for _, c := range cases {
		err := classifyHRESULT("op", c.hr)
		if errors.Is(err, errDXGILost) != c.lost || errors.Is(err, errDXGIUnsupported) != c.unsp {
			t.Errorf("0x%08X: %v", c.hr, err)
		}
	}
}

// --- kirli dikdortgen mantigi ------------------------------------------------

func TestDXGIDirtyUnion(t *testing.T) {
	// Tasima hedefi kirli sayilir; kaynak eklenmez; ekran disi kirpilir.
	moves := []dxgiMove{{Src: image.Pt(0, 0), Dst: image.Rect(100, 100, 200, 200)}}
	got := dxgiDirtyUnion([]image.Rectangle{image.Rect(500, 500, 600, 600), image.Rect(-10, -10, 5, 5), image.Rect(1900, 1000, 2100, 1200)}, moves, 1920, 1080)
	want := map[image.Rectangle]bool{
		image.Rect(100, 100, 200, 200):     true,
		image.Rect(500, 500, 600, 600):     true,
		image.Rect(0, 0, 5, 5):             true,
		image.Rect(1900, 1000, 1920, 1080): true,
	}
	if len(got) != len(want) {
		t.Fatalf("got %v", got)
	}
	for _, r := range got {
		if !want[r] {
			t.Errorf("beklenmeyen %v", r)
		}
	}
	// Bos / ters dikdortgenler elenir; ters olan duzeltilir.
	got = dxgiDirtyUnion([]image.Rectangle{{Min: image.Pt(10, 10), Max: image.Pt(10, 20)}, {Min: image.Pt(30, 30), Max: image.Pt(20, 20)}}, nil, 100, 100)
	if len(got) != 1 || got[0] != image.Rect(20, 20, 30, 30) {
		t.Fatalf("got %v", got)
	}
	if len(dxgiDirtyUnion(nil, nil, 100, 100)) != 0 {
		t.Fatal("bos girdi bos cikti vermeli")
	}
}

func TestCoalesceRectsSupersetAndLimit(t *testing.T) {
	rs := []image.Rectangle{image.Rect(0, 0, 10, 10), image.Rect(10, 0, 20, 10), image.Rect(50, 50, 60, 60), image.Rect(5, 5, 15, 15)}
	out := coalesceRects(rs, 8)
	if len(out) != 2 {
		t.Fatalf("2 grup bekleniyordu: %v", out)
	}
	for _, r := range rs { // her girdi bir ciktinin icinde kalmali
		in := false
		for _, o := range out {
			if r.In(o) {
				in = true
			}
		}
		if !in {
			t.Errorf("%v kapsanmadi: %v", r, out)
		}
	}
	// Limit asilirsa tek sinirlayici kutu.
	var many []image.Rectangle
	for i := 0; i < 20; i++ {
		many = append(many, image.Rect(i*100, i*50, i*100+10, i*50+10))
	}
	out = coalesceRects(many, 8)
	if len(out) != 1 || out[0] != image.Rect(0, 0, 1910, 960) {
		t.Fatalf("sinirlayici kutu: %v", out)
	}
}

func TestMatchOutputRect(t *testing.T) {
	a, b := image.Rect(0, 0, 1920, 1080), image.Rect(1920, 0, 3840, 1080)
	if matchOutputRect(b, []image.Rectangle{a, b}) != 1 || matchOutputRect(a, []image.Rectangle{a, b}) != 0 {
		t.Fatal("tam eslesme")
	}
	// DPI sanallastirmasi: boyut farkli, sol-ust kose ayni.
	if matchOutputRect(image.Rect(1920, 0, 3000, 800), []image.Rectangle{a, b}) != 1 {
		t.Fatal("kose eslesmesi")
	}
	// Konum farkli, boyut tek aday.
	if matchOutputRect(image.Rect(7, 7, 7+1280, 7+720), []image.Rectangle{a, image.Rect(0, 0, 1280, 720)}) != 1 {
		t.Fatal("tek boyut adayi")
	}
	// Belirsiz boyut => eslesme yok.
	if matchOutputRect(image.Rect(7, 7, 7+1920, 7+1080), []image.Rectangle{a, b}) != -1 {
		t.Fatal("belirsiz eslesme -1 olmali")
	}
	if matchOutputRect(a, nil) != -1 {
		t.Fatal("bos")
	}
}

// --- geri dusus durum makinesi -----------------------------------------------

type fakeDXGISrc struct {
	openErrs []error // sirayla; bitince nil (basari)
	nextRes  []fakeNext
	opens    int
	closes   int
	nexts    int
	cur      *image.RGBA
}

type fakeNext struct {
	res dxgiResult
	err error
}

func (f *fakeDXGISrc) Open(int, image.Rectangle) error {
	f.opens++
	if len(f.openErrs) > 0 {
		e := f.openErrs[0]
		f.openErrs = f.openErrs[1:]
		return e
	}
	return nil
}

func (f *fakeDXGISrc) Next(time.Duration) (dxgiResult, error) {
	f.nexts++
	if len(f.nextRes) == 0 {
		return dxgiResult{}, nil
	}
	n := f.nextRes[0]
	f.nextRes = f.nextRes[1:]
	return n.res, n.err
}

func (f *fakeDXGISrc) Close() { f.closes++ }

type fbCap struct{ n int }

func (*fbCap) NumDisplays() int           { return 1 }
func (*fbCap) Bounds(int) image.Rectangle { return image.Rect(0, 0, 64, 64) }
func (f *fbCap) Capture(int) (*image.RGBA, error) {
	f.n++
	return image.NewRGBA(image.Rect(0, 0, 64, 64)), nil
}
func (*fbCap) Method() string { return "gdi" }

func frameRes(dirty ...image.Rectangle) fakeNext {
	img := image.NewRGBA(image.Rect(0, 0, 64, 64))
	return fakeNext{res: dxgiResult{Changed: true, Img: img, Dirty: dirty}}
}

func TestDXGICapturerHappyPathAndTimeout(t *testing.T) {
	src := &fakeDXGISrc{nextRes: []fakeNext{frameRes(image.Rect(0, 0, 64, 64)), {}, frameRes(image.Rect(1, 1, 5, 5))}}
	fb := &fbCap{}
	c := newDXGICapturer(fb, src)

	img, hint, hinted, err := c.CaptureHinted(0)
	if err != nil || !hinted || img == nil || len(hint) != 1 {
		t.Fatalf("ilk kare: %v %v %v %v", img != nil, hint, hinted, err)
	}
	if c.Method() != "dxgi" {
		t.Fatalf("yontem %q", c.Method())
	}
	// Anlik goruntu kopyadir: kaynak tamponu degisse de etkilenmez.
	img2, hint2, hinted2, _ := c.CaptureHinted(0) // zaman asimi
	if img2 != nil || len(hint2) != 0 || !hinted2 {
		t.Fatal("zaman asiminda degisim yok (img nil, hinted) bekleniyordu")
	}
	_, hint3, hinted3, _ := c.CaptureHinted(0)
	if !hinted3 || len(hint3) != 1 || hint3[0] != image.Rect(1, 1, 5, 5) {
		t.Fatalf("hint3=%v", hint3)
	}
	if fb.n != 0 || src.opens != 1 {
		t.Fatalf("yedek %d, open %d", fb.n, src.opens)
	}
}

func TestDXGICapturerSnapshotIsCopy(t *testing.T) {
	r := frameRes(image.Rect(0, 0, 64, 64))
	src := &fakeDXGISrc{nextRes: []fakeNext{r}}
	c := newDXGICapturer(&fbCap{}, src)
	img, _, _, _ := c.CaptureHinted(0)
	r.res.Img.Pix[0] = 99
	if img.Pix[0] == 99 {
		t.Fatal("dondurulen goruntu kaynak tamponunu paylasiyor")
	}
}

func TestDXGICapturerNotPrimedUsesFallback(t *testing.T) {
	// Acildi ama ilk DXGI karesi gelmedi (zaman asimi): bu tur GDI ile tam kare.
	src := &fakeDXGISrc{}
	fb := &fbCap{}
	c := newDXGICapturer(fb, src)
	img, _, hinted, err := c.CaptureHinted(0)
	if err != nil || img == nil || hinted || fb.n != 1 {
		t.Fatalf("img=%v hinted=%v fb=%d err=%v", img != nil, hinted, fb.n, err)
	}
	if c.Method() != "gdi" {
		t.Fatalf("yontem %q", c.Method())
	}
}

func TestDXGICapturerAccessLostReopens(t *testing.T) {
	src := &fakeDXGISrc{nextRes: []fakeNext{
		frameRes(image.Rect(0, 0, 64, 64)),
		{err: fmt.Errorf("x: %w", errDXGILost)},
		frameRes(image.Rect(0, 0, 64, 64)),
	}}
	fb := &fbCap{}
	c := newDXGICapturer(fb, src)
	c.CaptureHinted(0)
	// Kayip: bu tur yedek (hint yok), kaynak kapatilir.
	img, _, hinted, _ := c.CaptureHinted(0)
	if img == nil || hinted || fb.n != 1 || src.closes != 1 {
		t.Fatalf("kayip turu: img=%v hinted=%v fb=%d closes=%d", img != nil, hinted, fb.n, src.closes)
	}
	// Sonraki tur yeniden kurar ve DXGI'ye doner (tum ekran ipucu).
	img, hint, hinted, _ := c.CaptureHinted(0)
	if img == nil || !hinted || len(hint) != 1 || src.opens != 2 {
		t.Fatalf("yeniden kurulum: hinted=%v hint=%v opens=%d", hinted, hint, src.opens)
	}
	if c.Method() != "dxgi" {
		t.Fatal("DXGI'ye donulmeli")
	}
}

func TestDXGICapturerFallsBackAndRetries(t *testing.T) {
	now := time.Unix(1000, 0)
	// 3 ardisik Open hatasi => 30 sn yedek.
	src := &fakeDXGISrc{openErrs: []error{errors.New("e1"), errors.New("e2"), errors.New("e3")}}
	fb := &fbCap{}
	c := newDXGICapturer(fb, src)
	c.now = func() time.Time { return now }
	for i := 0; i < 3; i++ {
		img, _, hinted, err := c.CaptureHinted(0)
		if err != nil || img == nil || hinted {
			t.Fatalf("tur %d", i)
		}
	}
	if src.opens != 3 {
		t.Fatalf("opens=%d", src.opens)
	}
	// Yedek penceresinde DXGI hic denenmez.
	now = now.Add(29 * time.Second)
	c.CaptureHinted(0)
	if src.opens != 3 {
		t.Fatal("yedek suresince Open denenmemeli")
	}
	// 30 sn sonra yeniden dener (artik basarili).
	now = now.Add(2 * time.Second)
	src.nextRes = []fakeNext{frameRes(image.Rect(0, 0, 64, 64))}
	_, hint, hinted, _ := c.CaptureHinted(0)
	if src.opens != 4 || !hinted || len(hint) != 1 {
		t.Fatalf("opens=%d hinted=%v", src.opens, hinted)
	}
	if c.LastError() == nil {
		t.Fatal("son hata kaydedilmeliydi")
	}
}

func TestDXGICapturerUnsupportedFallsBackImmediatelyWithBackoff(t *testing.T) {
	now := time.Unix(1000, 0)
	src := &fakeDXGISrc{openErrs: []error{errDXGIUnsupported, errDXGIUnsupported}}
	c := newDXGICapturer(&fbCap{}, src)
	c.now = func() time.Time { return now }
	c.CaptureHinted(0)
	c.CaptureHinted(0) // yedek penceresinde: Open yok
	if src.opens != 1 {
		t.Fatalf("opens=%d", src.opens)
	}
	now = now.Add(31 * time.Second)
	c.CaptureHinted(0) // ikinci hata: bekleme 60 sn'ye cikar
	now = now.Add(31 * time.Second)
	c.CaptureHinted(0)
	if src.opens != 2 {
		t.Fatalf("ussel artis: opens=%d", src.opens)
	}
	now = now.Add(31 * time.Second)
	c.CaptureHinted(0)
	if src.opens != 3 {
		t.Fatalf("opens=%d", src.opens)
	}
}

func TestDXGICapturerDisplayChangeReopens(t *testing.T) {
	src := &fakeDXGISrc{nextRes: []fakeNext{frameRes(image.Rect(0, 0, 64, 64)), frameRes(image.Rect(0, 0, 64, 64))}}
	c := newDXGICapturer(&fbCap{}, src)
	c.CaptureHinted(0)
	c.CaptureHinted(1)
	if src.opens != 2 || src.closes != 1 {
		t.Fatalf("opens=%d closes=%d", src.opens, src.closes)
	}
}

type fakeGuard struct{ begins, oks, clears int }

func (g *fakeGuard) Begin() error { g.begins++; return nil }
func (g *fakeGuard) FrameOK()     { g.oks++ }
func (g *fakeGuard) Clear()       { g.clears++ }

func TestDXGICapturerGuardHooks(t *testing.T) {
	src := &fakeDXGISrc{nextRes: []fakeNext{frameRes(image.Rect(0, 0, 64, 64)), frameRes(image.Rect(0, 0, 64, 64))}}
	c := newDXGICapturer(&fbCap{}, src)
	g := &fakeGuard{}
	c.guard = g
	c.CaptureHinted(0)
	c.CaptureHinted(0)
	if g.begins != 1 || g.oks != 2 {
		t.Fatalf("begins=%d oks=%d", g.begins, g.oks)
	}
	c.Close()
	if g.clears != 1 {
		t.Fatalf("clears=%d", g.clears)
	}
}
