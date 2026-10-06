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

	// bounds, YAKALANAN monitorun sanal masaustundeki dikdortgeni.
	// Girdi koordinatlari bu dikdortgen icinde yorumlanir; aksi halde ikinci
	// monitoru izlerken tiklamalar birinciye giderdi.
	bounds displayRect

	// input, girdi olaylarinin kuyrugu.
	//
	// NEDEN KUYRUK: onceden applyInput WebSocket OKUMA DONGUSUNDE senkron
	// calisiyordu. Tarayici saniyede 60-125 mousemove uretir ve her biri
	// SendInput syscall'i demektir; bu sure boyunca ayni istemcinin TUM tunel
	// trafigi (HTTP govde kareleri, terminal) bekliyordu. Artik olaylar
	// kuyruga birakilir ve ayri bir goroutine uygular.
	input chan protocol.ScreenInput

	// inputLog, oturum basina yalnizca ILK girdi olayinda log atmak icin.
	// FAZ 7 tanilama: "girdi alinmiyor" raporunda, girdinin sunucudan agent'a
	// ULASIP ulasmadigini loglardan kesin ayirt edebilmek icin.
	inputLog sync.Once
}

// screenInputQueue, oturum basina bekleyen girdi olayi siniri.
//
// Kucuk bilincli: kuyruk buyurse kullanici fareyi biraktiktan sonra bile
// imlec hareket etmeye devam eder (gecikmis olaylar). Dolunca mousemove
// DUSURULUR ama tus/dugme olaylari ASLA — bir mouseup'i dusurmek fareyi
// uzak makinede basili birakirdi.
const screenInputQueue = 64

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
	// defaultFPS, dashboard bir hedef FPS bildirmezse kullanilir. Eskiden 5'ti
	// ve akis "cok yavas" hissettiriyordu (FAZ 7 kullanici raporu). Dashboard
	// artik acikca FPS gonderiyor; bu yalnizca guvenlik tabani.
	defaultFPS      = 12
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

	// Yakalanan monitorun sanal masaustundeki yeri: girdi koordinatlari bunun
	// icinde yorumlanacak.
	b := screenshot.GetDisplayBounds(display)
	ss := &screenSession{
		id:     msg.SessionID,
		cancel: cancel,
		bounds: displayRect{x: b.Min.X, y: b.Min.Y, w: b.Dx(), h: b.Dy()},
		input:  make(chan protocol.ScreenInput, screenInputQueue),
	}

	sm.mu.Lock()
	sm.sessions[msg.SessionID] = ss
	cb := sm.onChanged
	sm.mu.Unlock()
	if cb != nil {
		cb()
	}

	// Girdi uygulayici: okuma dongusunden AYRI goroutine.
	go sm.applyLoop(ctx, ss)

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

		capStart := time.Now()
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
		captureMS := int(time.Since(capStart).Milliseconds())

		encStart := time.Now()
		out := scaleDown(img, maxWidth)

		buf.Reset()
		if err := jpeg.Encode(buf, out, &jpeg.Options{Quality: quality}); err != nil {
			sm.sendError(ctx, ss.id, "kare kodlanamadi: "+err.Error())
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
			"session", msg.SessionID, "kind", msg.Kind, "os", runtime.GOOS)
	})

	select {
	case ss.input <- msg:
	default:
		// Kuyruk dolu. mousemove atilabilir (bir sonraki zaten daha guncel
		// konumu tasir), ama dugme/tus olaylari ATILAMAZ: dusurulen bir
		// mouseup fareyi uzak makinede basili birakir, dusurulen bir keyup
		// tusu takili birakir.
		if msg.Kind == "mousemove" {
			return
		}
		// Kritik olay: en eski bekleyeni at, yer ac, tekrar dene.
		select {
		case <-ss.input:
		default:
		}
		select {
		case ss.input <- msg:
		default:
		}
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

	for {
		select {
		case <-ctx.Done():
			return
		case msg := <-ss.input:
			for _, ev := range coalesce(ss.input, msg) {
				applyInput(ev, ss.bounds)
			}
		}
	}
}

// coalesce, kuyrukta bekleyen ardisik mousemove'lari tek bir olaya indirger ve
// uygulanacak olaylari SIRAYLA doner.
//
// Neden: ara konumlari uygulamak anlamsizdir — imlec zaten en yeni konuma
// gidecek — ama son konum ATILAMAZ, cunku wheel olayi koordinat TASIMAZ ve
// imlecin o an dogru yerde olmasina guvenir.
//
// Donen dilim en fazla iki olay icerir: (birlestirilmis son mousemove) ve
// (onu takip eden mousemove olmayan olay).
func coalesce(q chan protocol.ScreenInput, first protocol.ScreenInput) []protocol.ScreenInput {
	if first.Kind != "mousemove" {
		return []protocol.ScreenInput{first}
	}
	last := first
	for {
		select {
		case next := <-q:
			if next.Kind != "mousemove" {
				// Once son konumu uygula, sonra bu olayi: sira korunur.
				return []protocol.ScreenInput{last, next}
			}
			last = next
		default:
			return []protocol.ScreenInput{last}
		}
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
