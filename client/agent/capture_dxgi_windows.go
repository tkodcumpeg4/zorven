//go:build windows

package agent

import (
	"errors"
	"fmt"
	"image"
	"os"
	"syscall"
	"time"
	"unsafe"
)

// Windows DXGI Desktop Duplication yakalama (COM, CGO'suz).
//
// Akis: CreateDXGIFactory1 -> adaptorler/cikislar -> kbinani monitor siniriyla
// eslesen cikis -> ayni adaptorde D3D11 cihazi (BGRA; olmazsa WARP) ->
// IDXGIOutput1::DuplicateOutput. Her turda AcquireNextFrame; kare dokusu tum
// masaustunun guncel halidir. Yalniz kirli dikdortgenler (+ tasima hedefleri)
// CPU okunabilir "staging" dokuya kopyalanir, Map edilir ve kalici RGBA
// tamponuna BGRA->RGBA cevrilir. Dokunulmayan bolgeler onceki turdan kalir.
//
// COM nesneleri comObj ile tutulur (unsafe.Pointer; uintptr'a erken cevrilmez).
// Her Open basarisiz olursa ya da Close cagrilirsa tum referanslar birakilir.
// Tum cagrilar yakalama goroutine'inden (OS thread'ine bagli) yapilir.

var (
	modD3D11Cap = syscall.NewLazyDLL("d3d11.dll")
	modDXGICap  = syscall.NewLazyDLL("dxgi.dll")

	procD3D11CreateDevice  = modD3D11Cap.NewProc("D3D11CreateDevice")
	procCreateDXGIFactory1 = modDXGICap.NewProc("CreateDXGIFactory1")

	iidDXGIFactory1 = syscall.GUID{Data1: 0x770aae78, Data2: 0xf26f, Data3: 0x4dba,
		Data4: [8]byte{0xa8, 0x29, 0x25, 0x3c, 0x83, 0xd1, 0xb3, 0x87}}
	iidDXGIOutput1 = syscall.GUID{Data1: 0x00cddea8, Data2: 0x939b, Data3: 0x4b83,
		Data4: [8]byte{0xa3, 0x40, 0xa6, 0x85, 0x22, 0x66, 0x66, 0xcc}}
	iidD3D11Texture2D = syscall.GUID{Data1: 0x6f15aaf2, Data2: 0xd208, Data3: 0x4e89,
		Data4: [8]byte{0x9a, 0xb4, 0x48, 0x95, 0x35, 0xd3, 0x4f, 0x9c}}
)

// comObj, bir COM arayuz isaretcisi (nil => gecersiz).
type comObj struct{ p unsafe.Pointer }

func (o comObj) ok() bool { return o.p != nil }

// call, vtable indisi idx olan metodu cagirir (this otomatik eklenir) ve ham
// HRESULT/donus degerini doner. args icindeki isaretciler heap'te (winDXGI
// alanlari) tutulur; uintptrescapes ile cagri boyunca canli kalir.
//
//go:uintptrescapes
func (o comObj) call(idx uintptr, args ...uintptr) uintptr {
	vtbl := *(*unsafe.Pointer)(o.p)
	fn := *(*uintptr)(unsafe.Add(vtbl, idx*unsafe.Sizeof(uintptr(0))))
	all := make([]uintptr, 0, len(args)+1)
	all = append(all, uintptr(o.p))
	all = append(all, args...)
	r, _, _ := syscall.SyscallN(fn, all...)
	return r
}

// release, Release() cagirir ve isaretciyi sifirlar (nil-guvenli).
func (o *comObj) release() {
	if o.p != nil {
		o.call(vtRelease)
		o.p = nil
	}
}

func comFailed(r uintptr) bool { return int32(r) < 0 }

// dxgiCand, bir (adaptor, cikis) cifti; ikisi de bir referans tutar.
type dxgiCand struct {
	adp, out comObj
	rect     image.Rectangle
	usable   bool
}

func (c *dxgiCand) release() {
	c.out.release()
	c.adp.release()
}

// winDXGI, dxgiSource'un Windows gerceklemesi. Heap'te tutulur: COM cikis
// parametreleri bu yapinin alanlaridir (adresleri sabit).
type winDXGI struct {
	dev, ctx, dup, staging comObj
	w, h                   int
	cur                    *image.RGBA
	full                   bool // sonraki kare tum ekrani kirli say (ilk kare / yeniden kurulum)

	// COM cikis parametreleri / ara tamponlar (heap).
	out1, out2, out3 unsafe.Pointer
	flLevel          uint32
	info             dxgiFrameInfo
	mapped           d3dMapped
	tdesc            d3dTex2DDesc
	odesc            dxgiOutputDesc
	ddesc            dxgiOutduplDesc
	req              uint32
	moves            []dxgiMoveRect
	dirtyBuf         []dxgiRect
}

func newWinDXGISource() dxgiSource { return &winDXGI{} }

// newPlatCapturer, oturum icin yakalayici dondurur:
//   - servis (oturum 0): kullanici oturumundaki yardimciya devreden yakalayici
//   - ZORVEN_SCREEN_CAPTURE=gdi|kbinani: yalniz o yol (goCapturer)
//   - varsayilan (Windows 8+): DXGI birincil + kbinani yedegi; cokme korumasi
//     DXGI'yi atlattiysa (bkz. capture_guard.go) ya da surum eskiyse kbinani.
//   - ZORVEN_SCREEN_CAPTURE=dxgi: DXGI'yi korumayi yok sayarak zorlar.
func newPlatCapturer() Capturer {
	if h := serviceHelper(); h != nil {
		return helperCapturer{h}
	}
	env := os.Getenv(envScreenCapture)
	g, allowed, _ := processGuard()
	allowed = allowed && winAtLeast8()
	if chooseCaptureMode(env, allowed) != "dxgi" {
		return goCapturer{}
	}
	c := newDXGICapturer(goCapturer{}, newWinDXGISource())
	c.guard = g
	return c
}

func (d *winDXGI) Close() {
	d.staging.release()
	d.dup.release()
	d.ctx.release()
	d.dev.release()
	d.cur = nil
	d.full = false
}

func (d *winDXGI) Open(display int, bounds image.Rectangle) (err error) {
	d.Close()
	defer func() {
		if err != nil {
			d.Close()
		}
	}()
	attachToInputDesktop()

	// 1) Adaptor/cikis numaralandirma.
	d.out1 = nil
	r, _, _ := procCreateDXGIFactory1.Call(uintptr(unsafe.Pointer(&iidDXGIFactory1)), uintptr(unsafe.Pointer(&d.out1)))
	if comFailed(r) {
		return classifyHRESULT("CreateDXGIFactory1", uint32(r))
	}
	fac := comObj{d.out1}
	var cands []*dxgiCand
	for ai := 0; ai < 16; ai++ {
		d.out1 = nil
		if comFailed(fac.call(vtFactoryEnumAdapters, uintptr(ai), uintptr(unsafe.Pointer(&d.out1)))) {
			break // DXGI_ERROR_NOT_FOUND: adaptor kalmadi
		}
		adp := comObj{d.out1}
		for oi := 0; oi < 16; oi++ {
			d.out1 = nil
			if comFailed(adp.call(vtAdapterEnumOutputs, uintptr(oi), uintptr(unsafe.Pointer(&d.out1)))) {
				break
			}
			out := comObj{d.out1}
			d.odesc = dxgiOutputDesc{}
			c := &dxgiCand{out: out}
			if !comFailed(out.call(vtOutputGetDesc, uintptr(unsafe.Pointer(&d.odesc)))) &&
				d.odesc.AttachedToDesktop != 0 &&
				(d.odesc.Rotation == dxgiRotationUnspec || d.odesc.Rotation == dxgiRotationIdentity) {
				c.rect = d.odesc.DesktopCoordinates.rect()
				c.usable = true
				adp.call(1) // AddRef: aday adaptoru ayri tutar
				c.adp = adp
			}
			cands = append(cands, c)
		}
		adp.release() // numaralandirma referansi
	}
	fac.release()

	var rects []image.Rectangle
	var usable []*dxgiCand
	for _, c := range cands {
		if c.usable {
			rects = append(rects, c.rect)
			usable = append(usable, c)
		}
	}
	pick := matchOutputRect(bounds, rects)
	var chosen *dxgiCand
	for _, c := range cands {
		if pick >= 0 && c == usable[pick] {
			chosen = c
		} else {
			c.release()
		}
	}
	if chosen == nil {
		return fmt.Errorf("dxgi: monitor %v icin cikis bulunamadi: %w", bounds, errDXGIUnsupported)
	}
	defer chosen.release()

	// 2) D3D11 cihazi: once secilen adaptorde donanim, olmazsa WARP.
	d.out1, d.out2 = nil, nil
	r = d3dCreateDevice(chosen.adp.p, d3dDriverUnknown, &d.out1, &d.flLevel, &d.out2)
	if comFailed(r) {
		d.out1, d.out2 = nil, nil
		r = d3dCreateDevice(nil, d3dDriverWarp, &d.out1, &d.flLevel, &d.out2)
		if comFailed(r) {
			return classifyHRESULT("D3D11CreateDevice", uint32(r))
		}
	}
	d.dev, d.ctx = comObj{d.out1}, comObj{d.out2}

	// 3) Duplication.
	d.out1 = nil
	if comFailed(chosen.out.call(vtQueryInterface, uintptr(unsafe.Pointer(&iidDXGIOutput1)), uintptr(unsafe.Pointer(&d.out1)))) {
		return errors.New("dxgi: IDXGIOutput1 yok: " + errDXGIUnsupported.Error())
	}
	out1 := comObj{d.out1}
	d.out2 = nil
	r = out1.call(vtOutput1DuplicateOutput, uintptr(d.dev.p), uintptr(unsafe.Pointer(&d.out2)))
	out1.release()
	if comFailed(r) {
		return classifyHRESULT("DuplicateOutput", uint32(r))
	}
	d.dup = comObj{d.out2}

	d.ddesc = dxgiOutduplDesc{}
	d.dup.call(vtDupGetDesc, uintptr(unsafe.Pointer(&d.ddesc)))
	if d.ddesc.Format != dxgiFormatB8G8R8A8 ||
		(d.ddesc.Rotation != dxgiRotationUnspec && d.ddesc.Rotation != dxgiRotationIdentity) ||
		d.ddesc.Width == 0 || d.ddesc.Height == 0 || d.ddesc.Width > 16384 || d.ddesc.Height > 16384 {
		return fmt.Errorf("dxgi: desteklenmeyen kip (bicim %d, donus %d, %dx%d): %w",
			d.ddesc.Format, d.ddesc.Rotation, d.ddesc.Width, d.ddesc.Height, errDXGIUnsupported)
	}
	d.w, d.h = int(d.ddesc.Width), int(d.ddesc.Height)

	// 4) CPU okunabilir staging dokusu.
	d.tdesc = d3dTex2DDesc{
		Width: uint32(d.w), Height: uint32(d.h), MipLevels: 1, ArraySize: 1,
		Format: dxgiFormatB8G8R8A8, SampleCount: 1,
		Usage: d3d11UsageStaging, CPUAccessFlags: d3d11CPUAccessRead,
	}
	d.out1 = nil
	if r = d.dev.call(vtDeviceCreateTex2D, uintptr(unsafe.Pointer(&d.tdesc)), 0, uintptr(unsafe.Pointer(&d.out1))); comFailed(r) {
		return classifyHRESULT("CreateTexture2D", uint32(r))
	}
	d.staging = comObj{d.out1}

	d.cur = image.NewRGBA(image.Rect(0, 0, d.w, d.h))
	d.full = true
	return nil
}

// d3dCreateDevice, D3D11CreateDevice sarmalayicisi (adapter nil olabilir).
func d3dCreateDevice(adapter unsafe.Pointer, driver uintptr, dev *unsafe.Pointer, fl *uint32, ctx *unsafe.Pointer) uintptr {
	r, _, _ := procD3D11CreateDevice.Call(
		uintptr(adapter), driver, 0, d3d11CreateBGRA, 0, 0, d3d11SDKVersion,
		uintptr(unsafe.Pointer(dev)), uintptr(unsafe.Pointer(fl)), uintptr(unsafe.Pointer(ctx)))
	return r
}

func (d *winDXGI) Next(timeout time.Duration) (dxgiResult, error) {
	if !d.dup.ok() {
		return dxgiResult{}, fmt.Errorf("dxgi: kurulu degil: %w", errDXGILost)
	}
	ms := uint32(timeout / time.Millisecond)
	d.out1 = nil
	d.info = dxgiFrameInfo{}
	r := d.dup.call(vtDupAcquireNext, uintptr(ms), uintptr(unsafe.Pointer(&d.info)), uintptr(unsafe.Pointer(&d.out1)))
	if uint32(r) == hrDXGIWaitTimeout {
		return dxgiResult{}, nil
	}
	if comFailed(r) {
		return dxgiResult{}, classifyHRESULT("AcquireNextFrame", uint32(r))
	}
	// Basarili Acquire: ReleaseFrame mutlaka cagrilmali (defer'ler LIFO: once
	// kaynak/doku referanslari, en son ReleaseFrame).
	defer d.dup.call(vtDupReleaseFrame)
	res := comObj{d.out1}
	defer res.release()

	if !d.full && (d.info.LastPresentTime == 0 || d.info.AccumulatedFrames == 0) {
		return dxgiResult{}, nil // yalniz imlec guncellemesi: masaustu degismedi
	}

	d.out2 = nil
	if comFailed(res.call(vtQueryInterface, uintptr(unsafe.Pointer(&iidD3D11Texture2D)), uintptr(unsafe.Pointer(&d.out2)))) {
		return dxgiResult{}, fmt.Errorf("dxgi: kare dokusu alinamadi: %w", errDXGILost)
	}
	tex := comObj{d.out2}
	defer tex.release()
	d.tdesc = d3dTex2DDesc{}
	tex.call(vtTex2DGetDesc, uintptr(unsafe.Pointer(&d.tdesc)))
	if int(d.tdesc.Width) != d.w || int(d.tdesc.Height) != d.h || d.tdesc.Format != dxgiFormatB8G8R8A8 {
		return dxgiResult{}, fmt.Errorf("dxgi: kare boyutu/bicimi degisti (%dx%d f%d): %w",
			d.tdesc.Width, d.tdesc.Height, d.tdesc.Format, errDXGILost)
	}

	var dirty []image.Rectangle
	if d.full {
		dirty = []image.Rectangle{image.Rect(0, 0, d.w, d.h)}
	} else {
		var ok bool
		dirty, ok = d.frameRects()
		if !ok {
			dirty = []image.Rectangle{image.Rect(0, 0, d.w, d.h)} // guvenli taraf
		}
	}
	if len(dirty) == 0 {
		return dxgiResult{}, nil
	}

	// GPU -> staging: az/kucuk bolgede yalniz bolgeler, aksi halde tum doku.
	if len(dirty) > 16 || rectsArea(dirty)*2 >= d.w*d.h {
		d.ctx.call(vtCtxCopyResource, uintptr(d.staging.p), uintptr(tex.p))
	} else {
		for _, rc := range dirty {
			box := d3dBox{Left: uint32(rc.Min.X), Top: uint32(rc.Min.Y), Right: uint32(rc.Max.X), Bottom: uint32(rc.Max.Y), Back: 1}
			d.ctx.call(vtCtxCopySubresource, uintptr(d.staging.p), 0, uintptr(box.Left), uintptr(box.Top), 0,
				uintptr(tex.p), 0, uintptr(unsafe.Pointer(&box)))
		}
	}
	d.mapped = d3dMapped{}
	if r = d.ctx.call(vtCtxMap, uintptr(d.staging.p), 0, d3d11MapRead, 0, uintptr(unsafe.Pointer(&d.mapped))); comFailed(r) {
		return dxgiResult{}, classifyHRESULT("Map", uint32(r))
	}
	pitch := int(d.mapped.RowPitch)
	if d.mapped.Data == nil || pitch < d.w*4 {
		d.ctx.call(vtCtxUnmap, uintptr(d.staging.p), 0)
		return dxgiResult{}, fmt.Errorf("dxgi: gecersiz Map sonucu (pitch %d): %w", pitch, errDXGILost)
	}
	src := unsafe.Slice((*byte)(d.mapped.Data), pitch*d.h)
	for _, rc := range dirty {
		so := rc.Min.Y*pitch + rc.Min.X*4
		do := rc.Min.Y*d.cur.Stride + rc.Min.X*4
		bgraToRGBA(d.cur.Pix[do:], src[so:], rc.Dx(), rc.Dy(), pitch, d.cur.Stride)
	}
	d.ctx.call(vtCtxUnmap, uintptr(d.staging.p), 0)
	d.full = false
	return dxgiResult{Changed: true, Img: d.cur, Dirty: dirty}, nil
}

// frameRects, karenin kirli ve tasima dikdortgenlerini okur ve birlestirir.
// Okunamazsa ok=false (cagiran tum ekrani kirli sayar).
func (d *winDXGI) frameRects() (rects []image.Rectangle, ok bool) {
	total := int(d.info.TotalMetadataBufferSize)
	if total == 0 {
		return nil, true // metadata yok: degisen bolge yok
	}
	moveSz, rectSz := int(unsafe.Sizeof(dxgiMoveRect{})), int(unsafe.Sizeof(dxgiRect{}))
	if cap(d.moves) < total/moveSz+1 {
		d.moves = make([]dxgiMoveRect, total/moveSz+1)
	}
	d.moves = d.moves[:cap(d.moves)]
	if cap(d.dirtyBuf) < total/rectSz+1 {
		d.dirtyBuf = make([]dxgiRect, total/rectSz+1)
	}
	d.dirtyBuf = d.dirtyBuf[:cap(d.dirtyBuf)]

	var moves []dxgiMove
	for try := 0; ; try++ {
		d.req = 0
		r := d.dup.call(vtDupGetMoveRects, uintptr(len(d.moves)*moveSz), uintptr(unsafe.Pointer(&d.moves[0])), uintptr(unsafe.Pointer(&d.req)))
		if uint32(r) == hrDXGIMoreData && try == 0 {
			d.moves = make([]dxgiMoveRect, int(d.req)/moveSz+1)
			continue
		}
		if comFailed(r) {
			return nil, false
		}
		n := int(d.req) / moveSz
		if n > len(d.moves) {
			return nil, false
		}
		for _, m := range d.moves[:n] {
			moves = append(moves, dxgiMove{Src: image.Pt(int(m.SrcX), int(m.SrcY)), Dst: m.Dst.rect()})
		}
		break
	}
	var dirty []image.Rectangle
	for try := 0; ; try++ {
		d.req = 0
		r := d.dup.call(vtDupGetDirtyRects, uintptr(len(d.dirtyBuf)*rectSz), uintptr(unsafe.Pointer(&d.dirtyBuf[0])), uintptr(unsafe.Pointer(&d.req)))
		if uint32(r) == hrDXGIMoreData && try == 0 {
			d.dirtyBuf = make([]dxgiRect, int(d.req)/rectSz+1)
			continue
		}
		if comFailed(r) {
			return nil, false
		}
		n := int(d.req) / rectSz
		if n > len(d.dirtyBuf) {
			return nil, false
		}
		for _, rc := range d.dirtyBuf[:n] {
			dirty = append(dirty, rc.rect())
		}
		break
	}
	return dxgiDirtyUnion(dirty, moves, d.w, d.h), true
}
