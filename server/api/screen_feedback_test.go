package api

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/tkodcumpeg4/zorven/server/tunnel"
	"github.com/tkodcumpeg4/zorven/shared/protocol"
)

func ms(n int) time.Duration { return time.Duration(n) * time.Millisecond }

func TestSummarizeFeedbackPicksSlowestPerLayer(t *testing.T) {
	ss := []fbSample{
		// katman 1: hizli izleyici (kisitsiz, 24 fps geliyor) ve yavas (tikali, 10 fps teslim)
		{layer: 1, arrived: 24, delivered: 24, elapsed: time.Second},
		{layer: 1, arrived: 24, delivered: 10, blocked: true, queue: ms(450), elapsed: time.Second},
		// katman 0: tek izleyici kisitsiz
		{layer: 0, arrived: 20, delivered: 20, elapsed: time.Second},
		// veri beklemeyen izleyici yavas sayilmaz
		{layer: 1, arrived: 0, delivered: 0, blocked: true, elapsed: time.Second},
		// gecersiz pencere yok sayilir
		{layer: 0, arrived: 5, delivered: 0, blocked: true, elapsed: 0},
	}
	got := summarizeFeedback(ss)
	if len(got) != 2 {
		t.Fatalf("katman sayisi: %+v", got)
	}
	l0, l1 := got[0], got[1]
	if l0.ID != 0 || l0.FPS != 20 || l0.Skipped != 0 || l0.Viewers != 1 {
		t.Fatalf("katman0 = %+v", l0)
	}
	if l1.ID != 1 || l1.FPS != 10 || l1.Skipped != 14 || l1.QueueMs != 450 || l1.Viewers != 2 {
		t.Fatalf("katman1 en yavas izleyici = %+v", l1)
	}
	if !(protocol.Screen2Feedback{Layers: got}).Valid() {
		t.Fatal("uretilen ozet gecerli olmali")
	}
}

func TestSummarizeFeedbackViewerFPSPreferenceIsNotSlow(t *testing.T) {
	// Izleyici kendi tercihi yuzunden 5 fps aliyor (kuyruk bos, tikanma yok): kisit
	// yok, ajanin geldigi hiz raporlanir, atlama sayilmaz.
	got := summarizeFeedback([]fbSample{
		{layer: 0, arrived: 24, delivered: 5, elapsed: time.Second},
		{layer: 0, arrived: 24, delivered: 24, elapsed: time.Second},
	})
	if len(got) != 1 || got[0].FPS != 24 || got[0].Skipped != 0 {
		t.Fatalf("%+v", got)
	}
	// Ayni katmanda biri gercekten tikaliysa o secilir.
	got = summarizeFeedback([]fbSample{
		{layer: 0, arrived: 24, delivered: 24, elapsed: time.Second},
		{layer: 0, arrived: 24, delivered: 3, blocked: true, elapsed: time.Second},
	})
	if got[0].FPS != 3 || got[0].Skipped != 21 {
		t.Fatalf("%+v", got)
	}
	// Veri bekleyen yok => bos ozet.
	if got := summarizeFeedback([]fbSample{{layer: 0, elapsed: time.Second}}); len(got) != 0 {
		t.Fatalf("%+v", got)
	}
}

func TestFBChangedQuantization(t *testing.T) {
	a := []protocol.Screen2FeedbackLayer{{ID: 1, FPS: 10.2, Skipped: 5, QueueMs: 310, Viewers: 1}}
	same := []protocol.Screen2FeedbackLayer{{ID: 1, FPS: 9.8, Skipped: 6, QueueMs: 399, Viewers: 1}}
	if fbChanged(a, same) {
		t.Fatal("kucuk oynama degisim sayilmamali")
	}
	for i, b := range [][]protocol.Screen2FeedbackLayer{
		nil,
		{{ID: 1, FPS: 12, Skipped: 5, QueueMs: 310, Viewers: 1}},
		{{ID: 1, FPS: 10, Skipped: 0, QueueMs: 310, Viewers: 1}},
		{{ID: 1, FPS: 10, Skipped: 5, QueueMs: 700, Viewers: 1}},
		{{ID: 0, FPS: 10, Skipped: 5, QueueMs: 310, Viewers: 1}},
	} {
		if !fbChanged(a, b) {
			t.Errorf("degisim %d algilanmadi", i)
		}
	}
}

// fbHub, geri bildirim testleri icin manuel (gonderici dongusuz) katmanli hub.
func fbHub() *screen2Hub {
	h := manualHub()
	h.fbOK = true
	return h
}

// fbClock, hub saatini sahte saate baglar; set ile ilerletilir.
type fbClock struct{ t time.Time }

func (c *fbClock) set(t time.Time) { c.t = t }

func clockHub(h *screen2Hub, start time.Time) *fbClock {
	c := &fbClock{t: start}
	h.clock = func() time.Time { return c.t }
	return c
}

func TestFeedbackTickSlowViewerAndNoSendWithoutChange(t *testing.T) {
	h := fbHub()
	base := time.Now()
	clk := clockHub(h, base)
	fast := h.addManual(30)
	slow := h.addManual(30)
	if r := h.feedbackTick(base); r.send {
		t.Fatal("ilk tick yalniz saati baslatir")
	}
	// 10 kare gelir; hizli izleyici hepsini alir, yavas hicbirini (kuyruk tikali).
	h.feed(tileMsgs(t, 0, 128, 64, true, allTiles(2, 1)))
	fast.take(base)
	slow.take(base) // ilk tam goruntu teslim edilir; sonra izleyici tikanir
	for i := 0; i < 10; i++ {
		clk.set(base.Add(ms(40 * i)))
		h.feed(tileMsgs(t, 0, 128, 64, false, map[int]byte{0: byte(i)}))
		fast.take(base.Add(ms(40 * i)))
	}
	r := h.feedbackTick(base.Add(ms(500)))
	if !r.send || len(r.layers) != 1 {
		t.Fatalf("gonderim bekleniyordu: %+v", r)
	}
	l := r.layers[0]
	if l.ID != 0 || l.Viewers != 2 || l.Skipped < 10 || l.QueueMs < 300 {
		t.Fatalf("en yavas izleyici (yavas) secilmeliydi: %+v", l)
	}
	if l.FPS > 4 { // ilk senkron kare teslim sayilir (<= ~1/0.5s), digerleri atlandi
		t.Fatalf("teslim fps cok yuksek: %+v", l)
	}
	if r.windowMs != 500 {
		t.Fatalf("pencere=%d", r.windowMs)
	}
	// srv bolumu izleyiciye ozgu: hizli izleyicinin ozeti kisit gostermez.
	h.mu.Lock()
	fl, sl := fast.fbLast, slow.fbLast
	h.mu.Unlock()
	if !fl.valid || fl.skipped != 0 || !sl.valid || sl.skipped < 10 {
		t.Fatalf("izleyici ozetleri: fast=%+v slow=%+v", fl, sl)
	}
	_ = slow
}

func TestFeedbackTickDoesNotResendUnchangedExceptKeepalive(t *testing.T) {
	h := fbHub()
	base := time.Now()
	clk := clockHub(h, base)
	v := h.addManual(30)
	h.feedbackTick(base)
	h.feed(tileMsgs(t, 0, 128, 64, true, allTiles(2, 1)))
	v.take(base)
	cur := base
	step := func() fbSendResult {
		for i := 0; i < 5; i++ { // her pencerede 10 fps, hepsi teslim
			clk.set(cur.Add(ms(100 * i)))
			h.feed(tileMsgs(t, 0, 128, 64, false, map[int]byte{0: byte(i)}))
			v.take(cur.Add(ms(100 * i)))
		}
		cur = cur.Add(ms(500))
		return h.feedbackTick(cur)
	}
	// Isinma: ilk pencerelerde yumusatma yuzunden fps 12 -> 11 -> 10 oturur.
	warm := false
	for i := 0; i < 3; i++ {
		warm = step().send || warm
	}
	if !warm {
		t.Fatal("ilk ozet gonderilmeli")
	}
	// Kararli durumda: degisim yok => yalniz >= 2 sn arayla canlilik mesaji.
	var sends []time.Time
	for i := 0; i < 10; i++ {
		if step().send {
			sends = append(sends, cur)
		}
	}
	if len(sends) == 0 || len(sends) > 3 {
		t.Fatalf("canlilik mesaji sayisi: %d", len(sends))
	}
	for i := 1; i < len(sends); i++ {
		if sends[i].Sub(sends[i-1]) < fbKeepalive {
			t.Fatalf("canlilik mesajlari cok sik: %v", sends[i].Sub(sends[i-1]))
		}
	}
	// Talep kesilince (yumusatma penceresi bitince) bir kez bos ozet gider, sonra susar.
	var tail []fbSendResult
	for i := 0; i < 8; i++ {
		cur = cur.Add(ms(500))
		if r := h.feedbackTick(cur); r.send {
			tail = append(tail, r)
		}
	}
	if len(tail) == 0 || len(tail[len(tail)-1].layers) != 0 {
		t.Fatalf("son gonderim bos ozet olmali: %+v", tail)
	}
	empties := 0
	for _, r := range tail {
		if len(r.layers) == 0 {
			empties++
		}
	}
	if empties != 1 {
		t.Fatalf("bos ozet %d kez gitti", empties)
	}
}

func TestFeedbackTickRateLimit(t *testing.T) {
	h := fbHub()
	base := time.Now()
	clockHub(h, base)
	v := h.addManual(30)
	h.feedbackTick(base)
	h.feed(tileMsgs(t, 0, 128, 64, true, allTiles(2, 1)))
	v.take(base)
	h.feed(tileMsgs(t, 0, 128, 64, false, map[int]byte{0: 1}))
	if r := h.feedbackTick(base.Add(ms(500))); !r.send {
		t.Fatal("ilk gonderim")
	}
	// Anlamli degisim olsa bile 400 ms dolmadan ikinci gonderim olmaz.
	for i := 0; i < 8; i++ {
		h.feed(tileMsgs(t, 0, 128, 64, false, map[int]byte{0: byte(i)}))
	}
	if r := h.feedbackTick(base.Add(ms(500 + 100))); r.send {
		t.Fatal("hiz siniri asildi")
	}
}

func TestFeedbackDisabledNeverSendsButKeepsSrv(t *testing.T) {
	h := manualHub() // fbOK false
	v := h.addManual(30)
	base := time.Now()
	h.feedbackTick(base)
	h.feed(tileMsgs(t, 0, 128, 64, true, allTiles(2, 1)))
	v.take(base)
	h.feed(tileMsgs(t, 0, 128, 64, false, map[int]byte{0: 1}))
	if r := h.feedbackTick(base.Add(ms(500))); r.send {
		t.Fatal("ajan desteklemiyorsa gonderilmemeli")
	}
	h.mu.Lock()
	ok := v.fbLast.valid
	h.mu.Unlock()
	if !ok {
		t.Fatal("sunucu ozeti yine de uretilmeli (panel icin)")
	}
}

// Eski (katmansiz) yayin: pump sayaclari; dolu kuyruk atlama ve tikanma sayilir.
func TestLegacyPumpCountsDeliveredAndDropped(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	h := &screen2Hub{
		viewers: map[*screen2Viewer]struct{}{}, done: make(chan struct{}),
		raw: make(chan []byte, 128), errs: make(chan protocol.ScreenError, 1),
		inp: make(chan protocol.ScreenInputStatus, 1), aux: make(chan tunnel.ScreenAux, 1),
		ctx: ctx, fbOK: true, agent: &fakeAgent{},
	}
	v := &screen2Viewer{hub: h, out: make(chan []byte, 8), msgs: make(chan []byte, 4), cur: make(chan []byte, 1),
		wake: make(chan struct{}, 1), quit: make(chan struct{})}
	v.resync = true
	h.viewers[v] = struct{}{}
	go h.pump()

	frame := func(seq uint32, key bool) []byte {
		msgs, err := protocol.PackScreen2(1, seq, key, 64, 64, []protocol.Screen2Rect{
			{X: 0, Y: 0, W: 64, H: 64, Enc: protocol.Screen2EncSolid, Payload: []byte{1, 2, 3, 255}},
		}, 0)
		if err != nil {
			t.Fatal(err)
		}
		return msgs[0]
	}
	h.raw <- frame(0, true)
	for i := 1; i < 20; i++ {
		h.raw <- frame(uint32(i), false)
	}
	waitUntil(t, func() bool {
		h.mu.Lock()
		defer h.mu.Unlock()
		return v.fbArrived == 20
	})
	h.mu.Lock()
	arrived, delivered, blocked := v.fbArrived, v.fbDelivered, v.fbBlocked
	resync := v.resync
	h.mu.Unlock()
	if delivered != cap(v.out) || blocked == 0 || !resync {
		t.Fatalf("arrived=%d delivered=%d blocked=%d resync=%v", arrived, delivered, blocked, resync)
	}
	base := time.Now()
	h.feedbackTick(base)
	h.mu.Lock()
	v.fbArrived, v.fbDelivered = 20, 8 // pencere sayaclari: 20 geldi, 8 teslim
	v.fbBlocked = 1
	h.mu.Unlock()
	h.feedbackTick(base.Add(ms(500)))
	h.mu.Lock()
	last := v.fbLast
	h.mu.Unlock()
	if !last.valid || last.skipped != 12 || last.fps > 17 {
		t.Fatalf("legacy ozet: %+v", last)
	}
}

// Stats iletiminde izleyiciye ozel "srv" bolumu eklenir.
func TestHubStatsCarrySrvSummary(t *testing.T) {
	var reg screen2Registry
	ag := newAuxAgent()
	v, err := reg.join("c9", ag, tunnel.Screen2Options{}, false, false, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer v.leave()
	v.hub.mu.Lock()
	v.fbLast = viewerFB{valid: true, fps: 9.5, skipped: 7, queueMs: 320}
	v.hub.mu.Unlock()
	ag.auxCh <- tunnel.ScreenAux{Stats: &protocol.Screen2Stats{Type: protocol.TypeScreen2Stats, SessionID: "x", FPS: 24}}
	m := msgOfType(t, v, "stats")
	srv, ok := m["srv"].(map[string]any)
	if !ok || srv["fps"].(float64) != 9.5 || srv["skipped"].(float64) != 7 || srv["queue_ms"].(float64) != 320 || srv["feedback"] != false {
		t.Fatalf("srv=%v", m["srv"])
	}
}

// Geri bildirimi destekleyen ajanda hub ozeti gercekten gonderir.
type fbAgentFake struct {
	*fakeAgent
	fmu   sync.Mutex
	calls [][]protocol.Screen2FeedbackLayer
}

func (f *fbAgentFake) SupportsScreen2Feedback() bool { return true }
func (f *fbAgentFake) Screen2Feedback(_ context.Context, _ string, _ int, l []protocol.Screen2FeedbackLayer) error {
	f.fmu.Lock()
	f.calls = append(f.calls, l)
	f.fmu.Unlock()
	return nil
}

func TestHubDetectsFeedbackAgent(t *testing.T) {
	var reg screen2Registry
	ag := &fbAgentFake{fakeAgent: newAuxAgent()}
	v, err := reg.join("c10", ag, tunnel.Screen2Options{}, false, false, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer v.leave()
	v.hub.mu.Lock()
	ok := v.hub.fbOK
	v.hub.mu.Unlock()
	if !ok {
		t.Fatal("fbOK ayarlanmadi")
	}
	ag.auxCh <- tunnel.ScreenAux{Stats: &protocol.Screen2Stats{Type: protocol.TypeScreen2Stats, FPS: 1}}
	if m := msgOfType(t, v, "stats"); m["layer"].(float64) != -1 {
		t.Fatalf("%v", m)
	}
}
