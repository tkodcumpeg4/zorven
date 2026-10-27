package api

import (
	"context"
	"sort"
	"time"

	"github.com/tkodcumpeg4/zorven/shared/protocol"
)

// Uctan uca geri bildirim (FeatureScreen2Feedback).
//
// Sunucu izleyici basina kare atlar/birlestirir (sinirli kuyruk), ama ajan
// bunu yerel yazma suresinden goremez (TCP tamponu). Hub, ~500 ms'lik
// pencerelerde izleyici basina sayar: ajandan gelen kare (arrived), izleyici
// kuyruguna teslim edilen kare (delivered), kuyrugun doluluk/tikanma durumu ve
// bekleyen verinin yasi. Katman basina EN YAVAS izleyicinin ozeti ajana gider;
// ajan fps/kaliteyi buna gore ayarlar. Degisim yoksa gonderilmez (seyrek
// canlilik mesaji haric).

const (
	fbEvery     = 500 * time.Millisecond
	fbKeepalive = 2 * time.Second
	// fbSlowQueue: bekleyen veri bundan eskiyse izleyici tikali sayilir.
	fbSlowQueue = 300 * time.Millisecond
	// fbMinGap: ardisik iki gonderim arasi en az sure (hiz siniri).
	fbMinGap = 400 * time.Millisecond
)

// fbSample, bir izleyicinin penceredeki ham olcumu.
type fbSample struct {
	layer     int
	arrived   int // ajandan gelen kare
	delivered int // izleyici kuyruguna giren kare
	blocked   bool
	queue     time.Duration
	elapsed   time.Duration
}

// summarizeFeedback, katman basina en yavas izleyiciyi secer. Veri beklemeyen
// (arrived==0) izleyici yavas sayilmaz. Kuyruk yuzunden kisitli izleyici
// (blocked ya da kuyruk yasi buyuk) kisitsizlardan once yavas sayilir; kendi
// fps tercihi yuzunden az alan izleyici KISITLI degildir (kisit yok: ajanin
// geldigi hiz raporlanir).
func summarizeFeedback(ss []fbSample) []protocol.Screen2FeedbackLayer {
	type cand struct {
		fps         float64
		skipped     int
		queue       time.Duration
		constrained bool
	}
	best := map[int]cand{}
	count := map[int]int{}
	for _, s := range ss {
		if s.arrived <= 0 || s.elapsed <= 0 {
			continue
		}
		sec := s.elapsed.Seconds()
		c := cand{queue: s.queue}
		del, arr := float64(s.delivered)/sec, float64(s.arrived)/sec
		if s.blocked || s.queue >= fbSlowQueue {
			c.constrained = true
			c.fps = del
			c.skipped = max(s.arrived-s.delivered, 0)
		} else {
			c.fps = max(del, arr)
		}
		count[s.layer]++
		b, ok := best[s.layer]
		switch {
		case !ok,
			c.constrained && !b.constrained,
			c.constrained == b.constrained && c.fps < b.fps,
			c.constrained == b.constrained && c.fps == b.fps && c.queue > b.queue:
			best[s.layer] = c
		}
	}
	out := make([]protocol.Screen2FeedbackLayer, 0, len(best))
	for id, c := range best {
		out = append(out, protocol.Screen2FeedbackLayer{
			ID: id, FPS: float64(int(c.fps*10+0.5)) / 10, Skipped: min(c.skipped, protocol.Screen2FeedbackMaxSkip),
			QueueMs: min(int(c.queue.Milliseconds()), protocol.Screen2FeedbackMaxQueue),
			Viewers: min(count[id], protocol.Screen2FeedbackMaxViewer),
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

// fbChanged, iki ozetin ajan icin anlamli bicimde farkli olup olmadigi
// (fps tam sayiya, kuyruk 100 ms'ye, atlama 4'luk kovaya yuvarlanir).
func fbChanged(a, b []protocol.Screen2FeedbackLayer) bool {
	if len(a) != len(b) {
		return true
	}
	for i := range a {
		x, y := a[i], b[i]
		if x.ID != y.ID || int(x.FPS+0.5) != int(y.FPS+0.5) || x.QueueMs/100 != y.QueueMs/100 ||
			x.Skipped/4 != y.Skipped/4 || (x.Skipped == 0) != (y.Skipped == 0) || x.Viewers != y.Viewers {
			return true
		}
	}
	return false
}

// viewerFB, izleyicinin son pencere ozeti (panele "srv" olarak iletilir).
type viewerFB struct {
	valid   bool
	fps     float64
	skipped int
	queueMs int
}

// fbWin, bir pencerenin ham sayaclari (yumusatma icin onceki pencere tutulur).
type fbWin struct {
	arrived, delivered int
	blocked            bool
	elapsed            time.Duration
}

// fbSendResult, tick'in sonucu.
type fbSendResult struct {
	send     bool
	layers   []protocol.Screen2FeedbackLayer
	windowMs int
}

// feedbackTick, pencereyi kapatir: izleyici sayaclarini ozetler, sifirlar ve
// ajana mesaj gerekip gerekmedigini soyler (h.mu icinde cagrilmaz; kendisi kilitler).
func (h *screen2Hub) feedbackTick(now time.Time) fbSendResult {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.closed {
		return fbSendResult{}
	}
	if h.fbTickAt.IsZero() {
		h.fbTickAt = now
		return fbSendResult{}
	}
	el := now.Sub(h.fbTickAt)
	h.fbTickAt = now
	if el <= 0 {
		return fbSendResult{}
	}
	samples := make([]fbSample, 0, len(h.viewers))
	for v := range h.viewers {
		layer := -1
		if h.layered {
			layer = v.layer
		}
		cur := fbWin{arrived: v.fbArrived, delivered: v.fbDelivered, blocked: v.fbBlocked > 0 || len(v.out) >= cap(v.out)/2, elapsed: el}
		q := v.fbQueueMax
		// Hala bekleyen veri: yasi simdiye kadar buyudu (sync turlari tikanma sayilmaz).
		if h.layered && v.dirtyN > 0 && !v.dirtySince.IsZero() && !v.needSync && !v.syncing {
			q = max(q, now.Sub(v.dirtySince))
		}
		if !h.layered && v.resync && !v.resyncSince.IsZero() {
			q = max(q, now.Sub(v.resyncSince))
		}
		// Iki pencerenin toplami: 500 ms'de 5 kare gibi kucuk sayilarda titremeyi azaltir.
		sum := fbWin{
			arrived: cur.arrived + v.fbPrev.arrived, delivered: cur.delivered + v.fbPrev.delivered,
			blocked: cur.blocked || v.fbPrev.blocked, elapsed: cur.elapsed + v.fbPrev.elapsed,
		}
		s := fbSample{layer: layer, arrived: sum.arrived, delivered: sum.delivered, blocked: sum.blocked, queue: q, elapsed: sum.elapsed}
		samples = append(samples, s)
		v.fbPrev = cur
		v.fbArrived, v.fbDelivered, v.fbBlocked, v.fbQueueMax = 0, 0, 0, 0
		one := summarizeFeedback([]fbSample{s})
		if len(one) == 1 {
			v.fbLast = viewerFB{valid: true, fps: one[0].FPS, skipped: one[0].Skipped, queueMs: one[0].QueueMs}
		} else {
			v.fbLast = viewerFB{}
		}
	}
	layers := summarizeFeedback(samples)
	if !h.fbOK {
		return fbSendResult{}
	}
	since := now.Sub(h.fbSentAt)
	if h.fbSentAt.IsZero() {
		since = fbEvery
	}
	changed := fbChanged(layers, h.fbSent)
	alive := len(layers) > 0 && since >= fbKeepalive
	if since < fbMinGap || (!changed && !alive) {
		return fbSendResult{}
	}
	h.fbSent, h.fbSentAt = layers, now
	return fbSendResult{send: true, layers: layers, windowMs: int(min(since, time.Minute).Milliseconds())}
}

// feedbackLoop, hub omru boyunca pencereleri kapatir ve ozeti ajana yollar.
func (h *screen2Hub) feedbackLoop() {
	t := time.NewTicker(fbEvery)
	defer t.Stop()
	for {
		select {
		case <-h.ctx.Done():
			return
		case now := <-t.C:
			r := h.feedbackTick(now)
			if !r.send {
				continue
			}
			ctx, cancel := context.WithTimeout(h.ctx, 2*time.Second)
			_ = h.fbAgent.Screen2Feedback(ctx, h.sid, r.windowMs, r.layers)
			cancel()
		}
	}
}

// nowT, hub saati (testler clock ile sahte saat verir).
func (h *screen2Hub) nowT() time.Time {
	if h.clock != nil {
		return h.clock()
	}
	return time.Now()
}

// feedbackAgent, geri bildirimi destekleyen istemci oturumu (*tunnel.Session);
// screen2Agent'a EKLENMEZ (eski sahte uygulamalar bozulmasin).
type feedbackAgent interface {
	SupportsScreen2Feedback() bool
	Screen2Feedback(ctx context.Context, sessionID string, windowMs int, layers []protocol.Screen2FeedbackLayer) error
}

// srvJSON, izleyicinin panele giden "srv" bolumu (kilit disinda kullanilmaz).
func (v *screen2Viewer) srvLocked(fbOK bool) map[string]any {
	if !v.fbLast.valid {
		return nil
	}
	return map[string]any{
		"fps": v.fbLast.fps, "skipped": v.fbLast.skipped, "queue_ms": v.fbLast.queueMs, "feedback": fbOK,
	}
}
