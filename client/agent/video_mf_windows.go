//go:build windows

package agent

import (
	"errors"
	"fmt"
	"sync"
	"syscall"
	"time"
	"unsafe"
)

// Windows Media Foundation H.264 kodlayici (COM, CGO'suz, ek kurulum yok).
//
// Sira: once donanim kodlayicilar (MFTEnumEx, MFT_ENUM_FLAG_HARDWARE; senkron
// ve asenkron MFT'ler), olmazsa Microsoft'un yazilim kodlayicisi
// (CLSID_MSH264EncoderMFT, senkron). Hepsi NV12 sistem bellegi ornegi alir.
//
// Dusuk gecikme: CODECAPI_AVLowLatencyMode, B-kare yok, CBR, constrained
// baseline profil (tarayici uyumu), GOP ~2 sn; IDR istegi
// CODECAPI_AVEncVideoForceKeyFrame, bit hizi CODECAPI_AVEncCommonMeanBitRate
// ile canli degisir.
//
// DOGRULAMA: bu kod Linux'ta derlenir ama GERCEK Windows makinesinde
// denenmedi (vtable indisleri/GUID'ler Windows SDK basliklarindan elle
// girildi). Hata halinde NewEncoder hata doner ve istemci karo modunda kalir
// (screen2_video ilan edilmez); bkz. rapor/manuel test adimlari.
//
// COM arayuz vtable indisleri (IUnknown 0..2):
//
//	IMFAttributes: GetUINT32 7, SetGUID 24, SetUINT32 21, SetUINT64 22
//	IMFMediaType : IMFAttributes + 33..37
//	IMFSample    : IMFAttributes + GetSampleFlags 33 .. SetSampleTime 36,
//	               SetSampleDuration 38, AddBuffer 42, ConvertToContiguousBuffer 41
//	IMFMediaBuffer: Lock 3, Unlock 4, SetCurrentLength 6
//	IMFTransform : GetOutputStreamInfo 7, GetAttributes 8, GetOutputAvailableType 14,
//	               SetInputType 15, SetOutputType 16, ProcessMessage 23,
//	               ProcessInput 24, ProcessOutput 25
//	ICodecAPI    : SetValue 9
//	IMFMediaEventGenerator: GetEvent 3;  IMFMediaEvent: GetType 33
//	IMFActivate  : ActivateObject 33, ShutdownObject 34
const (
	vtAttrGetUINT32 = 7
	vtAttrSetGUID   = 24
	vtAttrSetUINT32 = 21
	vtAttrSetUINT64 = 22

	vtSampleConvertContig = 41
	vtSampleAddBuffer     = 42
	vtSampleSetTime       = 36
	vtSampleSetDuration   = 38

	vtBufLock      = 3
	vtBufUnlock    = 4
	vtBufSetCurLen = 6

	vtMftOutStreamInfo = 7
	vtMftGetAttributes = 8
	vtMftOutAvailType  = 14
	vtMftSetInputType  = 15
	vtMftSetOutputType = 16
	vtMftProcessMsg    = 23
	vtMftProcessInput  = 24
	vtMftProcessOutput = 25

	vtCodecSetValue = 9

	vtEvGenGetEvent = 3
	vtEventGetType  = 33

	vtActivateObject   = 33
	vtActivateShutdown = 34
)

// MFT ileti/olay sabitleri.
const (
	mftMsgFlush          = 0
	mftMsgBeginStreaming = 0x10000000
	mftMsgEndOfStream    = 0x10000002
	mftMsgStartOfStream  = 0x10000003

	meTransformNeedInput  = 601
	meTransformHaveOutput = 602

	mftOutputProvidesSamples = 0x100

	mfVersion = 0x20070 // MF_SDK_VERSION 2, MF_API_VERSION 0x70

	enumSync     = 0x1
	enumAsync    = 0x2
	enumHardware = 0x4
	enumSortFilt = 0x40

	vtUI4 = 19

	h264ProfileBase = 66
	h264ProfileMain = 77
)

const (
	hrMFNotAccepting      uint32 = 0xC00D36B5
	hrMFNeedMoreInput     uint32 = 0xC00D6D72
	hrMFStreamChange      uint32 = 0xC00D6D61
	hrMFNoEventsAvailable uint32 = 0xC00D3E80
)

var (
	modMfplat = syscall.NewLazyDLL("mfplat.dll")
	modOle32  = syscall.NewLazyDLL("ole32.dll")

	procMFStartup            = modMfplat.NewProc("MFStartup")
	procMFCreateMediaType    = modMfplat.NewProc("MFCreateMediaType")
	procMFCreateSample       = modMfplat.NewProc("MFCreateSample")
	procMFCreateMemoryBuffer = modMfplat.NewProc("MFCreateMemoryBuffer")
	procMFTEnumEx            = modMfplat.NewProc("MFTEnumEx")
	procCoInitializeEx       = modOle32.NewProc("CoInitializeEx")
	procCoCreateInstance     = modOle32.NewProc("CoCreateInstance")
	procCoTaskMemFree        = modOle32.NewProc("CoTaskMemFree")
)

func guid(d1 uint32, d2, d3 uint16, d4 ...byte) syscall.GUID {
	g := syscall.GUID{Data1: d1, Data2: d2, Data3: d3}
	copy(g.Data4[:], d4)
	return g
}

var (
	mfMTMajor       = guid(0x48eba18e, 0xf8c9, 0x4687, 0xbf, 0x11, 0x0a, 0x74, 0xc9, 0xf9, 0x6a, 0x8f)
	mfMTSubtype     = guid(0xf7e34c9a, 0x42e8, 0x4714, 0xb7, 0x4b, 0xcb, 0x29, 0xd7, 0x2c, 0x35, 0xe5)
	mfMTAvgBitrate  = guid(0x20332624, 0xfb0d, 0x4d9e, 0xbd, 0x0d, 0xcb, 0xf6, 0x78, 0x6c, 0x10, 0x2e)
	mfMTFrameSize   = guid(0x1652c33d, 0xd6b2, 0x4012, 0xb8, 0x34, 0x72, 0x03, 0x08, 0x49, 0xa3, 0x7d)
	mfMTFrameRate   = guid(0xc459a2e8, 0x3d2c, 0x4e44, 0xb1, 0x32, 0xfe, 0xe5, 0x15, 0x6c, 0x7b, 0xb0)
	mfMTPixelAspect = guid(0xc6376a1e, 0x8d0a, 0x4027, 0xbe, 0x45, 0x6d, 0x9a, 0x0a, 0xd3, 0x9b, 0xb6)
	mfMTInterlace   = guid(0xe2724bb8, 0xe676, 0x4806, 0xb4, 0xb2, 0xa8, 0xd6, 0xef, 0xb4, 0x4c, 0xcd)
	mfMTMpeg2Prof   = guid(0xad76a80b, 0x2d5c, 0x4e0b, 0xb3, 0x75, 0x64, 0xe5, 0x20, 0x13, 0x70, 0x36)

	mfTransformAsync       = guid(0xf81a699a, 0x649a, 0x497d, 0x8c, 0x73, 0x29, 0xf8, 0xfe, 0xd6, 0xad, 0x7a)
	mfTransformAsyncUnlock = guid(0xe5666d6b, 0x3422, 0x4eb6, 0xa4, 0x21, 0xda, 0x7d, 0xb1, 0xf8, 0xe4, 0x0f)

	mfMediaTypeVideo = guid(0x73646976, 0x0000, 0x0010, 0x80, 0x00, 0x00, 0xaa, 0x00, 0x38, 0x9b, 0x71)
	mfFormatH264     = guid(0x34363248, 0x0000, 0x0010, 0x80, 0x00, 0x00, 0xaa, 0x00, 0x38, 0x9b, 0x71)
	mfFormatNV12     = guid(0x3231564E, 0x0000, 0x0010, 0x80, 0x00, 0x00, 0xaa, 0x00, 0x38, 0x9b, 0x71)

	mftCategoryVideoEncoder = guid(0xf79eac7d, 0xe545, 0x4387, 0xbd, 0xee, 0xd6, 0x47, 0xd7, 0xbd, 0xe4, 0x2a)
	clsidMSH264Encoder      = guid(0x6ca50344, 0x051a, 0x4ded, 0x97, 0x79, 0xa4, 0x33, 0x05, 0x16, 0x5e, 0x35)

	iidIMFTransform           = guid(0xbf94c121, 0x5b05, 0x4e6f, 0x80, 0x00, 0xba, 0x59, 0x89, 0x61, 0x41, 0x4d)
	iidICodecAPI              = guid(0x901db4c7, 0x31ce, 0x41a2, 0x85, 0xdc, 0x8f, 0xa0, 0xbf, 0x41, 0xb8, 0xda)
	iidIMFMediaEventGenerator = guid(0x2cd0bd52, 0xbcd5, 0x4b89, 0xb6, 0x2c, 0xea, 0xdc, 0x0c, 0x03, 0x1e, 0x7d)

	capLowLatency    = guid(0x9c27891a, 0xed7a, 0x40e1, 0x88, 0xe8, 0xb2, 0x27, 0x27, 0xa0, 0x24, 0xee)
	capRateControl   = guid(0x1c0608e9, 0x370c, 0x4710, 0x8a, 0x58, 0xcb, 0x61, 0x81, 0xc4, 0x24, 0x23)
	capMeanBitRate   = guid(0xf7222374, 0x2144, 0x4815, 0xb5, 0x50, 0xa3, 0x7f, 0x8e, 0x12, 0xee, 0x52)
	capGOPSize       = guid(0x95f31b26, 0x95a4, 0x41aa, 0x93, 0x03, 0x24, 0x6a, 0x7f, 0xc6, 0xee, 0xf1)
	capBPictureCnt   = guid(0x8d390aac, 0xdc5c, 0x4200, 0xb5, 0x7f, 0x81, 0x4d, 0x04, 0xba, 0xba, 0xb2)
	capForceKeyFrame = guid(0x398c1b98, 0x8353, 0x475a, 0x9e, 0xf2, 0x8f, 0x26, 0x5d, 0x26, 0x03, 0x45)
)

// mfObj, bir COM arayuz isaretcisi.
type mfObj struct{ p unsafe.Pointer }

func (o mfObj) ok() bool { return o.p != nil }

// call, vtable indisi idx olan metodu cagirir (this otomatik eklenir).
//
//go:uintptrescapes
func (o mfObj) call(idx uintptr, args ...uintptr) uintptr {
	vtbl := *(*unsafe.Pointer)(o.p)
	fn := *(*uintptr)(unsafe.Add(vtbl, idx*unsafe.Sizeof(uintptr(0))))
	all := make([]uintptr, 0, len(args)+1)
	all = append(all, uintptr(o.p))
	all = append(all, args...)
	r, _, _ := syscall.SyscallN(fn, all...)
	return r
}

func (o *mfObj) release() {
	if o.p != nil {
		o.call(2)
		o.p = nil
	}
}

//go:uintptrescapes
func mfDLL(p *syscall.LazyProc, args ...uintptr) uintptr {
	r, _, _ := p.Call(args...)
	return r
}

func mfFailed(r uintptr) bool { return int32(r) < 0 }

func mfErr(what string, r uintptr) error { return fmt.Errorf("%s: HRESULT 0x%08x", what, uint32(r)) }

func (o mfObj) qi(iid *syscall.GUID) (mfObj, bool) {
	var out unsafe.Pointer
	if mfFailed(o.call(0, uintptr(unsafe.Pointer(iid)), uintptr(unsafe.Pointer(&out)))) || out == nil {
		return mfObj{}, false
	}
	return mfObj{out}, true
}

var (
	mfStartOnce sync.Once
	mfStartErr  error
)

// coInitMTA, bu thread icin COM'u MTA olarak baslatir (ikinci cagri S_FALSE;
// farkli kip RPC_E_CHANGED_MODE -- ikisi de yok sayilir).
func coInitMTA() { mfDLL(procCoInitializeEx, 0, 0) }

func mfStartup() error {
	mfStartOnce.Do(func() {
		if unsafe.Sizeof(uintptr(0)) != 8 {
			mfStartErr = errors.New("H.264 kodlayici yalniz 64-bit Windows'ta desteklenir")
			return
		}
		if r := mfDLL(procMFStartup, mfVersion, 0); mfFailed(r) {
			mfStartErr = mfErr("MFStartup", r)
		}
	})
	return mfStartErr
}

// platVideoThreadInit, kodlama goroutine'inin OS thread'inde COM'u baslatir.
func platVideoThreadInit() { coInitMTA() }

type mfEncoder struct {
	name   string
	mft    mfObj
	codec  mfObj
	evGen  mfObj
	act    mfObj // donanim: IMFActivate
	async  bool
	w, h   int
	frmDur int64

	outProvides bool
	outSize     int
	outSample   mfObj
	outBuf      mfObj
	needInput   int
}

type mftOutputDataBuffer struct {
	StreamID uint32
	_        uint32
	Sample   unsafe.Pointer
	Status   uint32
	_        uint32
	Events   unsafe.Pointer
}

type mfVariant struct {
	VT  uint16
	_   [3]uint16
	Val uint64
	_   uint64
}

func platNewVideoEncoder(w, h, fps, bps int, preferSW bool) (videoEncoder, error) {
	coInitMTA()
	if err := mfStartup(); err != nil {
		return nil, err
	}
	var hwErr error
	if !preferSW {
		enc, err := newMFHardware(w, h, fps, bps)
		if err == nil {
			return enc, nil
		}
		hwErr = err
	}
	enc, err := newMFSoftware(w, h, fps, bps)
	if err != nil {
		if hwErr != nil {
			return nil, fmt.Errorf("donanim: %v; yazilim: %w", hwErr, err)
		}
		return nil, err
	}
	return enc, nil
}

func newMFSoftware(w, h, fps, bps int) (videoEncoder, error) {
	var p unsafe.Pointer
	r := mfDLL(procCoCreateInstance, uintptr(unsafe.Pointer(&clsidMSH264Encoder)), 0, 1, // CLSCTX_INPROC_SERVER
		uintptr(unsafe.Pointer(&iidIMFTransform)), uintptr(unsafe.Pointer(&p)))
	if mfFailed(r) || p == nil {
		return nil, mfErr("CoCreateInstance(MSH264EncoderMFT)", r)
	}
	e := &mfEncoder{name: "mf-sw", mft: mfObj{p}}
	if err := e.setup(w, h, fps, bps); err != nil {
		e.Close()
		return nil, err
	}
	return e, nil
}

func newMFHardware(w, h, fps, bps int) (videoEncoder, error) {
	out := struct{ major, sub syscall.GUID }{mfMediaTypeVideo, mfFormatH264}
	var arr unsafe.Pointer
	var n uint32
	r := mfDLL(procMFTEnumEx, uintptr(unsafe.Pointer(&mftCategoryVideoEncoder)),
		enumSync|enumAsync|enumHardware|enumSortFilt, 0, uintptr(unsafe.Pointer(&out)),
		uintptr(unsafe.Pointer(&arr)), uintptr(unsafe.Pointer(&n)))
	if mfFailed(r) || arr == nil || n == 0 {
		if arr != nil {
			mfDLL(procCoTaskMemFree, uintptr(arr))
		}
		return nil, errors.New("donanim H.264 kodlayici yok")
	}
	acts := unsafe.Slice((*unsafe.Pointer)(arr), int(n))
	var found *mfEncoder
	var lastErr error
	for _, a := range acts {
		act := mfObj{a}
		if found != nil {
			act.release()
			continue
		}
		var mft unsafe.Pointer
		r := act.call(vtActivateObject, uintptr(unsafe.Pointer(&iidIMFTransform)), uintptr(unsafe.Pointer(&mft)))
		if mfFailed(r) || mft == nil {
			lastErr = mfErr("ActivateObject", r)
			act.release()
			continue
		}
		e := &mfEncoder{name: "mf-hw", mft: mfObj{mft}, act: act}
		if err := e.setup(w, h, fps, bps); err != nil {
			lastErr = err
			e.Close()
			continue
		}
		found = e
	}
	mfDLL(procCoTaskMemFree, uintptr(arr))
	if found == nil {
		if lastErr == nil {
			lastErr = errors.New("donanim kodlayici baslatilamadi")
		}
		return nil, lastErr
	}
	return found, nil
}

func (e *mfEncoder) Name() string { return e.name }

func (e *mfEncoder) setCodec(g *syscall.GUID, v uint32) bool {
	if !e.codec.ok() {
		return false
	}
	vr := &mfVariant{VT: vtUI4, Val: uint64(v)}
	return !mfFailed(e.codec.call(vtCodecSetValue, uintptr(unsafe.Pointer(g)), uintptr(unsafe.Pointer(vr))))
}

func (e *mfEncoder) setup(w, h, fps, bps int) error {
	e.w, e.h = w, h
	e.frmDur = 10_000_000 / int64(max(fps, 1))

	// Asenkron MFT: kilidi ac.
	var attrs unsafe.Pointer
	if !mfFailed(e.mft.call(vtMftGetAttributes, uintptr(unsafe.Pointer(&attrs)))) && attrs != nil {
		a := mfObj{attrs}
		var v uint32
		if !mfFailed(a.call(vtAttrGetUINT32, uintptr(unsafe.Pointer(&mfTransformAsync)), uintptr(unsafe.Pointer(&v)))) && v == 1 {
			e.async = true
			a.call(vtAttrSetUINT32, uintptr(unsafe.Pointer(&mfTransformAsyncUnlock)), 1)
			if g, ok := e.mft.qi(&iidIMFMediaEventGenerator); ok {
				e.evGen = g
			} else {
				a.release()
				return errors.New("asenkron MFT olay uretecini vermedi")
			}
		}
		a.release()
	}
	if c, ok := e.mft.qi(&iidICodecAPI); ok {
		e.codec = c
	}
	// Tip pazarligindan ONCE kodlayici ayarlari (en iyi caba).
	e.setCodec(&capLowLatency, 1)
	e.setCodec(&capRateControl, 0) // eAVEncCommonRateControlMode_CBR
	e.setCodec(&capMeanBitRate, uint32(bps))
	e.setCodec(&capBPictureCnt, 0)
	e.setCodec(&capGOPSize, uint32(max(fps, 1)*2))

	// Cikis tipi (kodlayicilar once cikis tipini ister), constrained baseline; olmazsa main.
	var ok bool
	var lastR uintptr
	for _, prof := range []uint32{h264ProfileBase, h264ProfileMain} {
		t, err := mfNewType(w, h, fps, &mfFormatH264, bps, prof)
		if err != nil {
			return err
		}
		lastR = e.mft.call(vtMftSetOutputType, 0, uintptr(t.p), 0)
		t.release()
		if !mfFailed(lastR) {
			ok = true
			break
		}
	}
	if !ok {
		return mfErr("SetOutputType(H264)", lastR)
	}
	in, err := mfNewType(w, h, fps, &mfFormatNV12, 0, 0)
	if err != nil {
		return err
	}
	r := e.mft.call(vtMftSetInputType, 0, uintptr(in.p), 0)
	in.release()
	if mfFailed(r) {
		return mfErr("SetInputType(NV12)", r)
	}

	var info struct{ Flags, Size, Align uint32 }
	if r := e.mft.call(vtMftOutStreamInfo, 0, uintptr(unsafe.Pointer(&info))); mfFailed(r) {
		return mfErr("GetOutputStreamInfo", r)
	}
	e.outProvides = info.Flags&mftOutputProvidesSamples != 0
	e.outSize = int(max(info.Size, 1<<20))

	e.mft.call(vtMftProcessMsg, mftMsgBeginStreaming, 0)
	e.mft.call(vtMftProcessMsg, mftMsgStartOfStream, 0)
	return nil
}

func mfNewType(w, h, fps int, sub *syscall.GUID, bps int, profile uint32) (mfObj, error) {
	var p unsafe.Pointer
	if r := mfDLL(procMFCreateMediaType, uintptr(unsafe.Pointer(&p))); mfFailed(r) || p == nil {
		return mfObj{}, mfErr("MFCreateMediaType", r)
	}
	t := mfObj{p}
	setG := func(k, v *syscall.GUID) {
		t.call(vtAttrSetGUID, uintptr(unsafe.Pointer(k)), uintptr(unsafe.Pointer(v)))
	}
	set32 := func(k *syscall.GUID, v uint32) {
		t.call(vtAttrSetUINT32, uintptr(unsafe.Pointer(k)), uintptr(v))
	}
	set64 := func(k *syscall.GUID, hi, lo uint32) {
		t.call(vtAttrSetUINT64, uintptr(unsafe.Pointer(k)), uintptr(uint64(hi)<<32|uint64(lo)))
	}
	setG(&mfMTMajor, &mfMediaTypeVideo)
	setG(&mfMTSubtype, sub)
	if bps > 0 {
		set32(&mfMTAvgBitrate, uint32(bps))
	}
	set64(&mfMTFrameSize, uint32(w), uint32(h))
	set64(&mfMTFrameRate, uint32(max(fps, 1)), 1)
	set64(&mfMTPixelAspect, 1, 1)
	set32(&mfMTInterlace, 2) // MFVideoInterlace_Progressive
	if profile != 0 {
		set32(&mfMTMpeg2Prof, profile)
	}
	return t, nil
}

func (e *mfEncoder) SetBitrate(bps int) error {
	if !e.setCodec(&capMeanBitRate, uint32(bps)) {
		return errors.New("bit hizi ayarlanamadi")
	}
	return nil
}

func (e *mfEncoder) Close() {
	if e.mft.ok() {
		e.mft.call(vtMftProcessMsg, mftMsgEndOfStream, 0)
		e.mft.call(vtMftProcessMsg, mftMsgFlush, 0)
	}
	e.outBuf.release()
	e.outSample.release()
	e.evGen.release()
	e.codec.release()
	e.mft.release()
	if e.act.ok() {
		e.act.call(vtActivateShutdown)
		e.act.release()
	}
}

// inputSample, NV12 verisinden MF ornegi olusturur.
func (e *mfEncoder) inputSample(nv12 []byte, ts time.Duration) (mfObj, error) {
	var bp unsafe.Pointer
	if r := mfDLL(procMFCreateMemoryBuffer, uintptr(len(nv12)), uintptr(unsafe.Pointer(&bp))); mfFailed(r) || bp == nil {
		return mfObj{}, mfErr("MFCreateMemoryBuffer", r)
	}
	buf := mfObj{bp}
	defer buf.release()
	var data unsafe.Pointer
	var mx, cur uint32
	if r := buf.call(vtBufLock, uintptr(unsafe.Pointer(&data)), uintptr(unsafe.Pointer(&mx)), uintptr(unsafe.Pointer(&cur))); mfFailed(r) || data == nil {
		return mfObj{}, mfErr("buffer.Lock", r)
	}
	if int(mx) < len(nv12) {
		buf.call(vtBufUnlock)
		return mfObj{}, errors.New("MF tamponu kucuk")
	}
	copy(unsafe.Slice((*byte)(data), len(nv12)), nv12)
	buf.call(vtBufUnlock)
	buf.call(vtBufSetCurLen, uintptr(len(nv12)))
	var sp unsafe.Pointer
	if r := mfDLL(procMFCreateSample, uintptr(unsafe.Pointer(&sp))); mfFailed(r) || sp == nil {
		return mfObj{}, mfErr("MFCreateSample", r)
	}
	s := mfObj{sp}
	s.call(vtSampleAddBuffer, uintptr(buf.p))
	s.call(vtSampleSetTime, uintptr(uint64(ts.Nanoseconds()/100)))
	s.call(vtSampleSetDuration, uintptr(uint64(e.frmDur)))
	return s, nil
}

// readSample, ornegin icerigini Go dilimine kopyalar.
func readSample(s mfObj) ([]byte, error) {
	var bp unsafe.Pointer
	if r := s.call(vtSampleConvertContig, uintptr(unsafe.Pointer(&bp))); mfFailed(r) || bp == nil {
		return nil, mfErr("ConvertToContiguousBuffer", r)
	}
	buf := mfObj{bp}
	defer buf.release()
	var data unsafe.Pointer
	var mx, cur uint32
	if r := buf.call(vtBufLock, uintptr(unsafe.Pointer(&data)), uintptr(unsafe.Pointer(&mx)), uintptr(unsafe.Pointer(&cur))); mfFailed(r) || data == nil {
		return nil, mfErr("cikis Lock", r)
	}
	out := append([]byte(nil), unsafe.Slice((*byte)(data), int(cur))...)
	buf.call(vtBufUnlock)
	return out, nil
}

// pullOutput, ProcessOutput'u cagirir: (veri, hazir). hazir=false => daha
// fazla girdi gerekli.
func (e *mfEncoder) pullOutput() ([]byte, bool, error) {
	for tries := 0; tries < 3; tries++ {
		var odb mftOutputDataBuffer
		if !e.outProvides {
			if !e.outSample.ok() {
				var bp, sp unsafe.Pointer
				if r := mfDLL(procMFCreateMemoryBuffer, uintptr(e.outSize), uintptr(unsafe.Pointer(&bp))); mfFailed(r) {
					return nil, false, mfErr("cikis tamponu", r)
				}
				if r := mfDLL(procMFCreateSample, uintptr(unsafe.Pointer(&sp))); mfFailed(r) {
					tmp := mfObj{bp}
					tmp.release()
					return nil, false, mfErr("cikis ornegi", r)
				}
				e.outBuf, e.outSample = mfObj{bp}, mfObj{sp}
				e.outSample.call(vtSampleAddBuffer, uintptr(e.outBuf.p))
			}
			odb.Sample = e.outSample.p
		}
		var status uint32
		r := e.mft.call(vtMftProcessOutput, 0, 1, uintptr(unsafe.Pointer(&odb)), uintptr(unsafe.Pointer(&status)))
		if odb.Events != nil {
			ev := mfObj{odb.Events}
			ev.release()
		}
		switch uint32(r) {
		case hrMFNeedMoreInput:
			return nil, false, nil
		case hrMFStreamChange:
			var t unsafe.Pointer
			if rr := e.mft.call(vtMftOutAvailType, 0, 0, uintptr(unsafe.Pointer(&t))); mfFailed(rr) || t == nil {
				return nil, false, mfErr("GetOutputAvailableType", rr)
			}
			tt := mfObj{t}
			rr := e.mft.call(vtMftSetOutputType, 0, uintptr(tt.p), 0)
			tt.release()
			if mfFailed(rr) {
				return nil, false, mfErr("SetOutputType(yeniden)", rr)
			}
			continue
		}
		if mfFailed(r) {
			return nil, false, mfErr("ProcessOutput", r)
		}
		var s mfObj
		if e.outProvides {
			s = mfObj{odb.Sample}
		} else {
			s = e.outSample
		}
		if !s.ok() {
			return nil, false, errors.New("ProcessOutput ornek vermedi")
		}
		data, err := readSample(s)
		if e.outProvides {
			s.release()
		} else {
			e.outBuf.call(vtBufSetCurLen, 0)
		}
		if err != nil {
			return nil, false, err
		}
		return data, true, nil
	}
	return nil, false, errors.New("ProcessOutput akis degisimi donguye girdi")
}

func (e *mfEncoder) Encode(nv12 []byte, ts time.Duration, forceIDR bool) ([]byte, error) {
	if forceIDR {
		e.setCodec(&capForceKeyFrame, 1)
	}
	s, err := e.inputSample(nv12, ts)
	if err != nil {
		return nil, err
	}
	defer s.release()
	if e.async {
		return e.encodeAsync(s)
	}
	var out []byte
	for attempt := 0; attempt < 3; attempt++ {
		r := e.mft.call(vtMftProcessInput, 0, uintptr(s.p), 0)
		if uint32(r) == hrMFNotAccepting {
			d, _, err := e.pullOutput()
			if err != nil {
				return nil, err
			}
			out = append(out, d...)
			continue
		}
		if mfFailed(r) {
			return nil, mfErr("ProcessInput", r)
		}
		break
	}
	for {
		d, ok, err := e.pullOutput()
		if err != nil {
			return nil, err
		}
		if !ok {
			return out, nil
		}
		out = append(out, d...)
	}
}

// pollEvent, bir MFT olayi varsa tipini doner (yoksa 0).
func (e *mfEncoder) pollEvent() (uint32, error) {
	var ev unsafe.Pointer
	r := e.evGen.call(vtEvGenGetEvent, 1 /* MF_EVENT_FLAG_NO_WAIT */, uintptr(unsafe.Pointer(&ev)))
	if uint32(r) == hrMFNoEventsAvailable {
		return 0, nil
	}
	if mfFailed(r) || ev == nil {
		return 0, mfErr("GetEvent", r)
	}
	o := mfObj{ev}
	var typ uint32
	o.call(vtEventGetType, uintptr(unsafe.Pointer(&typ)))
	o.release()
	return typ, nil
}

// encodeAsync, asenkron (donanim) MFT akisi: NeedInput bekle, girdi ver,
// HaveOutput'u kisa sure bekle (dusuk gecikme kodlayicilar birkac ms'de verir).
func (e *mfEncoder) encodeAsync(s mfObj) ([]byte, error) {
	var out []byte
	deadline := time.Now().Add(2 * time.Second)
	for e.needInput == 0 {
		typ, err := e.pollEvent()
		if err != nil {
			return nil, err
		}
		switch typ {
		case meTransformNeedInput:
			e.needInput++
		case meTransformHaveOutput:
			d, _, err := e.pullOutput()
			if err != nil {
				return nil, err
			}
			out = append(out, d...)
		case 0:
			if time.Now().After(deadline) {
				return nil, errors.New("donanim kodlayici girdi istemedi (zaman asimi)")
			}
			time.Sleep(time.Millisecond)
		}
	}
	if r := e.mft.call(vtMftProcessInput, 0, uintptr(s.p), 0); mfFailed(r) {
		return nil, mfErr("ProcessInput(async)", r)
	}
	e.needInput--
	got := len(out) > 0
	end := time.Now().Add(40 * time.Millisecond)
	for time.Now().Before(end) {
		typ, err := e.pollEvent()
		if err != nil {
			return nil, err
		}
		switch typ {
		case meTransformNeedInput:
			e.needInput++
			if got {
				return out, nil
			}
		case meTransformHaveOutput:
			d, _, err := e.pullOutput()
			if err != nil {
				return nil, err
			}
			out = append(out, d...)
			got = true
		case 0:
			if got {
				return out, nil
			}
			time.Sleep(time.Millisecond)
		}
	}
	return out, nil
}
