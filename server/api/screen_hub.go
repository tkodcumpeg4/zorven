package api

import (
	"context"
	"encoding/json"
	"errors"
	"strconv"
	"sync"
	"time"

	"github.com/tkodcumpeg4/zorven/server/tunnel"
	"github.com/tkodcumpeg4/zorven/shared/protocol"
)

// Paylasimli screen2 yayini: ayni istemciyi izleyen tum panel WS'leri TEK bir
// istemci yakalama oturumunu paylasir. Ilk izleyici OpenScreen2 acar, sonraki
// izleyiciler akisa katilir (katilimda anahtar kare istenir), son izleyici
// ayrilinca CloseScreen cagrilir. Eski mjpeg/h264 yolu izleyici basina kalir.
//
// Ayar degisikligi (fps/kalite/genislik/monitor): ESKI ajanlarda SON YAZAN
// KAZANIR; degisiklik tum izleyicilere {"type":"settings_changed",...} ile
// bildirilir. Ajan FeatureScreen2Layers destekliyorsa (screen_layers.go) her
// izleyicinin ayari yalniz kendini etkiler.

// maxScreenViewers, tek bir paylasimli akisin kabul ettigi en fazla izleyici.
const maxScreenViewers = 8

const (
	screen2KeyframeRetryJoin = 1100 * time.Millisecond
	screen2KeyframeRetryMin  = time.Second
)

// ErrTooManyViewers, akis izleyici sinirina ulastiginda doner.
var ErrTooManyViewers = errors.New("ekran akisi izleyici siniri dolu")

// screen2Agent, paylasimli yayinin istemci oturumundan kullandigi yuzey
// (*tunnel.Session bunu karsilar; testler sahte uygular).
type screen2Agent interface {
	OpenScreen2(ctx context.Context, sessionID string, o tunnel.Screen2Options,
		frames chan<- []byte, errs chan<- protocol.ScreenError) (uint64, error)
	Screen2Keyframe(ctx context.Context, sessionID string) error
	Screen2Settings(ctx context.Context, sessionID string, o tunnel.Screen2Options) error
	CloseScreen(ctx context.Context, sessionID string)
	SubscribeScreenInputStatus(sessionID string, ch chan<- protocol.ScreenInputStatus) bool
	ScreenInput(ctx context.Context, in protocol.ScreenInput) error
	// Imlec katmani ve pano (screen_aux.go). Pazarlanmamissa false doner.
	SubscribeScreenAux(sessionID string, ch chan<- tunnel.ScreenAux) (cursor, clipboard bool)
	ScreenClipboardSet(ctx context.Context, sessionID, text string) error
	ScreenClipboardWatch(ctx context.Context, sessionID string, enable bool) error
}

// screen2Registry, istemci kimligine gore canli yayinlari tutar.
type screen2Registry struct {
	mu   sync.Mutex
	hubs map[string]*screen2Hub
}

// active, istemcide canli bir paylasimli yayin var mi.
func (r *screen2Registry) active(clientID string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	h := r.hubs[clientID]
	return h != nil && !h.isClosed()
}

// screen2Viewer, yayina bagli tek bir panel WS'i.
type screen2Viewer struct {
	hub  *screen2Hub
	out  chan []byte // ikili screen2 mesajlari (sinirli; dolunca anahtar kareye kadar dusurulur)
	msgs chan []byte // JSON kontrol mesajlari (sinirli, en iyi caba)
	cur  chan []byte // imlec konumu: kapasite 1, en yeni kazanir (birlestirilir)

	// hub.mu altinda
	resync   bool
	viewOnly bool
	locked   bool   // sunucu zorlamali: izleyici kendisi acamaz
	fileTok  string // dosya aktarimi HTTP uclari icin izleyiciye ozel belirtec (screen_files.go)

	// Katmanli mod (screen_layers.go); hepsi hub.mu altinda.
	fps, quality, maxWidth int // izleyicinin kendi ayari
	pref                   int // prefAuto | layerHigh | layerLow
	auto                   autoLayer
	layer                  int // etkin katman
	dirty                  []bool
	dirtyN                 int
	needSync               bool // sonraki tur tuval (yeniden) kurar: keyframe bayrakli
	syncing                bool // sync turu kuyruk butcesi yuzunden parcali: fps araligina takilma
	seq                    uint32
	lastSend               time.Time
	gone                   bool
	// Geri bildirim sayaclari (screen_feedback.go); hub.mu altinda.
	fbArrived, fbDelivered, fbBlocked int
	fbQueueMax                        time.Duration
	fbPrev                            fbWin
	fbLast                            viewerFB
	dirtySince                        time.Time // en eski bekleyen karonun gelis zamani (katmanli)
	resyncSince                       time.Time // eski yayinda atlama baslangici
	wake                              chan struct{}
	quit                              chan struct{}
	vid                               videoState // screen_video.go; hub.mu altinda
}

// screen2Hub, tek istemcinin paylasimli yayini.
type screen2Hub struct {
	reg      *screen2Registry
	clientID string
	agent    screen2Agent
	sid      string

	ready   chan struct{} // acilis bitince kapanir
	openErr error
	inpOK   bool // istemci girdi durumu bildiriyor mu
	curOK   bool // istemci imlec katmani pazarladi mi
	clipOK  bool // istemci pano pazarladi mi
	filesOK bool // istemci dosya aktarimi pazarladi mi

	// Katmanli mod: ajan FeatureScreen2Layers pazarladiysa true (yayin boyunca sabit).
	layered   bool
	lagent    layerAgent
	stores    [numLayers]layerStore // hub.mu altinda
	layerMu   sync.Mutex            // syncLayers istek sirasi
	wasActive [numLayers]bool       // hub.mu altinda
	sentSpecs []protocol.Screen2LayerSpec
	sentFPS   int

	// H.264 video modu (screen_video.go); hub.mu altinda.
	vid videoHubState

	// Geri bildirim (screen_feedback.go): ajan pazarladiysa fbOK. fbSent* ve
	// fbTickAt hub.mu altinda.
	fbOK     bool
	clock    func() time.Time // yalniz testler
	fbAgent  feedbackAgent
	fbSent   []protocol.Screen2FeedbackLayer
	fbSentAt time.Time
	fbTickAt time.Time

	mu       sync.Mutex
	viewers  map[*screen2Viewer]struct{}
	closed   bool
	opts     tunnel.Screen2Options // son istenen ayar (0 = istemci varsayilani)
	release  func()                // kiraci ekran slotu: akis basina bir tane
	lastKF   time.Time
	inpState []byte        // son input_status JSON (katilana iletilir)
	curMsg   []byte        // son imlec konumu JSON (katilana iletilir)
	shapeMsg []byte        // son imlec bicimi JSON (katilana iletilir)
	watching bool          // istemciye gonderilen son pano izleme istegi
	watchMu  sync.Mutex    // pano izleme isteklerini siralar
	done     chan struct{} // yayin kapaninca (istemci hatasi dahil) kapanir

	raw  chan []byte
	errs chan protocol.ScreenError
	inp  chan protocol.ScreenInputStatus
	aux  chan tunnel.ScreenAux
	stop context.CancelFunc
	ctx  context.Context
}

func (h *screen2Hub) isClosed() bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.closed
}

// join, istemciye ait yayina izleyici ekler; yoksa yayini acar. release: bu
// izleyicinin bileti icin alinmis slot; yayin zaten slot tutuyorsa hemen
// birakilir (akis basina tek slot), yoksa yayin devralir ve son izleyici
// ayrilinca birakir.
func (r *screen2Registry) join(clientID string, agent screen2Agent, o tunnel.Screen2Options,
	viewOnly, locked bool, release func()) (*screen2Viewer, error) {
	for {
		r.mu.Lock()
		if r.hubs == nil {
			r.hubs = map[string]*screen2Hub{}
		}
		h := r.hubs[clientID]
		if h != nil && h.agent != agent {
			// Istemci yeniden baglanmis: eski yayin olu.
			h.shutdownLocked(r)
			h = nil
		}
		creator := false
		if h == nil {
			ctx, cancel := context.WithCancel(context.Background())
			h = &screen2Hub{
				reg: r, clientID: clientID, agent: agent, sid: "scr_" + randomHex(6),
				ready: make(chan struct{}), viewers: map[*screen2Viewer]struct{}{},
				opts: o, done: make(chan struct{}),
				raw: make(chan []byte, 128), errs: make(chan protocol.ScreenError, 1),
				inp: make(chan protocol.ScreenInputStatus, 4),
				aux: make(chan tunnel.ScreenAux, 64), ctx: ctx, stop: cancel,
			}
			r.hubs[clientID] = h
			creator = true
		}
		r.mu.Unlock()

		if creator {
			h.inpOK = agent.SubscribeScreenInputStatus(h.sid, h.inp)
			h.curOK, h.clipOK = agent.SubscribeScreenAux(h.sid, h.aux)
			if fa, ok := agent.(fileAgent); ok {
				h.filesOK = fa.SupportsScreenFiles()
			}
			if fa, ok := agent.(feedbackAgent); ok && fa.SupportsScreen2Feedback() {
				h.fbOK, h.fbAgent = true, fa
			}
			if va, ok := agent.(videoAgent); ok && va.SupportsScreen2Video() {
				h.vid.ok, h.vid.agent, h.vid.sent = true, va, protocol.Screen2VideoTiles
			}
			if la, ok := agent.(layerAgent); ok && la.SupportsScreen2Layers() {
				// Ilk izleyicinin tercihine gore baslangic katmanlari.
				first := layerHigh
				if prefFromQuality(o.Quality) == layerLow {
					first = layerLow
				}
				specs, act := computeSpecs([]specIn{{first, o.MaxWidth, o.Quality}})
				o.Layers = specs
				h.layered, h.lagent, h.wasActive = true, la, act
				h.sentSpecs, h.sentFPS = specs, min(resolveFPS(o.FPS), agentFPSMax)
			}
			_, err := agent.OpenScreen2(h.ctx, h.sid, o, h.raw, h.errs)
			h.openErr = err
			if err != nil {
				r.mu.Lock()
				if r.hubs[clientID] == h {
					delete(r.hubs, clientID)
				}
				r.mu.Unlock()
				h.mu.Lock()
				h.closed = true
				h.mu.Unlock()
				h.stop()
				agent.CloseScreen(context.Background(), h.sid)
				close(h.done)
				close(h.ready)
				return nil, err
			}
			go h.pump()
			go h.feedbackLoop()
			close(h.ready)
		} else {
			<-h.ready
			if h.openErr != nil {
				continue // basarisiz yaratici: yeniden dene
			}
		}

		v := &screen2Viewer{
			hub: h, out: make(chan []byte, 32), msgs: make(chan []byte, 16), cur: make(chan []byte, 1),
			resync: !h.layered, viewOnly: viewOnly || locked, locked: locked, fileTok: randomHex(16),
			wake: make(chan struct{}, 1), quit: make(chan struct{}),
			fps: o.FPS, quality: o.Quality, maxWidth: o.MaxWidth, pref: prefFromQuality(o.Quality),
		}
		if h.layered {
			v.layer = layerHigh
			if v.pref == layerLow {
				v.layer = layerLow
			}
			v.auto = newAutoLayer(v.layer)
		}
		h.mu.Lock()
		if h.closed {
			h.mu.Unlock()
			continue
		}
		if len(h.viewers) >= maxScreenViewers {
			h.mu.Unlock()
			return nil, ErrTooManyViewers
		}
		if h.release == nil {
			h.release, release = release, nil
		}
		h.viewers[v] = struct{}{}
		if h.layered {
			v.resyncLocked()
		}
		inp := h.inpState
		cur := h.opts
		curMsg, shapeMsg := h.curMsg, h.shapeMsg
		h.mu.Unlock()
		if release != nil {
			release() // yayin zaten bir slot tutuyor
		}

		if !creator && !h.layered {
			// Katilana yayinin gecerli ayarini ve son girdi durumunu bildir.
			v.sendMsg(settingsMsg(cur))
		}
		if inp != nil {
			v.sendMsg(inp)
		}
		v.sendMsg(h.capsMsg())
		v.sendMsg(v.filesMsg())
		if shapeMsg != nil {
			v.sendMsg(shapeMsg)
		}
		if curMsg != nil {
			v.putCursor(curMsg)
		}
		h.broadcastViewers()
		if h.layered {
			// Depodan tam goruntu: ajandan anahtar kare gerekmez. Yeni katman
			// etkinlesiyorsa syncLayers key ile tam kare ister.
			v.sendState()
			go v.senderLoop()
			h.syncLayers()
			time.AfterFunc(screen2KeyframeRetryJoin, func() {
				h.mu.Lock()
				still := !h.closed && !v.gone && h.stores[v.layer].w == 0
				h.mu.Unlock()
				if still {
					h.askKeyframe()
				}
			})
			return v, nil
		}
		h.askKeyframe()
		// Hiz sinirina takilirsa katilanin ilk karesi icin yeniden dene.
		time.AfterFunc(screen2KeyframeRetryJoin, func() {
			h.mu.Lock()
			still := v.resync && !h.closed
			h.mu.Unlock()
			if still {
				h.askKeyframe()
			}
		})
		return v, nil
	}
}

func settingsMsg(o tunnel.Screen2Options) []byte {
	m := map[string]any{"type": "settings_changed", "fps": o.FPS, "quality": o.Quality, "max_width": o.MaxWidth, "display": o.Display}
	b, _ := json.Marshal(m)
	return b
}

// capsMsg, yayinin imlec/pano yeteneklerini panele bildirir.
func (h *screen2Hub) capsMsg() []byte {
	b, _ := json.Marshal(map[string]any{"type": "caps", "cursor": h.curOK, "clipboard": h.clipOK, "files": h.filesOK, "video": h.vid.ok})
	return b
}

// putCursor, imlec konumunu birlestirmeli yuvaya koyar (eski bekleyen atilir).
func (v *screen2Viewer) putCursor(b []byte) {
	for {
		select {
		case v.cur <- b:
			return
		default:
		}
		select {
		case <-v.cur:
		default:
		}
	}
}

func (v *screen2Viewer) sendMsg(b []byte) {
	select {
	case v.msgs <- b:
	default: // yavas izleyici: kontrol mesaji kaybi kabul edilir
	}
}

// askKeyframe, istemciden anahtar kare ister (en fazla saniyede bir).
func (h *screen2Hub) askKeyframe() {
	h.mu.Lock()
	if h.closed || time.Since(h.lastKF) < screen2KeyframeRetryMin {
		h.mu.Unlock()
		return
	}
	h.lastKF = time.Now()
	h.mu.Unlock()
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = h.agent.Screen2Keyframe(ctx, h.sid)
	}()
}

// pump, istemciden gelen mesajlari izleyicilere dagitir; her izleyicinin kendi
// sinirli kuyrugu vardir, yavas izleyici digerlerini ASLA bekletmez.
func (h *screen2Hub) pump() {
	for {
		select {
		case <-h.ctx.Done():
			return
		case b := <-h.raw:
			if len(b) > 0 && b[0] == protocol.FrameVideo {
				h.relayVideo(b)
				continue
			}
			if h.layered {
				h.mu.Lock()
				h.ingestLocked(b)
				h.mu.Unlock()
				continue
			}
			keyframe, first := screen2Flags(b)
			eof := len(b) > 13 && b[13]&protocol.FlagEOF != 0
			needAsk := false
			h.mu.Lock()
			now := time.Now()
			for v := range h.viewers {
				if eof {
					v.fbArrived++
				}
				if v.resync {
					if !(keyframe && first) {
						needAsk = true
						continue
					}
					v.resync = false
				}
				select {
				case v.out <- b:
					if eof {
						v.fbDelivered++
					}
				default:
					if !v.resync {
						v.resyncSince = now
					}
					v.resync = true
					v.fbBlocked++
					needAsk = true
				}
			}
			h.mu.Unlock()
			if needAsk {
				h.askKeyframe()
			}
		case st := <-h.inp:
			msg, _ := json.Marshal(map[string]any{
				"type": "input_status", "state": st.State, "code": st.Code, "detail": st.Detail,
			})
			h.mu.Lock()
			h.inpState = msg
			for v := range h.viewers {
				v.sendMsg(msg)
			}
			h.mu.Unlock()
		case a := <-h.aux:
			h.relayAux(a)
		case e := <-h.errs:
			msg, _ := json.Marshal(map[string]any{"type": "error", "message": e.Message})
			h.mu.Lock()
			for v := range h.viewers {
				v.sendMsg(msg)
			}
			h.mu.Unlock()
			h.reg.mu.Lock()
			h.shutdownLocked(h.reg)
			h.reg.mu.Unlock()
			return
		}
	}
}

// relayAux, istemciden gelen imlec/pano mesajini izleyicilere dagitir.
// Imlec herkese gider (yalniz gorsel); pano YALNIZ kontrolcu (salt-izleme
// olmayan) izleyicilere gider — sunucuda zorlanir.
func (h *screen2Hub) relayAux(a tunnel.ScreenAux) {
	switch {
	case a.Cursor != nil:
		msg, _ := json.Marshal(map[string]any{
			"type": "cursor", "x": a.Cursor.X, "y": a.Cursor.Y, "visible": a.Cursor.Visible,
			"w": a.Cursor.W, "h": a.Cursor.H,
		})
		h.mu.Lock()
		h.curMsg = msg
		for v := range h.viewers {
			v.putCursor(msg)
		}
		h.mu.Unlock()
	case a.Shape != nil:
		msg, _ := json.Marshal(map[string]any{
			"type": "cursor_shape", "w": a.Shape.W, "h": a.Shape.H,
			"hot_x": a.Shape.HotX, "hot_y": a.Shape.HotY, "data": a.Shape.Data,
		})
		h.mu.Lock()
		h.shapeMsg = msg
		for v := range h.viewers {
			v.sendMsg(msg)
		}
		h.mu.Unlock()
	case a.Stats != nil:
		// Ajan olcumu icerigi degistirilmeden iletilir; katmanli yayinda her
		// izleyiciye kendi etkin katmani eklenir (-1: katmansiz).
		raw, err := json.Marshal(a.Stats)
		if err != nil {
			return
		}
		var m map[string]json.RawMessage
		if json.Unmarshal(raw, &m) != nil {
			return
		}
		delete(m, "session_id")
		m["type"] = json.RawMessage(`"stats"`)
		h.mu.Lock()
		for v := range h.viewers {
			m["layer"] = json.RawMessage("-1")
			if h.layered {
				m["layer"] = json.RawMessage(strconv.Itoa(v.layer))
			}
			// Sunucunun bu izleyiciye teslim ozeti (varsa).
			delete(m, "srv")
			if srv := v.srvLocked(h.fbOK); srv != nil {
				if sb, err := json.Marshal(srv); err == nil {
					m["srv"] = sb
				}
			}
			b, _ := json.Marshal(m)
			v.sendMsg(b)
		}
		h.mu.Unlock()
	case a.Clipboard != nil:
		if !protocol.ClipboardTextOK(a.Clipboard.Text) {
			return
		}
		msg, _ := json.Marshal(map[string]any{"type": "clipboard", "text": a.Clipboard.Text})
		h.mu.Lock()
		for v := range h.viewers {
			if !v.viewOnly {
				v.sendMsg(msg)
			}
		}
		h.mu.Unlock()
	}
}

// syncClipboardWatch, istemci panosunu YALNIZ kontrolcu izleyici varken izletir;
// istek sirasi seri ve son istenen durum kazanir.
func (h *screen2Hub) syncClipboardWatch() {
	if !h.clipOK {
		return
	}
	go func() {
		h.watchMu.Lock()
		defer h.watchMu.Unlock()
		h.mu.Lock()
		want := false
		if !h.closed {
			for v := range h.viewers {
				if !v.viewOnly {
					want = true
					break
				}
			}
		}
		same := want == h.watching
		h.watching = want
		h.mu.Unlock()
		if same {
			return
		}
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = h.agent.ScreenClipboardWatch(ctx, h.sid, want)
	}()
}

// shutdownLocked, yayini kapatir (reg.mu elde). Izleyiciler done ile haberdar olur.
func (h *screen2Hub) shutdownLocked(r *screen2Registry) {
	h.mu.Lock()
	if h.closed {
		h.mu.Unlock()
		return
	}
	h.closed = true
	rel := h.release
	h.release = nil
	h.mu.Unlock()
	if r.hubs[h.clientID] == h {
		delete(r.hubs, h.clientID)
	}
	h.stop()
	h.agent.CloseScreen(context.Background(), h.sid)
	close(h.done)
	if rel != nil {
		rel()
	}
}

func (h *screen2Hub) broadcastViewers() {
	h.mu.Lock()
	defer h.mu.Unlock()
	n, c := len(h.viewers), 0
	for v := range h.viewers {
		if !v.viewOnly {
			c++
		}
	}
	msg, _ := json.Marshal(map[string]any{"type": "viewers", "count": n, "controllers": c})
	for v := range h.viewers {
		v.sendMsg(msg)
	}
	h.syncClipboardWatch()
}

// leave, izleyiciyi cikarir; son izleyiciyse yayini kapatir (CloseScreen).
func (v *screen2Viewer) leave() {
	h := v.hub
	r := h.reg
	r.mu.Lock()
	h.mu.Lock()
	if _, ok := h.viewers[v]; !ok {
		h.mu.Unlock()
		r.mu.Unlock()
		return
	}
	delete(h.viewers, v)
	if !v.gone {
		v.gone = true
		close(v.quit)
	}
	empty := len(h.viewers) == 0
	h.mu.Unlock()
	if empty {
		h.shutdownLocked(r)
	}
	r.mu.Unlock()
	if !empty {
		h.broadcastViewers()
		h.syncLayers()
		h.syncVideo(false)
	}
}

// settings, ayar isteklerini uygular (son yazan kazanir) ve herkese bildirir.
func (v *screen2Viewer) settings(o tunnel.Screen2Options) {
	h := v.hub
	h.mu.Lock()
	if h.closed {
		h.mu.Unlock()
		return
	}
	if o.FPS > 0 {
		h.opts.FPS = o.FPS
	}
	if o.Quality > 0 {
		h.opts.Quality = o.Quality
	}
	if o.MaxWidth > 0 {
		h.opts.MaxWidth = o.MaxWidth
	}
	if o.Display >= 0 {
		h.opts.Display = o.Display
	}
	msg := settingsMsg(h.opts)
	for w := range h.viewers {
		w.sendMsg(msg)
	}
	h.mu.Unlock()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = h.agent.Screen2Settings(ctx, h.sid, o)
}

// setViewOnly, izleyicinin salt-izleme durumunu degistirir (kilitliyse hep
// true). Gecerli durumu doner ve izleyiciye {"type":"view_only"} yollar.
func (v *screen2Viewer) setViewOnly(val bool) bool {
	h := v.hub
	h.mu.Lock()
	if v.locked {
		val = true
	}
	changed := v.viewOnly != val
	v.viewOnly = val
	h.mu.Unlock()
	v.sendMsg(v.viewOnlyMsg())
	v.sendMsg(v.filesMsg())
	if changed {
		h.broadcastViewers()
	}
	return val
}

func (v *screen2Viewer) viewOnlyMsg() []byte {
	v.hub.mu.Lock()
	defer v.hub.mu.Unlock()
	b, _ := json.Marshal(map[string]any{"type": "view_only", "value": v.viewOnly, "locked": v.locked})
	return b
}

// input, girdi olayini iletir; salt-izleyen izleyicinin girdisi SUNUCUDA dusurulur.
func (v *screen2Viewer) input(ctx context.Context, in protocol.ScreenInput) bool {
	v.hub.mu.Lock()
	vo := v.viewOnly
	v.hub.mu.Unlock()
	if vo {
		return false
	}
	in.SessionID = v.hub.sid
	_ = v.hub.agent.ScreenInput(ctx, in)
	return true
}

// keyframe: katmanli yayinda izleyiciye depodan tam goruntu yeniden gonderilir
// (ajan ve diger izleyiciler etkilenmez); eski yayinda ajandan anahtar kare istenir.
func (v *screen2Viewer) keyframe() {
	h := v.hub
	if h.vid.ok && v.videoActive() {
		h.askKeyframe() // video akiyor: IDR (depodaki eski karolar videonun ustune cizilmesin)
		return
	}
	if !h.layered {
		h.askKeyframe()
		return
	}
	h.mu.Lock()
	if _, ok := h.viewers[v]; ok {
		v.resyncLocked()
	}
	h.mu.Unlock()
}

// Pano reddi nedenleri (clipboard_status mesajindaki "reason").
const (
	clipReasonViewOnly    = "view_only"
	clipReasonUnsupported = "unsupported"
	clipReasonTooLarge    = "too_large"
	clipReasonInvalid     = "invalid"
)

// clipboard, panelin panosunu istemciye iletir. Salt-izleyen izleyici, desteksiz
// istemci, asiri buyuk veya gecersiz metin SUNUCUDA reddedilir; ret "" ise
// iletildi, degilse red nedeni (izleyiciye clipboard_status olarak bildirilir).
func (v *screen2Viewer) clipboard(ctx context.Context, text string) string {
	h := v.hub
	h.mu.Lock()
	vo := v.viewOnly
	h.mu.Unlock()
	reason := ""
	switch {
	case vo:
		reason = clipReasonViewOnly
	case !h.clipOK:
		reason = clipReasonUnsupported
	case len(text) > protocol.MaxScreenClipboardSize:
		reason = clipReasonTooLarge
	case !protocol.ClipboardTextOK(text):
		reason = clipReasonInvalid
	}
	if reason != "" {
		b, _ := json.Marshal(map[string]any{"type": "clipboard_status", "ok": false, "reason": reason})
		v.sendMsg(b)
		return reason
	}
	if err := h.agent.ScreenClipboardSet(ctx, h.sid, text); err != nil {
		b, _ := json.Marshal(map[string]any{"type": "clipboard_status", "ok": false, "reason": clipReasonUnsupported})
		v.sendMsg(b)
		return clipReasonUnsupported
	}
	b, _ := json.Marshal(map[string]any{"type": "clipboard_status", "ok": true})
	v.sendMsg(b)
	return ""
}
