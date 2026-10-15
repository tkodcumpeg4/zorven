package agent

import "time"

// screen2 uyarlama sinirlari.
const (
	screen2MinQuality = 35
	screen2MinFPS     = 3
	// screen2RaiseAfter: art arda bu kadar "rahat" kareden sonra bir basamak yukselir.
	screen2RaiseAfter = 8
	// screen2SlowWrite: bu kadar surunen tek bir kare yazimi her zaman tikanma sayilir.
	screen2SlowWrite = 250 * time.Millisecond
)

// adaptCtl, yazma suresine gore JPEG kalitesini ve FPS'i ayarlar.
//
// Belirleyici ve durumu kucuk tutulmus: gozlenen "kareyi WSS'e yazma suresi"
// (coder/websocket TCP tamponu doldugunda yazmada bloklar, yani bu sure ag
// basincinin dogrudan olcusudur). Tikanirken hizli dus, rahatken yavas yuksel.
// Goroutine-guvenli DEGIL; yalniz screen2 dongusu kullanir.
type adaptCtl struct {
	maxQ, maxFPS int
	q, fps       int
	good         int
}

func newAdaptCtl(maxQ, maxFPS int) *adaptCtl {
	a := &adaptCtl{}
	a.SetMax(maxQ, maxFPS)
	a.q, a.fps = a.maxQ, a.maxFPS
	return a
}

// SetMax, tavanlari degistirir (kullanici ayari); mevcut degerler tavana kirpilir.
func (a *adaptCtl) SetMax(maxQ, maxFPS int) {
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
func (a *adaptCtl) Quality() int { return a.q }
func (a *adaptCtl) FPS() int     { return a.fps }

// Interval, iki kare arasi hedef sure.
func (a *adaptCtl) Interval() time.Duration { return time.Second / time.Duration(a.fps) }

// Observe, bir karenin yazma suresini bildirir ve degisiklik olduysa true doner.
func (a *adaptCtl) Observe(write time.Duration) bool {
	interval := a.Interval()
	oq, of := a.q, a.fps

	switch {
	case write > interval/2 || write > screen2SlowWrite:
		// Tikanma: kaliteyi ve FPS'i hizla dusur.
		a.good = 0
		a.q -= 10
		if a.q < a.minQ() {
			a.q = a.minQ()
		}
		a.fps = a.fps * 3 / 4
		if a.fps < a.minFPS() {
			a.fps = a.minFPS()
		}
	case write < interval/8:
		a.good++
		if a.good >= screen2RaiseAfter {
			a.good = 0
			a.fps += 2
			if a.fps > a.maxFPS {
				a.fps = a.maxFPS
			}
			a.q += 5
			if a.q > a.maxQ {
				a.q = a.maxQ
			}
		}
	default:
		a.good = 0
	}
	return a.q != oq || a.fps != of
}
