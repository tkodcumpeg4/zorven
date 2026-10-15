package api

import (
	"bytes"
	"context"
	"sync"
	"testing"
	"time"

	"github.com/tkodcumpeg4/zorven/server/tunnel"
	"github.com/tkodcumpeg4/zorven/shared/protocol"
)

// --- yardimcilar ------------------------------------------------------------

// layerCall, Screen2Layers cagrisi kaydi.
type layerCall struct {
	specs []protocol.Screen2LayerSpec
	key   bool
}

// fakeLayerAgent, fakeAgent + katman destegi.
type fakeLayerAgent struct {
	*fakeAgent
	lmu    sync.Mutex
	opened tunnel.Screen2Options
	calls  []layerCall
}

func (f *fakeLayerAgent) SupportsScreen2Layers() bool { return true }
func (f *fakeLayerAgent) Screen2Layers(_ context.Context, _ string, l []protocol.Screen2LayerSpec, key bool) error {
	f.lmu.Lock()
	f.calls = append(f.calls, layerCall{append([]protocol.Screen2LayerSpec(nil), l...), key})
	f.lmu.Unlock()
	return nil
}
func (f *fakeLayerAgent) OpenScreen2(ctx context.Context, sid string, o tunnel.Screen2Options,
	frames chan<- []byte, errs chan<- protocol.ScreenError) (uint64, error) {
	f.lmu.Lock()
	f.opened = o
	f.lmu.Unlock()
	return f.fakeAgent.OpenScreen2(ctx, sid, o, frames, errs)
}
func (f *fakeLayerAgent) lastCall() (layerCall, int) {
	f.lmu.Lock()
	defer f.lmu.Unlock()
	if len(f.calls) == 0 {
		return layerCall{}, 0
	}
	return f.calls[len(f.calls)-1], len(f.calls)
}

func newLayerAgent() *fakeLayerAgent { return &fakeLayerAgent{fakeAgent: &fakeAgent{}} }

// tilePayload, belirli bir karo icin ayirt edilebilir sahte JPEG yuku.
func tilePayload(tag byte) []byte { return []byte{0xff, 0xd8, tag, 0xff, 0xd9} }

// tileMsgs, katman l icin w x h tuvalinde belirtilen karolari (indeks -> etiket)
// karo basina bir dikdortgen olarak paketler.
func tileMsgs(t *testing.T, l uint8, w, h int, key bool, tiles map[int]byte) [][]byte {
	t.Helper()
	tx := (w + s2Tile - 1) / s2Tile
	var rects []protocol.Screen2Rect
	for i := 0; i < tx*((h+s2Tile-1)/s2Tile); i++ {
		tag, ok := tiles[i]
		if !ok {
			continue
		}
		x, y := (i%tx)*s2Tile, (i/tx)*s2Tile
		rects = append(rects, protocol.Screen2Rect{
			X: uint16(x), Y: uint16(y), W: uint16(min(s2Tile, w-x)), H: uint16(min(s2Tile, h-y)),
			Enc: protocol.Screen2EncJPEG, Payload: tilePayload(tag),
		})
	}
	msgs, err := protocol.PackScreen2Layer(1, 0, key, l, uint16(w), uint16(h), rects, 0)
	if err != nil {
		t.Fatal(err)
	}
	return msgs
}

func allTiles(n int, tag byte) map[int]byte {
	m := map[int]byte{}
	for i := 0; i < n; i++ {
		m[i] = tag
	}
	return m
}

// manuel hub: gonderici dongusu OLMADAN deterministik testler icin.
func manualHub() *screen2Hub {
	return &screen2Hub{viewers: map[*screen2Viewer]struct{}{}, layered: true, done: make(chan struct{})}
}

func (h *screen2Hub) addManual(fps int) *screen2Viewer {
	v := &screen2Viewer{hub: h, out: make(chan []byte, 32), fps: fps,
		wake: make(chan struct{}, 1), quit: make(chan struct{}), pref: layerHigh, auto: newAutoLayer(layerHigh)}
	h.mu.Lock()
	h.viewers[v] = struct{}{}
	v.resyncLocked()
	h.mu.Unlock()
	return v
}

func (h *screen2Hub) feed(msgs [][]byte) {
	h.mu.Lock()
	for _, m := range msgs {
		h.ingestLocked(m)
	}
	h.mu.Unlock()
}

func (v *screen2Viewer) take(now time.Time) (tileBatch, time.Duration, bool) {
	v.hub.mu.Lock()
	defer v.hub.mu.Unlock()
	return v.takeBatchLocked(now, 1<<20)
}

// --- karo deposu / kirli kume ----------------------------------------------

func TestTileStoreCoalescesIntermediateVersions(t *testing.T) {
	h := manualHub()
	v := h.addManual(10)
	t0 := time.Now()
	h.feed(tileMsgs(t, 0, 128, 64, true, allTiles(2, 1))) // 2 karo
	b, _, ok := v.take(t0)
	if !ok || !b.key || len(b.rects) != 2 || b.w != 128 || b.h != 64 {
		t.Fatalf("ilk tur: ok=%v %+v", ok, b)
	}
	// ayni karo uc kez degisir; iki tur arasinda ara surumler birlesir
	h.feed(tileMsgs(t, 0, 128, 64, false, map[int]byte{0: 2}))
	h.feed(tileMsgs(t, 0, 128, 64, false, map[int]byte{0: 3}))
	h.feed(tileMsgs(t, 0, 128, 64, false, map[int]byte{0: 4}))
	// fps araligi (100ms) dolmadan: bekle
	if _, wait, ok := v.take(t0.Add(30 * time.Millisecond)); ok || wait <= 0 {
		t.Fatalf("fps siniri uygulanmadi: ok=%v wait=%v", ok, wait)
	}
	b, _, ok = v.take(t0.Add(120 * time.Millisecond))
	if !ok || b.key || len(b.rects) != 1 || b.rects[0].X != 0 || b.rects[0].Payload[2] != 4 {
		t.Fatalf("birlesme: ok=%v %+v", ok, b)
	}
	if _, _, ok := v.take(t0.Add(time.Second)); ok {
		t.Fatal("kirli kume temizlenmeliydi")
	}
}

func TestDirtySetsArePerViewer(t *testing.T) {
	h := manualHub()
	fast := h.addManual(30)
	slow := h.addManual(1)
	t0 := time.Now()
	h.feed(tileMsgs(t, 0, 128, 64, true, allTiles(2, 1)))
	fast.take(t0)
	slow.take(t0)

	h.feed(tileMsgs(t, 0, 128, 64, false, map[int]byte{1: 9}))
	b, _, ok := fast.take(t0.Add(50 * time.Millisecond))
	if !ok || len(b.rects) != 1 || b.rects[0].X != 64 {
		t.Fatalf("hizli izleyici: %+v", b)
	}
	// yavas izleyici henuz gormedi (1 fps) ama kirli kumesi KORUNUR
	if _, wait, ok := slow.take(t0.Add(50 * time.Millisecond)); ok || wait <= 0 {
		t.Fatal("yavas izleyici erken gonderim aldi")
	}
	h.feed(tileMsgs(t, 0, 128, 64, false, map[int]byte{1: 10}))
	if _, _, ok := fast.take(t0.Add(100 * time.Millisecond)); !ok {
		t.Fatal("hizli izleyici bir sonraki guncellemeyi hemen almali")
	}
	b, _, ok = slow.take(t0.Add(1100 * time.Millisecond))
	if !ok || len(b.rects) != 1 || b.rects[0].Payload[2] != 10 {
		t.Fatalf("yavas izleyici en guncel hali almali: %+v", b)
	}
}

func TestNewViewerGetsFullImageFromStore(t *testing.T) {
	h := manualHub()
	a := h.addManual(30)
	h.feed(tileMsgs(t, 0, 320, 192, true, allTiles(15, 5)))
	a.take(time.Now())
	h.feed(tileMsgs(t, 0, 320, 192, false, map[int]byte{3: 6}))

	late := h.addManual(30) // ajandan anahtar kare istenmeden
	b, _, ok := late.take(time.Now())
	if !ok || !b.key || len(b.rects) != 15 {
		t.Fatalf("yeni izleyici tam goruntu almali: ok=%v rects=%d", ok, len(b.rects))
	}
	for _, r := range b.rects {
		want := byte(5)
		if r.X == 192 && r.Y == 0 { // indeks 3
			want = 6
		}
		if r.Payload[2] != want {
			t.Fatalf("karo (%d,%d) eski surum", r.X, r.Y)
		}
	}
	// kenar karosu kirpilmis: 320 genislik => son sutun 64 (5*64), 192 yukseklik => 3 satir
	if last := b.rects[14]; last.X != 256 || last.Y != 128 || last.W != 64 || last.H != 64 {
		t.Fatalf("son karo: %+v", last)
	}
}

func TestStoreIgnoresMisalignedAndUnsubscribedLayer(t *testing.T) {
	h := manualHub()
	v := h.addManual(30)
	h.feed(tileMsgs(t, 0, 128, 64, true, map[int]byte{0: 1}))
	// hizasiz dikdortgen elenir
	bad, _ := protocol.PackScreen2Layer(1, 1, false, 0, 128, 64,
		[]protocol.Screen2Rect{{X: 10, Y: 0, W: 64, H: 64, Enc: protocol.Screen2EncJPEG, Payload: tilePayload(7)}}, 0)
	h.feed(bad)
	// abonesi olmayan katman (1) depoya girmez
	h.feed(tileMsgs(t, 1, 64, 64, true, map[int]byte{0: 1}))
	h.mu.Lock()
	if h.stores[1].w != 0 {
		t.Error("abonesiz katman depolandi")
	}
	if h.stores[0].tiles[1].payload != nil {
		t.Error("hizasiz dikdortgen depolandi")
	}
	h.mu.Unlock()
	b, _, _ := v.take(time.Now())
	if len(b.rects) != 1 {
		t.Fatalf("rects=%d", len(b.rects))
	}
}

func TestKeyframeSameDimsKeepsStore(t *testing.T) {
	h := manualHub()
	v := h.addManual(30)
	h.feed(tileMsgs(t, 0, 128, 64, true, allTiles(2, 1)))
	v.take(time.Now())
	// kayip sonrasi ajan tam kare yollar: ayni boyut => depo bosaltilmaz
	h.feed(tileMsgs(t, 0, 128, 64, true, map[int]byte{0: 2}))
	h.mu.Lock()
	kept := h.stores[0].tiles[1].payload != nil
	h.mu.Unlock()
	if !kept {
		t.Fatal("ayni boyutlu anahtar kare depoyu bosaltti")
	}
	// boyut degisince tuval sifirlanir ve izleyici yeniden kurulur (keyframe bayrakli)
	h.feed(tileMsgs(t, 0, 64, 64, true, map[int]byte{0: 3}))
	b, _, ok := v.take(time.Now().Add(time.Second))
	if !ok || !b.key || b.w != 64 || len(b.rects) != 1 {
		t.Fatalf("yeniden boyutlama: ok=%v %+v", ok, b)
	}
}

// --- otomatik katman --------------------------------------------------------

func drive(a *autoLayer, start time.Time, from, to time.Duration, st int) (time.Time, []int) {
	var changes []int
	now := start
	for el := from; el < to; el += 250 * time.Millisecond {
		now = start.Add(el)
		if l, c := a.Observe(now, st); c {
			changes = append(changes, l)
		}
	}
	return now, changes
}

func TestAutoLayerHysteresis(t *testing.T) {
	a := newAutoLayer(layerHigh)
	t0 := time.Now()
	a.Observe(t0, autoOK)
	// kisa sureli tikanma (tek pencere) gecis YAPMAZ
	_, ch := drive(&a, t0, 0, 2250*time.Millisecond, autoBad)
	if len(ch) != 0 {
		t.Fatalf("erken dusus: %v", ch)
	}
	// saglikli donus sayaci sifirlar
	drive(&a, t0, 2250*time.Millisecond, 7*time.Second, autoOK)
	_, ch = drive(&a, t0, 7*time.Second, 10*time.Second, autoBad)
	if len(ch) != 0 {
		t.Fatalf("sayac sifirlanmaliydi: %v", ch)
	}
	// surekli tikanma => dusuk
	_, ch = drive(&a, t0, 10*time.Second, 20*time.Second, autoBad)
	if len(ch) != 1 || ch[0] != layerLow {
		t.Fatalf("dusuk katmana gecilmeliydi: %v", ch)
	}
	// kisa iyi sure yukseltmez
	_, ch = drive(&a, t0, 20*time.Second, 28*time.Second, autoOK)
	if len(ch) != 0 {
		t.Fatalf("erken yukselis: %v", ch)
	}
	// uzun iyi sure => yuksek
	_, ch = drive(&a, t0, 28*time.Second, 50*time.Second, autoOK)
	if len(ch) != 1 || ch[0] != layerHigh {
		t.Fatalf("yuksek katmana donulmeliydi: %v", ch)
	}
	// hemen tekrar bozulursa bir sonraki yukselis daha uzun surer (salinim korumasi)
	_, ch = drive(&a, t0, 50*time.Second, 66*time.Second, autoBad)
	if len(ch) != 1 || ch[0] != layerLow {
		t.Fatalf("yeniden dusus: %v", ch)
	}
	if a.upNeed <= autoUpWinsMin {
		t.Fatalf("upNeed artmadi: %d", a.upNeed)
	}
}

func TestAutoLayerIdleIsNeutral(t *testing.T) {
	a := newAutoLayer(layerHigh)
	t0 := time.Now()
	a.Observe(t0, autoIdle)
	_, ch := drive(&a, t0, 0, 60*time.Second, autoIdle)
	if len(ch) != 0 || a.layer != layerHigh {
		t.Fatalf("bosta katman degismemeli: %v", ch)
	}
}

// --- katman kumesi hesabi ---------------------------------------------------

func TestComputeSpecs(t *testing.T) {
	specs, act := computeSpecs([]specIn{{layerHigh, 1280, 90}, {layerHigh, 1920, 0}, {layerLow, 1920, 40}})
	if !act[0] || !act[1] || len(specs) != 2 {
		t.Fatalf("%+v", specs)
	}
	if specs[0].MaxWidth != 1920 || specs[0].Quality != layerQualityHigh {
		t.Errorf("yuksek: %+v", specs[0])
	}
	if specs[1].MaxWidth != 960 || specs[1].Quality != layerQualityLow {
		t.Errorf("dusuk yariya inmeli: %+v", specs[1])
	}
	specs, act = computeSpecs([]specIn{{layerLow, 0, 40}})
	if act[0] || len(specs) != 1 || specs[0].ID != layerLow || specs[0].MaxWidth != screen2DefaultWidth/2 {
		t.Fatalf("yalniz dusuk: %+v", specs)
	}
	specs, _ = computeSpecs([]specIn{{layerHigh, 0, 0}})
	if specs[0].Quality != layerQualityMid {
		t.Errorf("acik yuksek yoksa orta kalite: %+v", specs[0])
	}
}

// --- tam akis: join / ayar / abonelik --------------------------------------

func readOutMsgs(v *screen2Viewer, n int, d time.Duration) []protocol.Screen2Message {
	var out []protocol.Screen2Message
	deadline := time.After(d)
	for len(out) < n {
		select {
		case b := <-v.out:
			if m, err := protocol.DecodeScreen2(b); err == nil {
				out = append(out, m)
			}
		case <-deadline:
			return out
		}
	}
	return out
}

func waitUntil(t *testing.T, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("zaman asimi")
}

func TestLayeredJoinFullImageAndSubscriptions(t *testing.T) {
	var reg screen2Registry
	fa := newLayerAgent()

	a, err := reg.join("c1", fa, tunnel.Screen2Options{FPS: 30, Quality: 90, MaxWidth: 1280}, false, false, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer a.leave()
	fa.lmu.Lock()
	op := fa.opened
	fa.lmu.Unlock()
	if len(op.Layers) != 1 || op.Layers[0].ID != layerHigh || op.Layers[0].MaxWidth != 1280 {
		t.Fatalf("acilista katmanlar: %+v", op.Layers)
	}

	// ajan yuksek katmani yollar: izleyici A tam goruntuyu depodan alir
	for _, m := range tileMsgs(t, 0, 128, 64, true, allTiles(2, 1)) {
		fa.raw <- m
	}
	got := readOutMsgs(a, 1, 2*time.Second)
	if len(got) != 1 || !got[0].Keyframe || len(got[0].Rects) != 2 || got[0].Layer != 0 {
		t.Fatalf("A ilk mesaj: %+v", got)
	}

	// B dusuk kaliteyle katilir: dusuk katman ABONE olur, ajana key ile bildirilir
	b, err := reg.join("c1", fa, tunnel.Screen2Options{FPS: 30, Quality: 40, MaxWidth: 1280}, false, false, nil)
	if err != nil {
		t.Fatal(err)
	}
	waitUntil(t, func() bool { c, n := fa.lastCall(); return n > 0 && len(c.specs) == 2 && c.key })
	if c, _ := fa.lastCall(); c.specs[1].ID != layerLow || c.specs[1].MaxWidth != 640 {
		t.Fatalf("dusuk katman: %+v", c.specs)
	}
	for _, m := range tileMsgs(t, 1, 64, 64, true, map[int]byte{0: 8}) {
		fa.raw <- m
	}
	gb := readOutMsgs(b, 1, 2*time.Second)
	if len(gb) != 1 || gb[0].Layer != 1 || !gb[0].Keyframe || gb[0].W != 64 {
		t.Fatalf("B dusuk katman goruntusu: %+v", gb)
	}
	// A hala yuksek katmanda ve B'nin dusuk katman karesi A'ya GITMEZ
	select {
	case extra := <-a.out:
		t.Fatalf("A'ya baska katman sizdi: %v", extra[:16])
	case <-time.After(150 * time.Millisecond):
	}

	// yuksek katmanda yeni karo: yalniz A alir
	for _, m := range tileMsgs(t, 0, 128, 64, false, map[int]byte{1: 2}) {
		fa.raw <- m
	}
	ga := readOutMsgs(a, 1, 2*time.Second)
	if len(ga) != 1 || ga[0].Keyframe || len(ga[0].Rects) != 1 {
		t.Fatalf("A delta: %+v", ga)
	}
	select {
	case <-b.out:
		t.Fatal("B yuksek katman karesi aldi")
	case <-time.After(150 * time.Millisecond):
	}

	// B ayrilir: dusuk katman kumeden CIKAR (ajan artik kodlamaz)
	_, before := fa.lastCall()
	b.leave()
	waitUntil(t, func() bool { c, n := fa.lastCall(); return n > before && len(c.specs) == 1 && c.specs[0].ID == layerHigh })
	a.hub.mu.Lock()
	cleared := a.hub.stores[1].w == 0
	a.hub.mu.Unlock()
	if !cleared {
		t.Error("abonesiz katman deposu bosaltilmadi")
	}
	if fa.get(func() int { return fa.keyframes }) != 0 {
		t.Error("katmanli yayinda katilim anahtar kare istememeliydi")
	}
}

func TestLayeredSettingsAffectOnlyOwnViewer(t *testing.T) {
	var reg screen2Registry
	fa := newLayerAgent()
	a, _ := reg.join("c2", fa, tunnel.Screen2Options{FPS: 15}, false, false, nil)
	b, _ := reg.join("c2", fa, tunnel.Screen2Options{FPS: 15}, false, false, nil)
	defer a.leave()
	defer b.leave()
	for _, m := range tileMsgs(t, 0, 64, 64, true, map[int]byte{0: 1}) {
		fa.raw <- m
	}
	readOutMsgs(a, 1, time.Second)
	readOutMsgs(b, 1, time.Second)
	drainMsgs(a)
	drainMsgs(b)

	// A, fps ve katmani degistirir
	a.applySettings(screenControl{kind: "settings", layer: "low", opts: tunnel.Screen2Options{FPS: 5, Quality: 40, Display: -1}})

	a.hub.mu.Lock()
	bf, bl, af, al := b.fps, b.layer, a.fps, a.layer
	a.hub.mu.Unlock()
	if bf != 15 || bl != layerHigh {
		t.Fatalf("B etkilendi: fps=%d layer=%d", bf, bl)
	}
	if af != 5 || al != layerLow {
		t.Fatalf("A ayari uygulanmadi: fps=%d layer=%d", af, al)
	}
	// B'ye settings_changed GITMEZ
	for _, m := range drainMsgs(b) {
		if bytes.Contains(m, []byte("settings_changed")) {
			t.Fatalf("B'ye settings_changed gitti: %s", m)
		}
	}
	// ajana yalniz birlesik fps tavani (B 15) gider; kalite/genislik gonderilmez
	for _, s := range fa.settingsSnapshot() {
		if s.Quality != 0 || s.MaxWidth != 0 {
			t.Fatalf("ajan ortak kalite/genislik aldi: %+v", s)
		}
	}
}

func TestLegacyAgentKeepsLastWriterWins(t *testing.T) {
	var reg screen2Registry
	fa := &fakeAgent{} // katman destegi YOK
	a, _ := reg.join("c3", fa, tunnel.Screen2Options{FPS: 10}, false, false, nil)
	b, _ := reg.join("c3", fa, tunnel.Screen2Options{FPS: 10}, false, false, nil)
	defer a.leave()
	defer b.leave()
	if a.hub.layered {
		t.Fatal("eski ajanda katmanli mod acilmamali")
	}
	drainMsgs(b)
	a.applySettings(screenControl{kind: "settings", opts: tunnel.Screen2Options{FPS: 5, Display: -1}})
	found := false
	for _, m := range drainMsgs(b) {
		if bytes.Contains(m, []byte("settings_changed")) {
			found = true
		}
	}
	if !found {
		t.Fatal("eski ajanda settings_changed yayini surmeli")
	}
}

func drainMsgs(v *screen2Viewer) [][]byte {
	time.Sleep(60 * time.Millisecond)
	var out [][]byte
	for {
		select {
		case m := <-v.msgs:
			out = append(out, m)
		default:
			return out
		}
	}
}

func (f *fakeAgent) settingsSnapshot() []tunnel.Screen2Options {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]tunnel.Screen2Options(nil), f.settings...)
}
