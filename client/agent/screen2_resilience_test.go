package agent

import (
	"image"
	"testing"
	"time"
)

func TestHintAccumOrsRects(t *testing.T) {
	var a hintAccum
	a.Add(srcHint{ok: true, rects: []image.Rectangle{image.Rect(0, 0, 10, 10)}})
	a.Add(srcHint{ok: true}) // degisim yok turu: onceki korunur
	a.Add(srcHint{ok: true, rects: []image.Rectangle{image.Rect(100, 100, 120, 120)}})
	h := a.Take()
	if !h.ok || len(h.rects) != 2 {
		t.Fatalf("iki turun ipucu birikmeli: %+v", h)
	}
	// Take sifirlar
	if h := a.Take(); !h.ok || len(h.rects) != 0 {
		t.Fatalf("sifirlanmali: %+v", h)
	}
}

func TestHintAccumNoHintPoisons(t *testing.T) {
	var a hintAccum
	a.Add(srcHint{ok: true, rects: []image.Rectangle{image.Rect(0, 0, 10, 10)}})
	a.Add(srcHint{}) // ipucu yok: tam karsilastirma
	a.Add(srcHint{ok: true, rects: []image.Rectangle{image.Rect(5, 5, 6, 6)}})
	if h := a.Take(); h.ok {
		t.Fatalf("ipucu yok turu sonrasi ipucu gecersiz olmali: %+v", h)
	}
	// Sonraki dongu temiz
	a.Add(srcHint{ok: true})
	if h := a.Take(); !h.ok {
		t.Fatal("temiz dongu ipucu verebilmeli")
	}
}

func TestHintAccumCollapses(t *testing.T) {
	var a hintAccum
	for i := 0; i < hintAccumMaxRects+5; i++ {
		a.Add(srcHint{ok: true, rects: []image.Rectangle{image.Rect(i, i, i+1, i+1)}})
	}
	h := a.Take()
	if !h.ok || len(h.rects) > hintAccumMaxRects {
		t.Fatalf("birikim sinirlanmali: %d", len(h.rects))
	}
	// kapsam daralmamali
	cover := image.Rect(0, 0, 1, 1).Union(image.Rect(hintAccumMaxRects+4, hintAccumMaxRects+4, hintAccumMaxRects+5, hintAccumMaxRects+5))
	var u image.Rectangle
	for _, r := range h.rects {
		u = u.Union(r)
	}
	if u != cover {
		t.Fatalf("kapsam %v, beklenen %v", u, cover)
	}
}

func TestCaptureRetryBackoffAndGiveUp(t *testing.T) {
	var r captureRetry
	t0 := time.Unix(1000, 0)
	prev := time.Duration(0)
	for i := 0; i < 6; i++ {
		w, give := r.Fail(t0.Add(time.Duration(i) * time.Second))
		if give {
			t.Fatalf("erken vazgecti i=%d", i)
		}
		if w < prev || w > captureRetryMax {
			t.Fatalf("bekleme artmali ve sinirli olmali: %v (onceki %v)", w, prev)
		}
		prev = w
	}
	if prev != captureRetryMax {
		t.Fatalf("tavan: %v", prev)
	}
	if _, give := r.Fail(t0.Add(captureRetryWindow + time.Second)); !give {
		t.Fatal("pencere dolunca vazgecmeli")
	}
}

func TestCaptureRetryRecovery(t *testing.T) {
	var r captureRetry
	if r.Succeed() {
		t.Fatal("hatasiz basari 'toparlandi' degil")
	}
	r.Fail(time.Unix(1, 0))
	if !r.Failing() {
		t.Fatal("hata suruyor")
	}
	if !r.Succeed() {
		t.Fatal("toparlandi bildirilmeli")
	}
	// yeni seri sifirdan baslar
	w, _ := r.Fail(time.Unix(2000, 0))
	if w != captureRetryMin {
		t.Fatalf("yeni seri min beklemeyle baslamali: %v", w)
	}
}

func TestBoundsWatch(t *testing.T) {
	var b boundsWatch
	cur := image.Rect(0, 0, 1920, 1080)
	q := func() image.Rectangle { return cur }
	t0 := time.Unix(100, 0)

	if _, ch := b.Check(t0, 1920, 1080, q); ch {
		t.Fatal("ilk okuma degisim sayilmaz")
	}
	cur = image.Rect(0, 0, 2560, 1440)
	// kare boyutu ayni ve periyot dolmadi: sorgulanmaz
	if _, ch := b.Check(t0.Add(100*time.Millisecond), 1920, 1080, q); ch {
		t.Fatal("periyot dolmadan sorgulanmamali")
	}
	// kare boyutu degisti: hemen sorgula
	got, ch := b.Check(t0.Add(200*time.Millisecond), 2560, 1440, q)
	if !ch || got != cur {
		t.Fatalf("kare boyutu degisince sinir yenilenmeli: %v %v", got, ch)
	}
	// periyodik: kare boyutu ayni kalsa da yerlesim degisebilir
	cur = image.Rect(-1920, 0, 640, 1440)
	if _, ch := b.Check(t0.Add(500*time.Millisecond), 2560, 1440, q); ch {
		t.Fatal("henuz periyot dolmadi")
	}
	if got, ch := b.Check(t0.Add(1500*time.Millisecond), 2560, 1440, q); !ch || got != cur {
		t.Fatal("periyot dolunca degisim yakalanmali")
	}
	// ayni sinir: degisim yok
	if _, ch := b.Check(t0.Add(3*time.Second), 2560, 1440, q); ch {
		t.Fatal("ayni sinir degisim degil")
	}
}

func TestIdlePacer(t *testing.T) {
	var p idlePacer
	base := 66 * time.Millisecond
	for i := 0; i < idleRoundsBeforeSlow-1; i++ {
		p.Idle()
		if got := p.Interval(base); got != base {
			t.Fatalf("esik oncesi tam hiz: %v", got)
		}
	}
	prev := base
	for i := 0; i < 40; i++ {
		p.Idle()
		got := p.Interval(base)
		if got < prev {
			t.Fatalf("yavaslama monoton olmali: %v < %v", got, prev)
		}
		prev = got
	}
	if prev != idleMaxInterval {
		t.Fatalf("taban %v bekleniyordu, %v", idleMaxInterval, prev)
	}
	p.Active()
	if got := p.Interval(base); got != base {
		t.Fatalf("degisimde hemen tam hiza donmeli: %v", got)
	}
	// taban zaten yavassa degismez
	p.n = 100
	if got := p.Interval(time.Second); got != time.Second {
		t.Fatalf("%v", got)
	}
}
