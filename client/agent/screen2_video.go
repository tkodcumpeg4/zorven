package agent

import (
	"context"
	"encoding/json"
	"image"
	"os"
	"runtime"
	"sync"
	"sync/atomic"
	"time"

	"github.com/tkodcumpeg4/zorven/shared/protocol"
)

// H.264 video modu (FeatureScreen2Video).
//
// Karo boru hattina MINIMUM kanca ile eklenir (bkz. screen2_pipe.go):
//   - captureLoop her turda degisim oranini verir (frame/idle); videoCtl
//     karo<->video kararini (videoSwitch) verir. Video modunda ayni olceklenmis
//     kare (katman 0) kodlayiciya gider, karo isi uretilmez.
//   - Kodlama ayri goroutine'de (encodeLoop): RGBA -> NV12 -> kodlayici ->
//     Annex-B -> AVCC; her IDR'den once config (avcC) gonderilir.
//   - Karoya donuste "stop" gonderilir ve tum katmanlar tam kare ister.
//   - Kodlayici hata verirse (donanim -> yazilim, sonra) video bu oturumda
//     kapatilir ve karo moduna dusulur.

// videoEncoder, tek bir akis icin H.264 kodlayici. Tek goroutine kullanir.
type videoEncoder interface {
	// Name, kodlayici adi (istatistik): "mf-hw" | "mf-sw".
	Name() string
	// Encode, bir NV12 karesini (kurulumdaki w x h) kodlar. Cikti Annex-B'dir;
	// kodlayici henuz cikti vermediyse bos doner. forceIDR: bu kare IDR olsun.
	Encode(nv12 []byte, ts time.Duration, forceIDR bool) ([]byte, error)
	// SetBitrate, hedef bit hizini (bit/sn) canli degistirir.
	SetBitrate(bps int) error
	Close()
}

// videoEncoderFactory, kodlayici uretir (testler degistirebilir).
type videoEncoderFactory func(w, h, fps, bps int, preferSW bool) (videoEncoder, error)

// newVideoEncoderFn, platform kodlayici fabrikasi (video_mf_windows.go /
// video_mf_other.go).
var newVideoEncoderFn videoEncoderFactory = platNewVideoEncoder

var (
	videoProbeOnce sync.Once
	videoProbeOK   bool
)

// videoEncoderAvailable, bu makinede H.264 kodlayici calisiyor mu (bir kez
// denenir). ZORVEN_NO_VIDEO=1 ile kapatilir. Hello'da screen2_video yalniz
// true ise ilan edilir.
func videoEncoderAvailable() bool {
	videoProbeOnce.Do(func() {
		if os.Getenv("ZORVEN_NO_VIDEO") == "1" {
			return
		}
		runtime.LockOSThread()
		defer runtime.UnlockOSThread()
		platVideoThreadInit()
		enc, err := newVideoEncoderFn(640, 360, 30, 1_000_000, false)
		if err != nil {
			return
		}
		enc.Close()
		videoProbeOK = true
	})
	return videoProbeOK
}

// vidAction, captureLoop'a donen karar.
type vidAction int

const (
	vidNone  vidAction = iota // karo isi normal uretilir
	vidSkip                   // video isliyor: karo isi uretme
	vidLeave                  // videodan karoya donuldu: tum katmanlar tam kare ister
)

const (
	videoMinDim = 64
	videoGOP    = 2 * time.Second
)

type vidJob struct {
	frame *image.RGBA
	w, h  int
	fps   int
	ts    time.Duration
}

// videoCtl, bir screen2 oturumunun video durumu.
type videoCtl struct {
	s *screen2Session

	// capture goroutine'ine ait
	sw     videoSwitch
	active bool

	pref   atomic.Pointer[string]
	activA atomic.Bool // active'in diger goroutine'ler icin aynasi
	broken atomic.Bool // kodlayici calismiyor: karo modunda kal
	idr    atomic.Bool

	// kodlama kuyrugu (derinlik 1, en yeni kazanir)
	qmu      sync.Mutex
	pending  *vidJob
	wantStop bool
	wake     chan struct{}

	// bit hizi (geri bildirim goroutine'i yazar, kodlayici okur)
	rmu      sync.Mutex
	rate     *videoRate
	rateW    int
	rateH    int
	bpsDirty bool

	// istatistik
	bytesTotal atomic.Int64
	backend    atomic.Pointer[string]
	curBps     atomic.Int64
	lastBytes  int64
	lastStatAt time.Time

	ctx     context.Context
	startAt time.Time
	seq     uint32
}

func newVideoCtl(s *screen2Session) *videoCtl {
	v := &videoCtl{s: s, wake: make(chan struct{}, 1), startAt: time.Now()}
	auto := protocol.Screen2VideoAuto
	v.pref.Store(&auto)
	return v
}

func (v *videoCtl) getPref() string {
	if p := v.pref.Load(); p != nil {
		return *p
	}
	return protocol.Screen2VideoAuto
}

// SetPref, sunucunun etkin modunu uygular (goroutine-guvenli). "video" ya da
// "auto"ya gecis icin bir tam kare istenir (durgun ekranda da gecis olsun).
func (v *videoCtl) SetPref(mode string) {
	if !protocol.ValidVideoMode(mode) {
		mode = protocol.Screen2VideoTiles
	}
	old := v.getPref()
	v.pref.Store(&mode)
	if old != mode {
		v.s.Keyframe()
	}
}

// onKeyReq, panelin/sunucunun anahtar kare istegi: video akiyorsa IDR da ister.
func (v *videoCtl) onKeyReq() {
	if v.activA.Load() {
		v.idr.Store(true)
	}
}

func (v *videoCtl) setActive(a bool) {
	v.active = a
	v.activA.Store(a)
}

// frame, bir yakalama turunun (katman 0) olceklenmis karesini ve degisim
// oranini isler. Yalniz yakalama goroutine'inden cagrilir.
func (v *videoCtl) frame(fr *image.RGBA, ratio float64, now time.Time) vidAction {
	if v.broken.Load() {
		if v.active {
			v.leave()
			return vidLeave
		}
		return vidNone
	}
	w, h := fr.Rect.Dx()&^1, fr.Rect.Dy()&^1
	if w < videoMinDim || h < videoMinDim || w > protocol.VideoMaxDim || h > protocol.VideoMaxDim {
		if v.active {
			v.leave()
			return vidLeave
		}
		return vidNone
	}
	video, changed := v.sw.step(now, ratio, v.getPref())
	if changed {
		if video {
			v.setActive(true)
			v.idr.Store(true)
			v.rmu.Lock()
			if v.rate == nil {
				v.rate = newVideoRate(w, h, v.s.ctl.FPS())
				v.rateW, v.rateH = w, h
			}
			v.rmu.Unlock()
		} else {
			v.leave()
			return vidLeave
		}
	}
	if !v.active {
		return vidNone
	}
	v.put(&vidJob{frame: fr, w: w, h: h, fps: max(v.s.ctl.FPS(), 5), ts: now.Sub(v.startAt)})
	return vidSkip
}

// idle, degisim olmayan (atlanan) bir turu isler: durgun ekranda videodan
// karoya donusu mumkun kilar. Video'ya GIRIS yalniz frame()'de olur.
func (v *videoCtl) idle(now time.Time) vidAction {
	video, changed := v.sw.step(now, 0, v.getPref())
	if changed && video {
		v.sw.video = false // kare olmadan girilmez
		return vidNone
	}
	if changed && !video {
		v.leave()
		return vidLeave
	}
	if v.active {
		return vidSkip
	}
	return vidNone
}

// leave, videodan cikar: kodlayiciyi kapatip stop gonderilmesini ister.
func (v *videoCtl) leave() {
	v.setActive(false)
	v.sw.video = false
	v.qmu.Lock()
	v.pending = nil
	v.wantStop = true
	v.qmu.Unlock()
	select {
	case v.wake <- struct{}{}:
	default:
	}
}

func (v *videoCtl) put(j *vidJob) {
	v.qmu.Lock()
	v.pending = j
	v.wantStop = false
	v.qmu.Unlock()
	select {
	case v.wake <- struct{}{}:
	default:
	}
}

func (v *videoCtl) take(ctx context.Context) (j *vidJob, stop bool, ok bool) {
	for {
		v.qmu.Lock()
		j, stop = v.pending, v.wantStop
		v.pending, v.wantStop = nil, false
		v.qmu.Unlock()
		if j != nil || stop {
			return j, stop, true
		}
		select {
		case <-ctx.Done():
			return nil, false, false
		case <-v.wake:
		}
	}
}

// feedback, sunucu teslim ozetinden bit hizini ayarlar.
func (v *videoCtl) feedback(m protocol.Screen2Feedback, now time.Time) {
	if !v.activA.Load() {
		return
	}
	cong := feedbackCongested(m)
	v.rmu.Lock()
	if v.rate != nil && v.rate.observe(now, cong) {
		v.bpsDirty = true
	}
	v.rmu.Unlock()
}

// fillStats, istatistik ozetine video alanlarini yazar.
func (v *videoCtl) fillStats(st *protocol.Screen2Stats, now time.Time) {
	st.Mode = protocol.Screen2VideoTiles
	if v.activA.Load() {
		st.Mode = "video"
	}
	if b := v.backend.Load(); b != nil {
		st.Backend = *b
	}
	st.VideoBitrate = int(v.curBps.Load())
	total := v.bytesTotal.Load()
	if !v.lastStatAt.IsZero() && st.Mode == "video" {
		if dt := now.Sub(v.lastStatAt).Seconds(); dt > 0 {
			st.VideoKbps = int(float64(total-v.lastBytes) * 8 / 1000 / dt)
		}
	}
	v.lastBytes, v.lastStatAt = total, now
}

// encodeLoop, kodlama goroutine'i (captureLoop baslarken baslatilir).
func (v *videoCtl) encodeLoop(ctx context.Context) {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	platVideoThreadInit()
	e := &videoEncodeState{v: v}
	defer e.close()
	for {
		j, stop, ok := v.take(ctx)
		if !ok {
			return
		}
		if stop {
			if e.enc != nil {
				e.close()
				e.sendStop(ctx)
			}
			continue
		}
		if err := e.encode(ctx, j); err != nil {
			if ctx.Err() != nil {
				return
			}
			v.broken.Store(true)
			e.close()
			e.sendStop(ctx)
		}
	}
}

// videoEncodeState, kodlama goroutine'inin durumu.
type videoEncodeState struct {
	v        *videoCtl
	enc      videoEncoder
	w, h     int
	fps      int
	nv12     []byte
	sps, pps []byte
	cfg      []byte // son config'in avcC'si
	codec    string
	preferSW bool
	lastIDR  time.Time
	sentCfg  bool
}

func (e *videoEncodeState) close() {
	if e.enc != nil {
		e.enc.Close()
		e.enc = nil
	}
	e.sentCfg = false
}

func (e *videoEncodeState) open(w, h, fps int) error {
	v := e.v
	e.close()
	v.rmu.Lock()
	if v.rate == nil || v.rateW != w || v.rateH != h {
		v.rate = newVideoRate(w, h, fps)
		v.rateW, v.rateH = w, h
	}
	bps := v.rate.bps
	v.bpsDirty = false
	v.rmu.Unlock()
	enc, err := newVideoEncoderFn(w, h, fps, bps, e.preferSW)
	if err != nil && !e.preferSW {
		e.preferSW = true
		enc, err = newVideoEncoderFn(w, h, fps, bps, true)
	}
	if err != nil {
		return err
	}
	e.enc, e.w, e.h, e.fps = enc, w, h, fps
	n := enc.Name()
	v.backend.Store(&n)
	v.curBps.Store(int64(bps))
	v.idr.Store(true)
	return nil
}

func (e *videoEncodeState) encode(ctx context.Context, j *vidJob) error {
	v := e.v
	if e.enc == nil || e.w != j.w || e.h != j.h {
		if err := e.open(j.w, j.h, j.fps); err != nil {
			return err
		}
	}
	// Bit hizi guncellemesi.
	v.rmu.Lock()
	var newBps int
	if v.bpsDirty && v.rate != nil {
		newBps, v.bpsDirty = v.rate.bps, false
	}
	v.rmu.Unlock()
	if newBps > 0 {
		if err := e.enc.SetBitrate(newBps); err == nil {
			v.curBps.Store(int64(newBps))
		}
	}
	idr := v.idr.Swap(false)
	if !idr && !e.lastIDR.IsZero() && time.Since(e.lastIDR) > videoGOP {
		idr = true // yedek: kodlayici GOP'una guvenme (kayip/gec katilan icin)
	}
	need := nv12Size(e.w, e.h)
	if len(e.nv12) != need {
		e.nv12 = make([]byte, need)
	}
	t0 := time.Now()
	rgbaToNV12(e.nv12, j.frame, e.w, e.h)
	out, err := e.enc.Encode(e.nv12, j.ts, idr)
	v.s.met.observe(stEncode, time.Since(t0))
	if err != nil {
		return err
	}
	if len(out) == 0 {
		if idr {
			v.idr.Store(true)
		}
		return nil
	}
	au := parseAnnexBAU(out)
	if au.SPS != nil && au.PPS != nil {
		if cfg, err := avcDecoderConfig(au.SPS, au.PPS); err == nil {
			if string(cfg) != string(e.cfg) {
				e.cfg, e.codec = cfg, avcCodecString(au.SPS)
				e.sentCfg = false
			}
		}
	}
	if len(au.AVCC) == 0 {
		return nil
	}
	if au.IDR {
		e.lastIDR = time.Now()
	}
	if len(au.AVCC) > protocol.VideoMaxChunk {
		// Cok buyuk kare: bit hizini yariya indir, yeni IDR iste.
		v.rmu.Lock()
		if v.rate != nil {
			v.rate.bps = clampInt(v.rate.bps/2, videoMinBitrate, videoMaxBitrate)
			v.bpsDirty = true
		}
		v.rmu.Unlock()
		v.idr.Store(true)
		return nil
	}
	if au.IDR || !e.sentCfg {
		if e.cfg == nil {
			if au.IDR {
				return errNoSPS
			}
			return nil // SPS/PPS henuz yok: IDR bekle
		}
		if !au.IDR {
			v.idr.Store(true)
			return nil
		}
		if err := e.sendConfig(ctx); err != nil {
			return err
		}
		e.sentCfg = true
	}
	return e.sendChunk(ctx, au, j.ts)
}

type videoError string

func (e videoError) Error() string { return string(e) }

const errNoSPS = videoError("kodlayici SPS/PPS uretmedi")

func (e *videoEncodeState) sendConfig(ctx context.Context) error {
	v := e.v
	b, err := protocol.VideoMessage{
		StreamID: v.s.streamID, Seq: v.seq, Keyframe: true, Kind: protocol.VideoKindConfig,
		W: uint16(e.w), H: uint16(e.h), Codec: e.codec, Desc: e.cfg,
	}.Encode()
	if err != nil {
		return err
	}
	return v.write(ctx, b, false)
}

func (e *videoEncodeState) sendChunk(ctx context.Context, au h264AU, ts time.Duration) error {
	v := e.v
	v.seq++
	b, err := protocol.VideoMessage{
		StreamID: v.s.streamID, Seq: v.seq, Keyframe: au.IDR, Kind: protocol.VideoKindChunk,
		TimestampUs: uint64(ts.Microseconds()), Data: au.AVCC,
	}.Encode()
	if err != nil {
		return err
	}
	return v.write(ctx, b, true)
}

func (e *videoEncodeState) sendStop(ctx context.Context) {
	v := e.v
	v.seq++
	b, err := protocol.VideoMessage{StreamID: v.s.streamID, Seq: v.seq, Kind: protocol.VideoKindStop}.Encode()
	if err == nil {
		_ = v.write(ctx, b, false)
	}
}

func (v *videoCtl) write(ctx context.Context, b []byte, frame bool) error {
	t0 := time.Now()
	err := v.s.send(ctx, b)
	v.s.met.observe(stWrite, time.Since(t0))
	if err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return err
	}
	if frame {
		v.s.met.addSent(int64(len(b)))
		v.bytesTotal.Add(int64(len(b)))
		v.s.ctl.Observe(time.Since(t0))
	}
	return nil
}

// --- screenManager baglantisi --------------------------------------------------

// screen2Video, sunucunun video modu bildirimini oturuma iletir.
func (sm *screenManager) screen2Video(m protocol.Screen2Video) {
	sm.mu.Lock()
	ss := sm.sessions[m.SessionID]
	sm.mu.Unlock()
	if ss == nil {
		return
	}
	if s := ss.getS2(); s != nil && s.vid != nil {
		s.vid.SetPref(m.Mode)
	}
}

// parseScreen2Video, screen2_video JSON'unu ayristirir.
func parseScreen2Video(data []byte) (protocol.Screen2Video, bool) {
	var m protocol.Screen2Video
	if json.Unmarshal(data, &m) != nil {
		return m, false
	}
	return m, true
}
