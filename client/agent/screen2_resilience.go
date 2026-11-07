package agent

import (
	"image"
	"time"
)

// Yakalama dongusunun dayaniklilik yardimcilari (platformdan bagimsiz).

// ---------------------------------------------------------------------------
// Kirli bolge ipucu birikimi (katmanli mod)

// hintAccum, bir katmanin son islenmesinden beri gelen yakalama ipuclarini
// BIRIKTIRIR (OR). Katmanlar farkli fps'te calisir: vadesi gelmeyen bir turun
// ipucu atilirsa, katman sonraki vadede yalniz O turun ipucuna bakar ve arada
// degisen bolgeleri hic gormez (ekran bozulur).
type hintAccum struct {
	full  bool // ipucu yok turu goruldu: tam karsilastirma gerekli
	rects []image.Rectangle
}

// hintAccumMaxRects, birikim bu sayiyi asarsa dikdortgenler tek sinir kutusuna
// indirgenir (bellek ve karo hesabi sinirli kalsin; kapsam GENISLER, daralmaz).
const hintAccumMaxRects = 512

// Add, bir yakalama turunun ipucunu ekler.
func (a *hintAccum) Add(h srcHint) {
	if !h.ok {
		a.full = true
		a.rects = nil
		return
	}
	if a.full {
		return
	}
	a.rects = append(a.rects, h.rects...)
	if len(a.rects) > hintAccumMaxRects {
		var u image.Rectangle
		for _, r := range a.rects {
			u = u.Union(r)
		}
		a.rects = []image.Rectangle{u}
	}
}

// Take, birikmis ipucunu doner ve sifirlar. full => ipucu yok (srcHint{}).
func (a *hintAccum) Take() srcHint {
	h := srcHint{}
	if !a.full {
		h = srcHint{ok: true, rects: a.rects}
	}
	a.full, a.rects = false, nil
	return h
}

// ---------------------------------------------------------------------------
// Yakalama hatasi yeniden deneme politikasi

const (
	captureRetryWindow = 30 * time.Second
	captureRetryMin    = 250 * time.Millisecond
	captureRetryMax    = 4 * time.Second
)

// captureRetry, ardisik yakalama hatalarini ussel geri cekilmeyle yeniden
// dener; captureRetryWindow boyunca hic basari yoksa vazgecer.
type captureRetry struct {
	firstFail time.Time
	fails     int
}

// Fail, bir hatayi kaydeder. giveUp=true => oturum sonlandirilmali; degilse
// wait kadar bekleyip yeniden dene.
func (r *captureRetry) Fail(now time.Time) (wait time.Duration, giveUp bool) {
	if r.fails == 0 {
		r.firstFail = now
	}
	r.fails++
	if now.Sub(r.firstFail) > captureRetryWindow {
		return 0, true
	}
	wait = captureRetryMin
	for i := 1; i < r.fails && wait < captureRetryMax; i++ {
		wait *= 2
	}
	return min(wait, captureRetryMax), false
}

// Failing, ardisik hata surerken true.
func (r *captureRetry) Failing() bool { return r.fails > 0 }

// Succeed, basariyi kaydeder; hata seri bittiyse true doner (toparlandi).
func (r *captureRetry) Succeed() (recovered bool) {
	recovered = r.fails > 0
	r.fails = 0
	return
}

// ---------------------------------------------------------------------------
// Sinir (cozunurluk/DPI/yerlesim) yenileme

// boundsWatch, izlenen monitorun sinirlarini kare boyutu degisince ya da
// periyodik olarak yeniden sorar; degisince girdi eslemesi ve imlec
// katmani icin bildirim gerekir.
type boundsWatch struct {
	last  image.Rectangle
	frame image.Point
	at    time.Time
	every time.Duration
	init  bool
}

// Check, kare boyutu (fw,fh) degistiyse ya da periyot dolduysa query ile
// sinirlari yeniden okur. changed=true => yeni sinirlar cur'dadir.
func (b *boundsWatch) Check(now time.Time, fw, fh int, query func() image.Rectangle) (cur image.Rectangle, changed bool) {
	if b.every <= 0 {
		b.every = time.Second
	}
	fp := image.Pt(fw, fh)
	if b.init && fp == b.frame && now.Sub(b.at) < b.every {
		return b.last, false
	}
	b.at = now
	cur = query()
	first := !b.init
	b.init = true
	b.frame = fp
	if first {
		b.last = cur
		return cur, false // ilk okuma: run() zaten notifyDisplay yapti
	}
	if cur == b.last {
		return cur, false
	}
	b.last = cur
	return cur, true
}

// Reset, izlemeyi sifirlar (monitor degisti: sonraki Check yalniz tabani alir).
func (b *boundsWatch) Reset() { b.init = false }

// ---------------------------------------------------------------------------
// Bosta kalma hizi

const (
	idleRoundsBeforeSlow = 8
	idleMaxInterval      = 400 * time.Millisecond // ~2.5 fps tabani
)

// idlePacer, ardisik "degisim yok" turlarindan sonra yakalama hizini
// kademeli dusurur; degisim olunca (Active) hemen tam hiza doner.
type idlePacer struct{ n int }

func (p *idlePacer) Idle()   { p.n++ }
func (p *idlePacer) Active() { p.n = 0 }

// Interval, taban araliktan (base) bosta kalma yavaslamasini uygulanmis arayi doner.
func (p *idlePacer) Interval(base time.Duration) time.Duration {
	if p.n < idleRoundsBeforeSlow || base >= idleMaxInterval {
		return base
	}
	iv := base
	steps := (p.n-idleRoundsBeforeSlow)/4 + 1
	for i := 0; i < steps && iv < idleMaxInterval; i++ {
		iv *= 2
	}
	return min(iv, idleMaxInterval)
}
