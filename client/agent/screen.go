package agent

import (
	"bytes"
	"context"
	"image"
	"image/jpeg"
	"runtime"
	"sync"
	"time"

	"github.com/kbinani/screenshot"
	"github.com/tkodcumpeg4/zorven/shared/protocol"
	"golang.org/x/image/draw"
)

// screenManager, istemcideki uzak ekran oturumlarini yonetir.
//
// GUVENLIK: her oturum istemci makinesinin EKRANINI yayinlar ve (Windows'ta)
// fare/klavye girdisini uygular. Yetkilendirme sunucu tarafinda: yalnizca admin
// anahtarli dashboard tek kullanimlik biletle baglanabilir. Istemci sunucuya
// kendi token'iyla kimlik dogrulamis ve TLS uzerinden baglidir.
type screenManager struct {
	cs *clientSession

	mu        sync.Mutex
	sessions  map[string]*screenSession
	onChanged func() // oturum sayisi degistiginde cagrilir (nil olabilir)
}

type screenSession struct {
	id     string
	cancel context.CancelFunc
	once   sync.Once
}

func newScreenManager(cs *clientSession) *screenManager {
	return &screenManager{cs: cs, sessions: make(map[string]*screenSession)}
}

// count, aktif ekran oturumu sayisini doner.
func (sm *screenManager) count() int {
	sm.mu.Lock()
	defer sm.mu.Unlock()
	return len(sm.sessions)
}

// Varsayilanlar: dusuk FPS + orta kalite. Kareler paylasilan kontrol kanalindan
// base64 olarak aktigi icin bant genisligini bilincli sinirli tutuyoruz.
const (
	defaultFPS      = 5
	defaultQuality  = 55
	defaultMaxWidth = 1600
)

// open, ekran yakalamayi baslatir ve kareleri sunucuya akitir.
func (sm *screenManager) open(parent context.Context, msg protocol.ScreenOpen) {
	fps := msg.FPS
	if fps <= 0 || fps > 30 {
		fps = defaultFPS
	}
	quality := msg.Quality
	if quality <= 0 || quality > 100 {
		quality = defaultQuality
	}
	maxWidth := msg.MaxWidth
	if maxWidth <= 0 {
		maxWidth = defaultMaxWidth
	}

	// Hangi monitor: gecersizse birincile dus.
	display := msg.Display
	if display < 0 || display >= screenshot.NumActiveDisplays() {
		display = 0
	}

	ctx, cancel := context.WithCancel(parent)
	ss := &screenSession{id: msg.SessionID, cancel: cancel}

	sm.mu.Lock()
	sm.sessions[msg.SessionID] = ss
	cb := sm.onChanged
	sm.mu.Unlock()
	if cb != nil {
		cb()
	}

	// Codec secimi: mode "mjpeg" degilse ve donanim H.264 encoder'i varsa H.264
	// akisini dene (cok daha akici + dusuk bant genisligi). Basarisiz olursa
	// veya yoksa JPEG karelere dus — her zaman calisan taban budur.
	if msg.Mode != "mjpeg" {
		if enc := detectHWEncoder(); enc != "" {
			if sm.captureH264(ctx, ss, display, fps, maxWidth, enc) {
				return // H.264 yolu oturumu ustlendi
			}
			// captureH264 baslayamadiysa (ffmpeg hemen coktu) JPEG'e dus.
		}
	}

	go sm.capture(ctx, ss, display, fps, quality, maxWidth)
}

func (sm *screenManager) capture(ctx context.Context, ss *screenSession, display, fps, quality, maxWidth int) {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	attachToInputDesktop()

	defer sm.finish(ss)

	interval := time.Second / time.Duration(fps)
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	var seq uint64
	buf := new(bytes.Buffer)

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}

		bounds := screenshot.GetDisplayBounds(display)
		img, err := screenshot.CaptureRect(bounds)
		if err != nil {
			// Masaustu degismis veya kilitlenmis olabilir; tekrar baglanmayi dene
			attachToInputDesktop()
			img, err = screenshot.CaptureRect(bounds)
			if err != nil {
				sm.sendError(ctx, ss.id, "ekran yakalanamadi: "+err.Error())
				return
			}
		}

		out := scaleDown(img, maxWidth)

		buf.Reset()
		if err := jpeg.Encode(buf, out, &jpeg.Options{Quality: quality}); err != nil {
			sm.sendError(ctx, ss.id, "kare kodlanamadi: "+err.Error())
			return
		}
		frame := make([]byte, buf.Len())
		copy(frame, buf.Bytes())

		seq++
		if err := sm.cs.sendControl(ctx, protocol.ScreenFrame{
			Type:      protocol.TypeScreenFrame,
			SessionID: ss.id,
			Data:      frame,
			Width:     out.Bounds().Dx(),
			Height:    out.Bounds().Dy(),
			// Native ekran boyutu: dashboard cozunurluk secenekleri icin
			// olceklenmis kareye degil buna bakar (bkz. protocol.ScreenFrame).
			ScreenW: bounds.Dx(),
			ScreenH: bounds.Dy(),
			Seq:     seq,
			Codec:   "mjpeg",
		}, protocol.TypeScreenFrame); err != nil {
			return // baglanti koptu
		}
	}
}

// scaleDown, goruntuyu maxWidth'i asiyorsa oranini koruyarak kuculir.
// Buyuk ekranlar bant genisligini patlatmasin diye.
func scaleDown(src *image.RGBA, maxWidth int) image.Image {
	w := src.Bounds().Dx()
	h := src.Bounds().Dy()
	if w <= maxWidth {
		return src
	}
	nh := h * maxWidth / w
	dst := image.NewRGBA(image.Rect(0, 0, maxWidth, nh))
	// ApproxBiLinear: hiz/kalite dengesi iyi; her karede calisacak.
	draw.ApproxBiLinear.Scale(dst, dst.Bounds(), src, src.Bounds(), draw.Over, nil)
	return dst
}

// input, dashboard'dan gelen fare/klavye olayini uygular.
// Gercek enjeksiyon platforma ozeldir (bkz. screen_input_windows.go).
func (sm *screenManager) input(msg protocol.ScreenInput) {
	applyInput(msg)
}

func (sm *screenManager) closeSession(sessionID string) {
	sm.mu.Lock()
	ss, ok := sm.sessions[sessionID]
	delete(sm.sessions, sessionID)
	cb := sm.onChanged
	sm.mu.Unlock()
	if ok {
		ss.cancel()
		if cb != nil {
			cb()
		}
	}
}

func (sm *screenManager) finish(ss *screenSession) {
	sm.mu.Lock()
	delete(sm.sessions, ss.id)
	cb := sm.onChanged
	sm.mu.Unlock()
	ss.once.Do(func() { ss.cancel() })
	if cb != nil {
		cb()
	}
}

func (sm *screenManager) sendError(ctx context.Context, id, message string) {
	_ = sm.cs.sendControl(ctx, protocol.ScreenError{
		Type:      protocol.TypeScreenError,
		SessionID: id,
		Message:   message,
	}, protocol.TypeScreenError)
}

// closeAll, baglanti koptugunda tum ekran oturumlarini durdurur.
func (sm *screenManager) closeAll() {
	sm.mu.Lock()
	all := sm.sessions
	sm.sessions = make(map[string]*screenSession)
	cb := sm.onChanged
	sm.mu.Unlock()
	for _, ss := range all {
		ss.cancel()
	}
	if cb != nil {
		cb()
	}
}
