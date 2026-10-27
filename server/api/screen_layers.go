package api

import (
	"context"
	"encoding/json"
	"time"

	"github.com/tkodcumpeg4/zorven/server/tunnel"
	"github.com/tkodcumpeg4/zorven/shared/protocol"
)

// Katmanli paylasimli screen2 (FeatureScreen2Layers).
//
// Istemci YALNIZ abonesi olan katmanlari, karo (64 px) hizali ve karo basina
// bir dikdortgen olarak yollar. Sunucu her katman icin karo basina SON kodlanmis
// yuku tutar (layerStore). Her izleyicinin kendi kirli-karo kumesi ve kendi
// gonderici dongusu vardir: kuyrugunda yer ve kendi fps araligi geldikce yalniz
// kirli karolarin EN GUNCEL halini yollar (ara surumler birlesir) ve kumeyi
// temizler. Yeni izleyici = tum karolar kirli (ajandan anahtar kare gerekmez).
// Yavas izleyici otomatik daha dusuk fps alir ama tam goruntuye sahip olur;
// hizli izleyiciyi hic bekletmez. Ayar degisikligi YALNIZ o izleyiciyi etkiler.

const (
	layerHigh = 0
	layerLow  = 1
	numLayers = 2

	s2Tile = 64

	prefAuto = -1 // izleyici katmani: sunucu secer (gecikmeli gecis)

	screen2DefaultWidth = 1600
	screen2MinLowWidth  = 160
	layerQualityLow     = 40
	layerQualityHigh    = 90 // izleyici acikca "yuksek" kalite istediyse
	layerQualityMid     = 75 // aksi halde yuksek katman
	viewerFPSDefault    = 15
	viewerFPSMax        = 30
	agentFPSMax         = 30

	// gonderici: bir turda en fazla bu kadar yuk (kuyrukta yer oranina gore azalir).
	senderMsgBytes = 180 << 10
)

// layerAgent, katmanli modu destekleyen istemci oturumu (*tunnel.Session);
// screen2Agent'a EKLENMEZ ki eski sahte uygulamalar ve eski ajan yolu bozulmasin.
type layerAgent interface {
	SupportsScreen2Layers() bool
	Screen2Layers(ctx context.Context, sessionID string, layers []protocol.Screen2LayerSpec, key bool) error
}

// tileEntry, bir karonun son kodlanmis hali. payload degismez (hic mutasyona
// ugramaz, yalniz degistirilir): izleyiciler kopyalamadan paylasir.
type tileEntry struct {
	enc     uint8
	payload []byte
}

// layerStore, bir katmanin karo deposu. w==0 => gecersiz/bos.
type layerStore struct {
	w, h, tx, ty int
	tiles        []tileEntry
}

func (s *layerStore) reset(w, h int) {
	s.w, s.h = w, h
	s.tx, s.ty = (w+s2Tile-1)/s2Tile, (h+s2Tile-1)/s2Tile
	s.tiles = make([]tileEntry, s.tx*s.ty)
}

func (s *layerStore) clear() { *s = layerStore{} }

// bytes, depodaki toplam yuk (tanilama/test).
func (s *layerStore) bytes() int {
	n := 0
	for _, t := range s.tiles {
		n += len(t.payload)
	}
	return n
}

// --- otomatik katman secimi (histerezis) -----------------------------------

const (
	autoIdle = 0 // bekleyen is yok: nötr
	autoOK   = 1 // is var, kuyrukta yer var
	autoBad  = 2 // is var, kuyruk dolu (izleyici yetisemiyor)

	autoWindow    = 2 * time.Second
	autoDownWins  = 2 // ardisik kotu pencere => dusuk katman (~4 sn)
	autoUpWinsMin = 6 // ardisik iyi pencere => yuksek katman (~12 sn)
	autoUpWinsMax = 60
	autoHold      = 8 * time.Second  // iki gecis arasi en az sure
	autoFlapSpan  = 40 * time.Second // yukseldikten bu kadar sure icinde yeniden dusus => bir sonraki yukselis daha zor
)

// autoLayer, izleyicinin kendi kuyruk baskisina bakarak yuksek/dusuk katman
// secer. Zaman disaridan verilir (test edilebilir).
type autoLayer struct {
	layer       int
	init        bool
	winStart    time.Time
	lastT       time.Time
	lastState   int
	bad, active time.Duration
	badWins     int
	goodWins    int
	upNeed      int
	lastChange  time.Time
	lastUp      time.Time
}

func newAutoLayer(layer int) autoLayer { return autoLayer{layer: layer, upNeed: autoUpWinsMin} }

// Observe, o anki durumu bildirir; katman degistiyse (yeni, true) doner.
func (a *autoLayer) Observe(now time.Time, st int) (int, bool) {
	if !a.init {
		a.init = true
		a.winStart, a.lastT, a.lastState = now, now, st
		a.lastChange = now.Add(-autoHold)
		return a.layer, false
	}
	if dt := now.Sub(a.lastT); dt > 0 && a.lastState != autoIdle {
		a.active += dt
		if a.lastState == autoBad {
			a.bad += dt
		}
	}
	a.lastT, a.lastState = now, st
	if now.Sub(a.winStart) < autoWindow {
		return a.layer, false
	}
	switch {
	case a.active < 200*time.Millisecond:
		// bosta: kanit yok, sayaclar korunur
	case a.active >= 300*time.Millisecond && a.bad*10 >= a.active*4:
		a.badWins++
		a.goodWins = 0
	case a.bad*20 < a.active:
		a.goodWins++
		a.badWins = 0
	default:
		a.badWins, a.goodWins = 0, 0
	}
	a.winStart, a.bad, a.active = now, 0, 0

	held := now.Sub(a.lastChange) >= autoHold
	switch {
	case a.layer == layerHigh && a.badWins >= autoDownWins && held:
		if !a.lastUp.IsZero() && now.Sub(a.lastUp) < autoFlapSpan {
			a.upNeed = min(a.upNeed*2, autoUpWinsMax)
		}
		a.layer, a.lastChange = layerLow, now
		a.badWins, a.goodWins = 0, 0
		return a.layer, true
	case a.layer == layerLow && a.goodWins >= a.upNeed && held:
		a.layer, a.lastChange, a.lastUp = layerHigh, now, now
		a.badWins, a.goodWins = 0, 0
		return a.layer, true
	}
	return a.layer, false
}

// --- katman kumesi hesabi ---------------------------------------------------

// specIn, katman hesabi icin bir izleyicinin ozeti.
type specIn struct{ layer, maxWidth, quality int }

func resolveWidth(w int) int {
	if w <= 0 {
		return screen2DefaultWidth
	}
	return min(w, 16384)
}

func resolveFPS(f int) int {
	if f <= 0 {
		return viewerFPSDefault
	}
	return min(f, viewerFPSMax)
}

// computeSpecs, izleyici ozetlerinden etkin katman parametrelerini uretir.
// Yuksek katman: abonelerinin en genis istegi; kalite 90 (biri acikca yuksek
// istediyse) yoksa 75. Dusuk katman: tum izleyicilerin en genis isteginin yarisi,
// kalite 40. active[i]: katman i'nin abonesi var mi.
func computeSpecs(in []specIn) (specs []protocol.Screen2LayerSpec, active [numLayers]bool) {
	allMax, hiW, hiQ := 0, 0, false
	for _, v := range in {
		if v.layer < 0 || v.layer >= numLayers {
			continue
		}
		active[v.layer] = true
		w := resolveWidth(v.maxWidth)
		allMax = max(allMax, w)
		if v.layer == layerHigh {
			hiW = max(hiW, w)
			if v.quality >= 70 {
				hiQ = true
			}
		}
	}
	if active[layerHigh] {
		q := layerQualityMid
		if hiQ {
			q = layerQualityHigh
		}
		specs = append(specs, protocol.Screen2LayerSpec{ID: layerHigh, MaxWidth: hiW, Quality: q})
	}
	if active[layerLow] {
		specs = append(specs, protocol.Screen2LayerSpec{
			ID: layerLow, MaxWidth: max(allMax/2, screen2MinLowWidth), Quality: layerQualityLow,
		})
	}
	return
}

func specsEqual(a, b []protocol.Screen2LayerSpec) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// prefFromQuality, kalite tavanindan katman tercihini turetir (0 => otomatik).
func prefFromQuality(q int) int {
	switch {
	case q <= 0:
		return prefAuto
	case q < 70:
		return layerLow
	}
	return layerHigh
}

func prefFromName(s string) (int, bool) {
	switch s {
	case "auto":
		return prefAuto, true
	case "high":
		return layerHigh, true
	case "low":
		return layerLow, true
	}
	return 0, false
}

func layerName(l int) string {
	if l == layerLow {
		return "low"
	}
	return "high"
}

// --- hub: depo ve kirli kume ------------------------------------------------

func (h *screen2Hub) layerSubscribedLocked(l int) bool {
	for v := range h.viewers {
		if v.layer == l {
			return true
		}
	}
	return false
}

// ingestLocked, istemciden gelen bir katmanli mesaji depoya isler ve ilgili
// izleyicilerin kirli kumesini isaretler (h.mu elde).
func (h *screen2Hub) ingestLocked(b []byte) {
	m, err := protocol.DecodeScreen2(b)
	if err != nil {
		return
	}
	l := int(m.Layer)
	if l >= numLayers || !h.layerSubscribedLocked(l) {
		return
	}
	st := &h.stores[l]
	now := h.nowT()
	w, hh := int(m.W), int(m.H)
	if m.Keyframe && m.First && (st.w != w || st.h != hh) {
		st.reset(w, hh)
		for v := range h.viewers {
			if v.layer == l {
				v.dirty = make([]bool, len(st.tiles))
				v.dirtyN = 0
				v.needSync = true
				v.wakeUp()
			}
		}
	}
	if st.w != w || st.h != hh {
		return // anahtar kare gelene kadar (veya bozuk) yok say
	}
	for _, r := range m.Rects {
		x, y, rw, rh := int(r.X), int(r.Y), int(r.W), int(r.H)
		if x%s2Tile != 0 || y%s2Tile != 0 || rw != min(s2Tile, w-x) || rh != min(s2Tile, hh-y) {
			continue // karo hizali degil
		}
		idx := (y/s2Tile)*st.tx + x/s2Tile
		st.tiles[idx] = tileEntry{enc: r.Enc, payload: append([]byte(nil), r.Payload...)}
		for v := range h.viewers {
			if v.layer != l {
				continue
			}
			if len(v.dirty) != len(st.tiles) {
				v.dirty = make([]bool, len(st.tiles))
				v.dirtyN = 0
			}
			if !v.dirty[idx] {
				v.dirty[idx] = true
				v.dirtyN++
				if v.dirtySince.IsZero() {
					v.dirtySince = now
				}
			}
		}
	}
	for v := range h.viewers {
		if v.layer == l && m.EOF {
			v.fbArrived++
		}
		if v.layer == l && v.dirtyN > 0 {
			v.wakeUp()
		}
	}
}

func (v *screen2Viewer) wakeUp() {
	select {
	case v.wake <- struct{}{}:
	default:
	}
}

// kick, yazici bir mesaji tukettiginde kuyrukta yer acildigini haber verir.
func (v *screen2Viewer) kick() { v.wakeUp() }

// markAllLocked, deponun dolu tum karolarini bu izleyici icin kirli yapar.
func (v *screen2Viewer) markAllLocked() {
	st := &v.hub.stores[v.layer]
	if len(v.dirty) != len(st.tiles) {
		v.dirty = make([]bool, len(st.tiles))
	}
	v.dirtyN = 0
	for i, t := range st.tiles {
		if t.payload != nil {
			v.dirty[i] = true
			v.dirtyN++
		} else {
			v.dirty[i] = false
		}
	}
	if v.dirtyN == 0 {
		v.dirtySince = time.Time{}
	} else if v.dirtySince.IsZero() {
		v.dirtySince = v.hub.nowT()
	}
}

// resyncLocked, izleyiciye depodan tam goruntuyu yeniden yollatir.
func (v *screen2Viewer) resyncLocked() {
	v.needSync = true
	v.markAllLocked()
	v.wakeUp()
}

// setLayerLocked, izleyicinin etkin katmanini degistirir; yeni katmanin tam
// goruntusu deposundan gonderilir.
func (v *screen2Viewer) setLayerLocked(l int) bool {
	if v.layer == l {
		return false
	}
	v.layer = l
	v.dirty = nil
	v.resyncLocked()
	return true
}

// effectiveLayerLocked, tercihe gore izleyicinin katmani.
func (v *screen2Viewer) effectiveLayerLocked() int {
	if v.pref == prefAuto {
		return v.auto.layer
	}
	return v.pref
}

func (v *screen2Viewer) interval() time.Duration {
	return time.Second / time.Duration(resolveFPS(v.fps))
}

type tileBatch struct {
	rects []protocol.Screen2Rect
	w, h  uint16
	key   bool
	layer uint8
	seq   uint32
}

// takeBatchLocked, kirli karolarin en guncel halini (en fazla maxBytes yuk)
// alir ve kirli isaretini temizler. ok=false iken wait>0 ise fps araligi
// dolana kadar beklenmeli. Sync turu (katman degisimi/katilim) fps araligina
// takilmaz, boylece tam goruntu hizla gelir.
func (v *screen2Viewer) takeBatchLocked(now time.Time, maxBytes int) (b tileBatch, wait time.Duration, ok bool) {
	st := &v.hub.stores[v.layer]
	if st.w == 0 || len(v.dirty) != len(st.tiles) {
		if st.w != 0 {
			v.markAllLocked()
		} else {
			return b, 0, false
		}
	}
	if !v.needSync && v.dirtyN == 0 {
		return b, 0, false
	}
	if !v.syncing && !v.needSync && !v.lastSend.IsZero() {
		if el := now.Sub(v.lastSend); el < v.interval() {
			return b, v.interval() - el, false
		}
	}
	// Geri bildirim: bekleme yasi (sync turlari tikanma sayilmaz) ve teslim sayaci.
	if !v.needSync && !v.syncing && !v.dirtySince.IsZero() {
		v.fbQueueMax = max(v.fbQueueMax, now.Sub(v.dirtySince))
	}
	v.fbDelivered++
	size := 0
	for i := 0; i < len(v.dirty) && (size < maxBytes || len(b.rects) == 0); i++ {
		if !v.dirty[i] {
			continue
		}
		t := st.tiles[i]
		v.dirty[i] = false
		v.dirtyN--
		if t.payload == nil {
			continue
		}
		x, y := (i%st.tx)*s2Tile, (i/st.tx)*s2Tile
		b.rects = append(b.rects, protocol.Screen2Rect{
			X: uint16(x), Y: uint16(y), W: uint16(min(s2Tile, st.w-x)), H: uint16(min(s2Tile, st.h-y)),
			Enc: t.enc, Payload: t.payload,
		})
		size += len(t.payload) + protocol.Screen2RectHeaderSize
	}
	b.w, b.h, b.key, b.layer = uint16(st.w), uint16(st.h), v.needSync, uint8(v.layer)
	if v.dirtyN > 0 {
		v.dirtySince = now // kalan karolar simdiden itibaren bekler
	} else {
		v.dirtySince = time.Time{}
	}
	b.seq = v.seq
	v.seq++
	v.syncing = v.needSync && v.dirtyN > 0
	v.needSync = false
	v.lastSend = now
	return b, 0, true
}

// --- gonderici dongusu ------------------------------------------------------

func (v *screen2Viewer) senderLoop() {
	h := v.hub
	tick := time.NewTicker(time.Second)
	defer tick.Stop()
	var timer *time.Timer
	defer func() {
		if timer != nil {
			timer.Stop()
		}
	}()
	for {
		now := time.Now()
		h.mu.Lock()
		if h.closed || v.gone {
			h.mu.Unlock()
			return
		}
		room := cap(v.out) - len(v.out)
		st := &h.stores[v.layer]
		pending := v.dirtyN > 0 || (v.needSync && st.w != 0)
		var (
			b    tileBatch
			wait time.Duration
			ok   bool
			obs  = autoIdle
		)
		switch {
		case !pending:
		case room < 2:
			obs = autoBad
			v.fbBlocked++
		default:
			obs = autoOK
			b, wait, ok = v.takeBatchLocked(now, min(room-1, 5)*senderMsgBytes)
		}
		changed := false
		if v.pref == prefAuto {
			if l, c := v.auto.Observe(now, obs); c {
				changed = v.setLayerLocked(l)
			}
		}
		h.mu.Unlock()
		if changed {
			h.syncLayers()
			v.sendState()
		}
		if ok {
			msgs, err := protocol.PackScreen2Layer(0, b.seq, b.key, b.layer, b.w, b.h, b.rects, senderMsgBytes+(20<<10))
			if err == nil {
				for _, m := range msgs {
					select {
					case v.out <- m:
					case <-v.quit:
						return
					case <-h.done:
						return
					}
				}
			}
			continue
		}
		var tc <-chan time.Time
		if wait > 0 {
			if timer == nil {
				timer = time.NewTimer(wait)
			} else {
				timer.Reset(wait)
			}
			tc = timer.C
		}
		select {
		case <-v.quit:
			return
		case <-h.done:
			return
		case <-v.wake:
		case <-tick.C:
		case <-tc:
		}
		if timer != nil && tc != nil {
			if !timer.Stop() {
				select {
				case <-timer.C:
				default:
				}
			}
		}
	}
}

// --- durum ve ayar -----------------------------------------------------------

func (v *screen2Viewer) stateMsg() []byte {
	h := v.hub
	h.mu.Lock()
	defer h.mu.Unlock()
	b, _ := json.Marshal(map[string]any{
		"type": "viewer_state", "layered": true, "layer": layerName(v.layer),
		"auto": v.pref == prefAuto, "fps": resolveFPS(v.fps),
	})
	return b
}

func (v *screen2Viewer) sendState() { v.sendMsg(v.stateMsg()) }

// applySettings, panelin ayar mesajini uygular. Katmanli yayinda YALNIZ bu
// izleyiciyi etkiler (monitor secimi yayin geneli olmak zorunda); eski ajanda
// son yazan kazanir.
func (v *screen2Viewer) applySettings(c screenControl) {
	if !v.hub.layered {
		v.settings(c.opts)
		return
	}
	h := v.hub
	o := c.opts
	h.mu.Lock()
	if h.closed {
		h.mu.Unlock()
		return
	}
	if o.FPS > 0 {
		v.fps = min(o.FPS, viewerFPSMax)
	}
	if o.MaxWidth > 0 {
		v.maxWidth = o.MaxWidth
	}
	if o.Quality > 0 {
		v.quality = o.Quality
		v.pref = prefFromQuality(o.Quality)
	}
	if p, ok := prefFromName(c.layer); ok {
		v.pref = p
	}
	if v.pref == prefAuto {
		v.auto = newAutoLayer(v.layer) // gecerli katmandan basla
	}
	v.setLayerLocked(v.effectiveLayerLocked())
	display := -1
	if o.Display >= 0 && o.Display != h.opts.Display {
		h.opts.Display = o.Display
		display = o.Display
	}
	v.wakeUp()
	h.mu.Unlock()

	h.syncLayers()
	v.sendState()
	if display >= 0 {
		msg, _ := json.Marshal(map[string]any{"type": "settings_changed", "display": display})
		h.mu.Lock()
		for w := range h.viewers {
			w.sendMsg(msg)
		}
		h.mu.Unlock()
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = h.agent.Screen2Settings(ctx, h.sid, tunnel.Screen2Options{Display: display})
	}
}

// syncLayers, izleyicilerin ihtiyacina gore ajanin etkin katman kumesini ve
// yakalama fps tavanini gunceller. Sirali ve en son durumu yansitir. Abonesiz
// katmanin deposu hemen bosaltilir.
func (h *screen2Hub) syncLayers() {
	if !h.layered {
		return
	}
	go func() {
		h.layerMu.Lock()
		defer h.layerMu.Unlock()
		h.mu.Lock()
		if h.closed {
			h.mu.Unlock()
			return
		}
		in := make([]specIn, 0, len(h.viewers))
		fps := 0
		for v := range h.viewers {
			in = append(in, specIn{v.layer, v.maxWidth, v.quality})
			fps = max(fps, resolveFPS(v.fps))
		}
		specs, active := computeSpecs(in)
		key := false
		for l := 0; l < numLayers; l++ {
			if active[l] && !h.wasActive[l] {
				key = true
			}
			if !active[l] {
				h.stores[l].clear()
			}
			h.wasActive[l] = active[l]
		}
		fps = min(fps, agentFPSMax)
		sendSpecs := key || !specsEqual(specs, h.sentSpecs)
		sendFPS := fps > 0 && fps != h.sentFPS
		if sendSpecs {
			h.sentSpecs = specs
		}
		if sendFPS {
			h.sentFPS = fps
		}
		h.mu.Unlock()
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if sendSpecs {
			_ = h.lagent.Screen2Layers(ctx, h.sid, specs, key)
		}
		if sendFPS {
			_ = h.agent.Screen2Settings(ctx, h.sid, tunnel.Screen2Options{FPS: fps, Display: -1})
		}
	}()
}
