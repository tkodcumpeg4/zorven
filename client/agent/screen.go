package agent

import (
	"bytes"
	"context"
	"image"
	"image/jpeg"
	"runtime"
	"sync"
	"time"

	"github.com/tkodcumpeg4/zorven/client/i18n"
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

	// bounds, YAKALANAN monitorun sanal masaustundeki dikdortgeni.
	// Girdi koordinatlari bu dikdortgen icinde yorumlanir; aksi halde ikinci
	// monitoru izlerken tiklamalar birinciye giderdi.
	bounds   displayRect
	boundsMu sync.RWMutex

	// streamID/s2: yalniz screen2 modunda (bkz. screen2.go).
	streamID uint64
	s2       *screen2Session
	// clip: pano izleyici (yalniz screen2 + FeatureScreenClipboard; boundsMu altinda).
	clip *clipboardWatcher

	// input, girdi olaylarinin kuyrugu.
	//
	// NEDEN KUYRUK: onceden applyInput WebSocket OKUMA DONGUSUNDE senkron
	// calisiyordu. Tarayici saniyede 60-125 mousemove uretir ve her biri
	// SendInput syscall'i demektir; bu sure boyunca ayni istemcinin TUM tunel
	// trafigi (HTTP govde kareleri, terminal) bekliyordu. Artik olaylar
	// kuyruga birakilir ve ayri bir goroutine uygular.
	input *inputQueue

	// tracker, basili tus/dugmeleri izler (yalniz applyLoop); oturum bitince birakilir.
	tracker inputTracker

	// inputLog, oturum basina yalnizca ILK girdi olayinda log atmak icin.
	// FAZ 7 tanilama: "girdi alinmiyor" raporunda, girdinin sunucudan agent'a
	// ULASIP ulasmadigini loglardan kesin ayirt edebilmek icin.
	inputLog sync.Once

	// statusGate, girdi durumu bildirimini kisar (bkz. input_status.go).
	// Yalniz applyLoop goroutine'i kullanir.
	statusGate inputStatusGate
}

func (ss *screenSession) getBounds() displayRect {
	ss.boundsMu.RLock()
	defer ss.boundsMu.RUnlock()
	return ss.bounds
}

// setBounds, izlenen monitor degisince (screen2) girdi hedefini gunceller.
func (ss *screenSession) setBounds(b displayRect) {
	ss.boundsMu.Lock()
	ss.bounds = b
	ss.boundsMu.Unlock()
}

func (ss *screenSession) getS2() *screen2Session {
	ss.boundsMu.RLock()
	defer ss.boundsMu.RUnlock()
	return ss.s2
}

func newScreenManager(cs *clientSession) *screenManager {
	ensureDPIAware()
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
	// defaultFPS, dashboard bir hedef FPS bildirmezse kullanilir. Eskiden 5'ti
	// ve akis "cok yavas" hissettiriyordu (FAZ 7 kullanici raporu). Dashboard
	// artik acikca FPS gonderiyor; bu yalnizca guvenlik tabani.
	defaultFPS      = 12
	defaultQuality  = 55
	defaultMaxWidth = 1600
)

// legacyFPS, eski (mjpeg/ffmpeg H.264) yolda istenen fps'i cozer. 30 ustu fps
// yalniz sunucu screen2_fps60'i onayladiysa (high) kabul edilir ve yalniz donanim
// H.264'te (ffmpeg, 60'a kadar) kullanilir; JPEG kare dongusu (capture) 30'da
// kalir. Gecersiz/asiri deger varsayilana (defaultFPS) doner.
func legacyFPS(req int, high bool) int {
	limit := screen2MaxFPS
	if high {
		limit = screen2HighFPS
	}
	if req <= 0 || req > limit {
		return defaultFPS
	}
	return req
}

// screen2ParamsFor, screen_open mesajindan screen2 oturum parametrelerini kurar:
// katmanlar yalniz screen2_layers pazarlandiysa, 30 ustu fps tavani yalniz
// screen2_fps60 pazarlandiysa gecerlidir (pazarlanmayan yetenek oturuma sizmaz).
func (sm *screenManager) screen2ParamsFor(msg protocol.ScreenOpen, display int) screen2Params {
	p := screen2Params{FPS: msg.FPS, Quality: msg.Quality, MaxWidth: msg.MaxWidth, Display: display,
		HighFPS: sm.cs.screen2FPS60}
	if sm.cs.screen2Layers {
		p.Layers = msg.Layers // bos => eski (katmansiz) davranis
	}
	return p
}

// open, ekran yakalamayi baslatir ve kareleri sunucuya akitir.
func (sm *screenManager) open(parent context.Context, msg protocol.ScreenOpen) {
	if err := platSessionBlocker(); err != nil {
		// Ornegin saf Wayland: siyah kare yerine acik, yerellestirilmis hata.
		sm.sendError(parent, msg.SessionID, err.Error())
		return
	}
	useScreen2 := msg.Mode == protocol.ScreenModeScreen2 && sm.cs.screen2
	if useScreen2 && platNumDisplays() == 0 {
		sm.sendError(parent, msg.SessionID, screenUnsupportedErr().Error())
		return
	}
	fps := legacyFPS(msg.FPS, sm.cs.screen2FPS60)
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
	if display < 0 || display >= platNumDisplays() {
		display = 0
	}

	ctx, cancel := context.WithCancel(parent)

	// Yakalanan monitorun sanal masaustundeki yeri: girdi koordinatlari bunun
	// icinde yorumlanacak.
	b := platBounds(display)
	ss := &screenSession{
		id:       msg.SessionID,
		cancel:   cancel,
		bounds:   displayRect{x: b.Min.X, y: b.Min.Y, w: b.Dx(), h: b.Dy()},
		input:    newInputQueue(screenInputQueue, screenInputHardCap),
		streamID: msg.StreamID,
	}

	// Ayni SessionID yeniden acildiysa ESKI oturumu once iptal et: aksi halde
	// iki yakalama dongusu ayni kimlikle calisir ve eskisinin finish()'i yeni
	// oturumun kaydini silerdi.
	sm.mu.Lock()
	old := sm.sessions[msg.SessionID]
	sm.sessions[msg.SessionID] = ss
	cb := sm.onChanged
	sm.mu.Unlock()
	if old != nil {
		old.cancel()
	}
	if cb != nil {
		cb()
	}

	// Girdi uygulayici: okuma dongusunden AYRI goroutine.
	go sm.applyLoop(ctx, ss)

	// screen2: karo tabanli ikili mod (yalniz FeatureScreen2 pazarlandiysa).
	if useScreen2 {
		go sm.runScreen2(ctx, ss, sm.screen2ParamsFor(msg, display))
		return
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

	go sm.capture(ctx, ss, display, min(fps, screen2MaxFPS), quality, maxWidth)
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
	var retry captureRetry
	degVis := false

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}

		capStart := time.Now()
		if n := platNumDisplays(); n > 0 && (display < 0 || display >= n) {
			display = 0 // monitor kayboldu: birinciye dus
		}
		bounds := platBounds(display)
		img, err := platCapture(display)
		if err != nil {
			// Masaustu degismis veya kilitlenmis olabilir; tekrar baglanmayi dene
			attachToInputDesktop()
			img, err = platCapture(display)
		}
		if err != nil {
			// Gecici hata: oturumu sonlandirmak yerine ussel geri cekilmeyle
			// yeniden dene (son kare izleyicide kalir).
			wait, giveUp := retry.Fail(time.Now())
			sm.cs.log.Warn("ekran yakalanamadi, yeniden denenecek", "session", ss.id, "hata", err, "bekleme", wait)
			if giveUp {
				sm.sendError(ctx, ss.id, i18n.T("scr.err.capture_gave_up", err.Error()))
				return
			}
			if !degVis {
				degVis = true
				if sm.cs.screenInputStatus {
					sm.sendInputStatus(ctx, ss.id, inputResult{State: protocol.InputStateDegraded, Detail: protocol.DegradedCaptureRetry})
				}
			}
			select {
			case <-ctx.Done():
				return
			case <-time.After(wait):
			}
			continue
		}
		if retry.Succeed() && degVis {
			degVis = false
			if sm.cs.screenInputStatus {
				sm.sendInputStatus(ctx, ss.id, inputResult{State: protocol.InputStateDegraded})
			}
		}
		captureMS := int(time.Since(capStart).Milliseconds())

		encStart := time.Now()
		out := scaleDown(img, maxWidth)

		buf.Reset()
		if err := jpeg.Encode(buf, out, &jpeg.Options{Quality: quality}); err != nil {
			sm.sendError(ctx, ss.id, i18n.T("scr.err.encode", err.Error()))
			return
		}
		frame := make([]byte, buf.Len())
		copy(frame, buf.Bytes())
		encodeMS := int(time.Since(encStart).Milliseconds())

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
			// Metrik paneli icin: darbogaz yakalama mi, kodlama mi, ag mi?
			CaptureMS: captureMS,
			EncodeMS:  encodeMS,
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

// input, dashboard'dan gelen fare/klavye olayini KUYRUGA birakir.
//
// ASLA BLOKLAMAZ: cagiran WebSocket okuma dongusudur ve orada beklemek
// istemcinin tum tunel trafigini durdurur.
func (sm *screenManager) input(msg protocol.ScreenInput) {
	sm.mu.Lock()
	ss := sm.sessions[msg.SessionID]
	sm.mu.Unlock()
	if ss == nil || ss.input == nil {
		return
	}

	// FAZ 7 tanilama: ilk girdide bir kez logla — girdinin agent'a ULASTIGINI
	// (panel->sunucu->agent yolunun saglam oldugunu) kesin gosterir. Uygulama
	// (SendInput) ayri; boylece "girdi alinmiyor" sorunu yol mu yoksa uygulama
	// mi diye ayrilabilir.
	ss.inputLog.Do(func() {
		sm.cs.log.Info("ekran girdisi alindi (ilk olay)",
			"session", msg.SessionID, "tur", msg.Kind, "os", runtime.GOOS)
	})

	// Politika inputQueue'da: yalniz mousemove dusurulur/birlestirilir,
	// tus/dugme/tekerlek/metin olaylari ASLA (bir keyup/mouseup'in kaybi
	// uzak makinede takili tus/dugme demektir).
	ss.input.Push(msg)
	if s2 := ss.getS2(); s2 != nil {
		s2.Poke() // kullanici etkilesiyor: bosta yavaslamayi hemen bitir
	}
}

// applyLoop, kuyruktaki girdi olaylarini uygular.
//
// Ardisik mousemove'lari BIRLESTIRIR: kuyrukta bekleyen daha yeni bir konum
// varsa eskisini uygulamak anlamsizdir — imlec zaten oraya gitmeyecek, sadece
// syscall harcanir ve gecikme buyur.
func (sm *screenManager) applyLoop(ctx context.Context, ss *screenSession) {
	// SendInput'un girdi masaustune erisebilmesi icin ayni is parcacigi.
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	attachToInputDesktop()
	var guard desktopGuard
	guard.Due(time.Now())

	// Oturum bitince (iptal, baglanti kopmasi, hata) basili kalan her tusu ve
	// fare dugmesini birak: takili tus/dugme kullanicinin makinesini kilitler.
	defer sm.releaseHeld(ss)

	for {
		select {
		case <-ctx.Done():
			return
		case <-ss.input.sig:
		}
		for {
			msg, ok := ss.input.Pop()
			if !ok {
				break
			}
			for _, ev := range coalesceQ(ss.input, msg) {
				now := time.Now()
				if guard.Due(now) {
					// Girdi masaustu degismis olabilir (UAC/kilit/oturum degisimi).
					attachToInputDesktop()
				}
				res := applyInput(ev, ss.getBounds())
				ss.tracker.Observe(ev)
				if sm.cs.screenInputStatus && ss.statusGate.Should(res, now) {
					sm.sendInputStatus(ctx, ss.id, res)
				}
			}
			if ctx.Err() != nil {
				return
			}
		}
	}
}

// releaseHeld, izlenen basili tus/dugmeleri birakir.
func (sm *screenManager) releaseHeld(ss *screenSession) {
	evs := ss.tracker.ReleaseAll()
	if len(evs) == 0 {
		return
	}
	rect := ss.getBounds()
	for _, ev := range evs {
		applyInput(ev, rect)
	}
	sm.cs.log.Info("ekran oturumu bitti: basili kalan girdi birakildi", "session", ss.id, "adet", len(evs))
}

// coalesceQ, siradaki ardisik mousemove'lari tek bir olaya indirger ve
// uygulanacak olaylari SIRAYLA doner (en fazla iki olay: birlestirilmis son
// mousemove + onu takip eden mousemove olmayan olay).
//
// Ara konumlari uygulamak anlamsizdir ama son konum ATILAMAZ: wheel koordinat
// tasimaz ve imlecin o an dogru yerde olmasina guvenir.
func coalesceQ(q *inputQueue, first protocol.ScreenInput) []protocol.ScreenInput {
	if first.Kind != "mousemove" {
		return []protocol.ScreenInput{first}
	}
	last := first
	for {
		next, ok := q.Pop()
		if !ok {
			return []protocol.ScreenInput{last}
		}
		if next.Kind != "mousemove" {
			return []protocol.ScreenInput{last, next}
		}
		last = next
	}
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
	// Yalniz kayit HALA bu oturumu gosteriyorsa sil: ayni kimlikle yeniden
	// acilmis daha yeni oturumun kaydi bozulmasin.
	removed := sm.sessions[ss.id] == ss
	if removed {
		delete(sm.sessions, ss.id)
	}
	cb := sm.onChanged
	sm.mu.Unlock()
	ss.once.Do(func() { ss.cancel() })
	if removed && cb != nil {
		cb()
	}
}

// sendInputStatus, girdi uygulama durumunu sunucuya bildirir.
func (sm *screenManager) sendInputStatus(ctx context.Context, id string, res inputResult) {
	_ = sm.cs.sendControl(ctx, protocol.ScreenInputStatus{
		Type:      protocol.TypeScreenInputStatus,
		SessionID: id,
		State:     res.State,
		Code:      res.Code,
		Detail:    res.Detail,
	}, protocol.TypeScreenInputStatus)
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
