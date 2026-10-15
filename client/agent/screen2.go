package agent

import (
	"context"
	"errors"
	"fmt"
	"image"
	"runtime"
	"sync"
	"sync/atomic"
	"time"

	"github.com/tkodcumpeg4/zorven/shared/protocol"
)

// Capturer, ekran yakalama soyutlamasi. Go surumu kbinani/screenshot kullanir;
// ileride ayri bir Rust yardimci sureci ayni arayuzu uygulayabilir.
//
// Sozlesme: Capture'in dondurdugu goruntu cagiranin malidir (yakalayici
// sonraki cagrilarda ayni tamponu yeniden KULLANMAMALI), Pix doldurulmus ve
// Stride >= 4*genislik olmalidir.
type Capturer interface {
	// NumDisplays, kullanilabilir monitor sayisi (0 => yakalama desteklenmiyor).
	NumDisplays() int
	// Bounds, monitorun sanal masaustundeki dikdortgeni.
	Bounds(display int) image.Rectangle
	// Capture, monitorun tam goruntusunu doner.
	Capture(display int) (*image.RGBA, error)
}

// TileEncoder, bir dikdortgen bolgeyi kodlar. enc protocol.Screen2Enc* degeridir.
// Donen yuk cagirana aittir (tamponu yeniden kullanilmaz).
type TileEncoder interface {
	Encode(img *image.RGBA, r image.Rectangle, quality int) (enc uint8, payload []byte, err error)
}

// goCapturer, platNumDisplays/platBounds/platCapture uzerinde Capturer
// (macOS disinda kbinani/screenshot, macOS'ta capture_darwin.go).
type goCapturer struct{}

func (goCapturer) NumDisplays() int { return platNumDisplays() }
func (goCapturer) Bounds(d int) image.Rectangle {
	return platBounds(d)
}
func (goCapturer) Capture(d int) (*image.RGBA, error) {
	return platCapture(d)
}

// errScreen2Unsupported, yakalama yapilamayan platformlar icin acik hata.
var errScreen2Unsupported = errors.New("ekran yakalama bu platformda desteklenmiyor (macOS icin Ekran Kaydi izni ve macOS 14 veya oncesi gerekir)")

// screen2Params, oturumun degistirilebilir ayarlari.
type screen2Params struct {
	FPS, Quality, MaxWidth int
	Display                int // -1 => degistirme (yalniz guncellemede)
	// Layers dolu ise oturum katmanli calisir (screen2_layers pazarlandi).
	Layers []protocol.Screen2LayerSpec
}

// screen2Session, yakalama -> karo farki -> kodlama -> gonderme dongusu.
type screen2Session struct {
	id       string
	streamID uint64
	cap      Capturer
	enc      TileEncoder
	// send, tek ikili mesajı gonderir (kilit/zaman asimi cagirana aittir).
	send func(ctx context.Context, b []byte) error
	// onDisplay, izlenen monitor degisince (ve basta) girdi koordinatlari icin cagrilir.
	onDisplay func(display int, bounds image.Rectangle)
	// onAttach, yakalama oncesi (Windows girdi masaustune baglanma); nil olabilir.
	onAttach func()

	display  int
	maxWidth int
	ctl      *adaptCtl

	keyReq atomic.Bool

	// Katmanli mod (screen2_layers): bkz. screen2_layers.go.
	layered bool
	layerMu sync.Mutex
	layerIn layerUpdate // bekleyen guncelleme (en yenisi kazanir; anahtar istegi OR'lanir)
	// canvas, son kare tuvalinin boyutu (w<<32|h); imlec katmani okur.
	canvas atomic.Uint64
	setCh  chan screen2Params // kapasite 1; en son deger kazanir

	seq uint32
	// istatistik (test/log)
	statMu sync.Mutex
	stats  screen2Stats
}

type screen2Stats struct {
	Frames, Keyframes int
	Bytes             int64
}

func newScreen2Session(id string, streamID uint64, c Capturer, e TileEncoder,
	send func(context.Context, []byte) error, p screen2Params) *screen2Session {
	if p.FPS <= 0 || p.FPS > 30 {
		p.FPS = 15
	}
	if p.FPS > 20 {
		p.FPS = 20
	}
	if p.Quality <= 0 || p.Quality > 100 {
		p.Quality = 70
	}
	s := &screen2Session{
		id: id, streamID: streamID, cap: c, enc: e, send: send,
		display: p.Display, maxWidth: p.MaxWidth,
		ctl:   newAdaptCtl(p.Quality, p.FPS),
		setCh: make(chan screen2Params, 1),
	}
	if len(p.Layers) > 0 {
		// Kalite tavani katman bazinda (spec.Quality); denetleyici yalniz dusurur.
		s.layered = true
		s.ctl = newAdaptCtl(100, p.FPS)
		s.layerIn = layerUpdate{set: true, layers: append([]protocol.Screen2LayerSpec(nil), p.Layers...)}
	}
	return s
}

// canvasSize, son gonderilen karenin (tuval) boyutu; kare yoksa 0,0.
func (s *screen2Session) canvasSize() (w, h int) {
	v := s.canvas.Load()
	return int(v >> 32), int(v & 0xffffffff)
}

// Keyframe, bir sonraki turda tam kare gonderilmesini ister (goroutine-guvenli).
func (s *screen2Session) Keyframe() { s.keyReq.Store(true) }

// Update, ayarlari canli degistirir (goroutine-guvenli, bloklamaz). Sifir
// alanlar degistirilmez; Display < 0 degistirilmez.
func (s *screen2Session) Update(p screen2Params) {
	for {
		select {
		case s.setCh <- p:
			return
		default:
		}
		select {
		case <-s.setCh: // eski bekleyeni at, yenisini koy
		default:
		}
	}
}

func (s *screen2Session) applyParams(p screen2Params) (displayChanged bool) {
	mq, mf := s.ctl.maxQ, s.ctl.maxFPS
	if p.Quality > 0 && p.Quality <= 100 {
		mq = p.Quality
	}
	if p.FPS > 0 {
		mf = min(p.FPS, 20)
	}
	s.ctl.SetMax(mq, mf)
	if p.MaxWidth > 0 {
		s.maxWidth = p.MaxWidth
	}
	if p.Display >= 0 && p.Display != s.display && p.Display < s.cap.NumDisplays() {
		s.display = p.Display
		return true
	}
	return false
}

// run, ctx iptal olana veya kalici hata olusana kadar calisir. Capture hatasi
// bir kez yeniden denenir (onAttach ile).
func (s *screen2Session) run(ctx context.Context) error {
	if s.cap.NumDisplays() == 0 {
		return errScreen2Unsupported
	}
	if s.display < 0 || s.display >= s.cap.NumDisplays() {
		s.display = 0
	}
	s.notifyDisplay()
	if s.layered {
		return s.runLayered(ctx)
	}

	var prev *image.RGBA
	needKey := true
	next := time.Now()
	timer := time.NewTimer(0)
	defer timer.Stop()

	for {
		if d := time.Until(next); d > 0 {
			timer.Reset(d)
			select {
			case <-ctx.Done():
				return nil
			case <-timer.C:
			}
		} else if ctx.Err() != nil {
			return nil
		}
		start := time.Now()

		select {
		case p := <-s.setCh:
			if s.applyParams(p) {
				s.notifyDisplay()
				needKey = true
				prev = nil
			}
		default:
		}
		if s.keyReq.Swap(false) {
			needKey = true
		}

		img, err := s.cap.Capture(s.display)
		if err != nil {
			if s.onAttach != nil {
				s.onAttach()
			}
			if img, err = s.cap.Capture(s.display); err != nil {
				return fmt.Errorf("ekran yakalanamadi: %w", err)
			}
		}
		frame := scaleRGBA(img, s.maxWidth)
		if frame.Rect.Dx() > 65535 || frame.Rect.Dy() > 65535 {
			return errors.New("ekran cozunurlugu screen2 icin cok buyuk")
		}
		if prev != nil && (prev.Rect.Dx() != frame.Rect.Dx() || prev.Rect.Dy() != frame.Rect.Dy()) {
			needKey = true
		}

		var rects []image.Rectangle
		if needKey {
			rects = splitRect(frame.Rect, screen2MaxRectDim)
		} else {
			dirty, tx, ty := diffTiles(prev, frame)
			for _, r := range mergeDirty(dirty, tx, ty, frame.Rect.Dx(), frame.Rect.Dy()) {
				rects = append(rects, splitRect(r, screen2MaxRectDim)...)
			}
		}
		prev = frame
		s.canvas.Store(uint64(frame.Rect.Dx())<<32 | uint64(frame.Rect.Dy()))

		if len(rects) > 0 {
			wire, err := s.encodeRects(frame, rects)
			if err != nil {
				return fmt.Errorf("kare kodlanamadi: %w", err)
			}
			msgs, err := protocol.PackScreen2(s.streamID, s.seq, needKey,
				uint16(frame.Rect.Dx()), uint16(frame.Rect.Dy()), wire, 0)
			if err != nil {
				return fmt.Errorf("kare paketlenemedi: %w", err)
			}
			s.seq++
			wstart := time.Now()
			var n int64
			for _, m := range msgs {
				if err := s.send(ctx, m); err != nil {
					if ctx.Err() != nil {
						return nil
					}
					return err
				}
				n += int64(len(m))
			}
			s.ctl.Observe(time.Since(wstart))
			s.statMu.Lock()
			s.stats.Frames++
			if needKey {
				s.stats.Keyframes++
			}
			s.stats.Bytes += n
			s.statMu.Unlock()
			needKey = false
		}

		next = start.Add(s.ctl.Interval())
		if now := time.Now(); next.Before(now) {
			next = now // yetisemiyoruz: birikmis kareleri yakalamaya calisma
		}
	}
}

func (s *screen2Session) notifyDisplay() {
	if s.onDisplay != nil {
		s.onDisplay(s.display, s.cap.Bounds(s.display))
	}
}

// encodeRects, dikdortgenleri kodlar; yuk cok buyukse ikiye bolup yeniden kodlar.
func (s *screen2Session) encodeRects(img *image.RGBA, rects []image.Rectangle) ([]protocol.Screen2Rect, error) {
	out := make([]protocol.Screen2Rect, 0, len(rects))
	q := s.ctl.Quality()
	var enc func(r image.Rectangle) error
	enc = func(r image.Rectangle) error {
		e, payload, err := s.enc.Encode(img, r, q)
		if err != nil {
			return err
		}
		if len(payload) > screen2MaxPayload && (r.Dx() > tileSize || r.Dy() > tileSize) {
			a, b := r, r
			if r.Dx() >= r.Dy() {
				m := r.Min.X + r.Dx()/2
				a.Max.X, b.Min.X = m, m
			} else {
				m := r.Min.Y + r.Dy()/2
				a.Max.Y, b.Min.Y = m, m
			}
			if err := enc(a); err != nil {
				return err
			}
			return enc(b)
		}
		out = append(out, protocol.Screen2Rect{
			X: uint16(r.Min.X), Y: uint16(r.Min.Y), W: uint16(r.Dx()), H: uint16(r.Dy()),
			Enc: e, Payload: payload,
		})
		return nil
	}
	for _, r := range rects {
		if err := enc(r); err != nil {
			return nil, err
		}
	}
	return out, nil
}

// scaleRGBA, scaleDown'u *image.RGBA donusuyle sarar (scaleDown her zaman RGBA doner).
func scaleRGBA(src *image.RGBA, maxWidth int) *image.RGBA {
	if maxWidth <= 0 {
		maxWidth = defaultMaxWidth
	}
	return scaleDown(src, maxWidth).(*image.RGBA)
}

// --- screenManager baglantisi ----------------------------------------------

// runScreen2, screen2 oturumunu surdurur ve bitince oturumu kapatir.
func (sm *screenManager) runScreen2(ctx context.Context, ss *screenSession, p screen2Params) {
	defer sm.finish(ss)
	s := newScreen2Session(ss.id, ss.streamID, goCapturer{}, newGoTileEncoder(), sm.cs.sendBinary, p)
	s.onAttach = func() { attachToInputDesktop() }
	s.onDisplay = func(_ int, b image.Rectangle) {
		ss.setBounds(displayRect{x: b.Min.X, y: b.Min.Y, w: b.Dx(), h: b.Dy()})
	}
	ss.boundsMu.Lock()
	ss.s2 = s
	ss.boundsMu.Unlock()
	sm.startAux(ctx, ss, s)
	// Yakalama is parcacigina bagli olmali (Windows masaustu baglama).
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	attachToInputDesktop()
	if err := s.run(ctx); err != nil {
		sm.sendError(ctx, ss.id, err.Error())
	}
}

// screen2Keyframe, panelin anahtar kare istegini oturuma iletir.
func (sm *screenManager) screen2Keyframe(id string) {
	sm.mu.Lock()
	ss := sm.sessions[id]
	sm.mu.Unlock()
	if ss != nil {
		if s := ss.getS2(); s != nil {
			s.Keyframe()
		}
	}
}

// screen2Layers, sunucunun etkin katman kumesi guncellemesini oturuma iletir.
func (sm *screenManager) screen2Layers(m protocol.Screen2Layers) {
	sm.mu.Lock()
	ss := sm.sessions[m.SessionID]
	sm.mu.Unlock()
	if ss != nil {
		if s := ss.getS2(); s != nil {
			s.SetLayers(m.Layers, m.Key)
		}
	}
}

// screen2Settings, canli ayar guncellemesini oturuma iletir.
func (sm *screenManager) screen2Settings(m protocol.Screen2Settings) {
	sm.mu.Lock()
	ss := sm.sessions[m.SessionID]
	sm.mu.Unlock()
	if ss != nil {
		if s := ss.getS2(); s != nil {
			s.Update(screen2Params{FPS: m.FPS, Quality: m.Quality, MaxWidth: m.MaxWidth, Display: m.Display})
		}
	}
}
