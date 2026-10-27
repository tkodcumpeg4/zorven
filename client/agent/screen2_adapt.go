package agent

import (
	"sync"
	"time"

	"github.com/tkodcumpeg4/zorven/shared/protocol"
)

// screen2 uyarlama sinirlari.
const (
	screen2MinQuality = 35
	screen2MinFPS     = 3
	// screen2MaxFPS, kullanicinin secebilecegi ve uyarlamanin ulasabilecegi tavan.
	screen2MaxFPS = 30
	// screen2RaiseAfter: art arda bu kadar "rahat" kareden sonra bir basamak yukselir.
	screen2RaiseAfter = 4
	// screen2RaiseFPS: her yukselis basamaginda eklenen FPS.
	screen2RaiseFPS = 3
	// screen2SlowStreak: art arda bu kadar yavas kareden sonra dusulur
	// (tek yavas kare, ornegin anahtar kare, dusurmez).
	screen2SlowStreak = 2
	// screen2SlowWrite: bu kadar surunen tek bir kare yazimi hemen dusurur.
	screen2SlowWrite = 250 * time.Millisecond
)

// adaptCtl, yazma suresine gore JPEG kalitesini ve FPS'i ayarlar.
//
// Gozlenen olcu "kareyi WSS'e yazma suresi" (coder/websocket TCP tamponu
// dolunca yazmada bloklar; bu sure ag basincinin dogrudan olcusudur).
// Kurallar:
//   - yazma > aralik: yavas kare; art arda screen2SlowStreak yavas kare =>
//     tikanma: kalite -10, fps *3/4. Yazma > 250 ms (ya da > 3 aralik) hemen dusurur.
//   - yazma < aralik/4: rahat kare; art arda screen2RaiseAfter rahat kare =>
//     fps +3, kalite +5 (tavana kadar).
//   - aradaki kareler sayaclari sifirlar.
//
// Goroutine-guvenlidir (yakalama ve gonderici asamalari ayri goroutine'lerdir).
type adaptCtl struct {
	mu           sync.Mutex
	maxQ, maxFPS int
	q, fps       int
	good, slow   int
	// fb, sunucu geri bildirimi (FeatureScreen2Feedback) katman basina durumu;
	// anahtar katman kimligi (-1: katmansiz akis).
	fb map[int]*fbState
}

func newAdaptCtl(maxQ, maxFPS int) *adaptCtl {
	a := &adaptCtl{}
	a.SetMax(maxQ, maxFPS)
	a.q, a.fps = a.maxQ, a.maxFPS
	return a
}

// SetMax, tavanlari degistirir (kullanici ayari); mevcut degerler tavana kirpilir.
func (a *adaptCtl) SetMax(maxQ, maxFPS int) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if maxQ < 1 {
		maxQ = 1
	}
	if maxQ > 100 {
		maxQ = 100
	}
	if maxFPS < 1 {
		maxFPS = 1
	}
	a.maxQ, a.maxFPS = maxQ, maxFPS
	if a.q == 0 || a.q > maxQ {
		a.q = maxQ
	}
	if a.fps == 0 || a.fps > maxFPS {
		a.fps = maxFPS
	}
}

// MaxQ ve MaxFPS, kullanici tavanlari.
func (a *adaptCtl) MaxQ() int   { a.mu.Lock(); defer a.mu.Unlock(); return a.maxQ }
func (a *adaptCtl) MaxFPS() int { a.mu.Lock(); defer a.mu.Unlock(); return a.maxFPS }

func (a *adaptCtl) minQ() int {
	if screen2MinQuality > a.maxQ {
		return a.maxQ
	}
	return screen2MinQuality
}

func (a *adaptCtl) minFPS() int {
	if screen2MinFPS > a.maxFPS {
		return a.maxFPS
	}
	return screen2MinFPS
}

// Quality ve FPS, gecerli (uyarlanmis) degerler.
func (a *adaptCtl) Quality() int { a.mu.Lock(); defer a.mu.Unlock(); return a.q }
func (a *adaptCtl) FPS() int     { a.mu.Lock(); defer a.mu.Unlock(); return a.fps }

// Interval, iki kare arasi hedef sure.
func (a *adaptCtl) Interval() time.Duration {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.interval()
}

func (a *adaptCtl) interval() time.Duration { return time.Second / time.Duration(a.fps) }

// Observe, bir karenin yazma suresini bildirir ve degisiklik olduysa true doner.
func (a *adaptCtl) Observe(write time.Duration) bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	interval := a.interval()
	oq, of := a.q, a.fps

	slow := write > interval
	severe := write > screen2SlowWrite || write > 3*interval
	if slow {
		a.good = 0
		a.slow++
	} else {
		a.slow = 0
	}
	switch {
	case slow && (severe || a.slow >= screen2SlowStreak):
		// Tikanma: kaliteyi ve FPS'i hizla dusur.
		a.slow = 0
		a.q -= 10
		if a.q < a.minQ() {
			a.q = a.minQ()
		}
		a.fps = a.fps * 3 / 4
		if a.fps < a.minFPS() {
			a.fps = a.minFPS()
		}
	case write < interval/4:
		a.good++
		if a.good >= screen2RaiseAfter {
			a.good = 0
			a.fps += screen2RaiseFPS
			if a.fps > a.maxFPS {
				a.fps = a.maxFPS
			}
			a.q += 5
			if a.q > a.maxQ {
				a.q = a.maxQ
			}
		}
	case !slow:
		a.good = 0
	}
	return a.q != oq || a.fps != of
}

// Sunucu geri bildirimi (uctan uca geri basinc) sabitleri.
const (
	// screen2FBTTL: bu sureden eski geri bildirim yok sayilir (sunucu degisim
	// yoksa seyrek canlilik mesaji yollar; hic gelmiyorsa kimse veri beklemiyor).
	screen2FBTTL = 4 * time.Second
	// screen2FBQueueSlow: kuyruk yasi bunu asarsa atlama olmasa da tikanma sayilir.
	screen2FBQueueSlow = 300
	// screen2FBRaiseMs: tikanmasiz bu kadar pencere suresi gecince tavan yukselir.
	screen2FBRaiseMs = 1000
	// screen2FBRaiseStep: her yukselis basamagindaki fps.
	screen2FBRaiseStep = 2
	// screen2FBMinQuality: geri bildirim kalite indiriminin tabani.
	screen2FBMinQuality = 30
	screen2FBQDropStep  = 5
	screen2FBQDropMax   = 60
)

// fbState, bir katmanin geri bildirimden turetilen kisitlari.
type fbState struct {
	at      time.Time // son geri bildirim
	ceil    int       // fps tavani; 0 => kisit yok
	cleanMs int       // tikanmasiz biriken sure
	qDrop   int       // kalite indirimi (puan)
}

func (f *fbState) fresh(now time.Time) bool { return f != nil && now.Sub(f.at) <= screen2FBTTL }

// Feedback, sunucunun bir katman icin en yavas izleyici ozetini isler.
//
// Tikanma (atlanan kare var ya da kuyruk yasi > 300 ms): fps tavani, yavas
// izleyicinin teslim hizinin biraz ustune (teslim*1.2+1, en az 3) HEMEN iner.
// Tavan tabana (3) dayandigi halde tikanma suruyorsa kalite 5 puan duser
// (en az 30, katman spec kalitesinin altina da inebilir). Tikanmasiz her
// ~1 sn'de tavan +2 yukselir (tavanin ustune cikinca kisit kalkar) ve kalite
// indirimi 5 puan geri alinir.
func (a *adaptCtl) Feedback(l protocol.Screen2FeedbackLayer, windowMs int, now time.Time) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.fb == nil {
		a.fb = map[int]*fbState{}
	}
	st := a.fb[l.ID]
	if st == nil {
		st = &fbState{}
		a.fb[l.ID] = st
	}
	if !st.fresh(now) {
		// Eski/ilk bilgi: kisitlari sifirdan baslat.
		st.ceil, st.cleanMs, st.qDrop = 0, 0, 0
	}
	st.at = now
	if l.Skipped > 0 || l.QueueMs > screen2FBQueueSlow {
		st.cleanMs = 0
		t := int(l.FPS*1.2) + 1
		t = min(max(t, a.minFPS()), a.maxFPS)
		st.ceil = t
		if t <= a.minFPS() {
			st.qDrop = min(st.qDrop+screen2FBQDropStep, screen2FBQDropMax)
		}
		return
	}
	if windowMs < 0 {
		windowMs = 0
	}
	st.cleanMs += min(windowMs, 5000)
	if st.cleanMs < screen2FBRaiseMs {
		return
	}
	st.cleanMs = 0
	if st.qDrop > 0 {
		st.qDrop = max(st.qDrop-screen2FBQDropStep, 0)
	}
	if st.ceil > 0 {
		st.ceil += screen2FBRaiseStep
		if st.ceil >= a.maxFPS {
			st.ceil = 0
		}
	}
}

// FeedbackIDs, geri bildirim durumu tutulan katman kimlikleri.
func (a *adaptCtl) FeedbackIDs() []int {
	a.mu.Lock()
	defer a.mu.Unlock()
	ids := make([]int, 0, len(a.fb))
	for id := range a.fb {
		ids = append(ids, id)
	}
	return ids
}

// ClearFeedback, bir katmanin geri bildirim kisitlarini siler (katman kalkinca).
func (a *adaptCtl) ClearFeedback(layer int) {
	a.mu.Lock()
	delete(a.fb, layer)
	a.mu.Unlock()
}

// LayerFPS, katmanin hedef fps'i: yerel uyarlama fps'i ile geri bildirim tavaninin asgarisi.
func (a *adaptCtl) LayerFPS(layer int, now time.Time) int {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.layerFPS(layer, now)
}

func (a *adaptCtl) layerFPS(layer int, now time.Time) int {
	f := a.fps
	if st := a.fb[layer]; st.fresh(now) && st.ceil > 0 && st.ceil < f {
		f = st.ceil
	}
	return max(f, a.minFPS())
}

// LayerInterval, katmanin kare araligi.
func (a *adaptCtl) LayerInterval(layer int, now time.Time) time.Duration {
	a.mu.Lock()
	defer a.mu.Unlock()
	return time.Second / time.Duration(a.layerFPS(layer, now))
}

// LayerQuality, katmanin kullanilacak JPEG kalitesi: spec kalitesi (0 => yok)
// ile yerel uyarlama kalitesinin asgarisi, geri bildirim indirimi dusulmus
// (indirim en az 30'a iner; taban zaten 30'un altindaysa dokunulmaz).
func (a *adaptCtl) LayerQuality(layer, specQ int, now time.Time) int {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.layerQuality(layer, specQ, now)
}

func (a *adaptCtl) layerQuality(layer, specQ int, now time.Time) int {
	q := a.q
	if specQ > 0 && specQ < q {
		q = specQ
	}
	if st := a.fb[layer]; st.fresh(now) && st.qDrop > 0 {
		q = max(q-st.qDrop, min(q, screen2FBMinQuality))
	}
	return q
}

// layerInfo, bir etkin katmanin kimligi ve spec kalitesi (istatistik icin).
type layerInfo struct{ id, specQ int }

// LayerStats, panel icin katman basina gercek kalite/hedef fps.
func (a *adaptCtl) LayerStats(ls []layerInfo, now time.Time) []protocol.Screen2LayerStat {
	a.mu.Lock()
	defer a.mu.Unlock()
	out := make([]protocol.Screen2LayerStat, 0, len(ls))
	for _, l := range ls {
		out = append(out, protocol.Screen2LayerStat{
			ID: l.id, Quality: a.layerQuality(l.id, l.specQ, now), SpecQuality: l.specQ,
			TargetFPS: a.layerFPS(l.id, now),
		})
	}
	return out
}
