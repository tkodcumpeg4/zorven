package agent

import (
	"bytes"
	"context"
	"image"
	"math/rand"
	"testing"
	"time"
)

func TestHintTilesMapping(t *testing.T) {
	// Olceksiz: 1920x1080, tek dikdortgen 70,70..80,80 => karo (1,1).
	m, tx, ty := hintTiles([]image.Rectangle{image.Rect(70, 70, 80, 80)}, 1920, 1080, 1920, 1080, 0)
	if tx != 30 || ty != 17 {
		t.Fatalf("izgara %dx%d", tx, ty)
	}
	n := 0
	for i, v := range m {
		if v {
			n++
			if i != 1*tx+1 {
				t.Errorf("beklenmeyen karo %d", i)
			}
		}
	}
	if n != 1 {
		t.Fatalf("%d karo", n)
	}
	// Karo sinirini kesen dikdortgen 4 karo (2x2) isaretler.
	m, tx, _ = hintTiles([]image.Rectangle{image.Rect(60, 60, 70, 70)}, 1920, 1080, 1920, 1080, 0)
	cnt := 0
	for _, v := range m {
		if v {
			cnt++
		}
	}
	if cnt != 4 || !m[0] || !m[1] || !m[tx] || !m[tx+1] {
		t.Fatalf("2x2 karo bekleniyordu (%d)", cnt)
	}
	// Olcekli (2x kucultme) + 2px marj: kaynak (126..130) -> hedef 63..65 -> +-2 => 61..67 => karo 0 ve 1.
	m, _, _ = hintTiles([]image.Rectangle{image.Rect(126, 10, 130, 12)}, 1920, 1080, 960, 540, 2)
	if !m[0] || !m[1] {
		t.Fatal("marj komsu karoyu da isaretlemeliydi")
	}
	// Ekran disi / bos dikdortgenler yok sayilir.
	m, _, _ = hintTiles([]image.Rectangle{image.Rect(5000, 5000, 5010, 5010), {}}, 1920, 1080, 1920, 1080, 0)
	for _, v := range m {
		if v {
			t.Fatal("isaret olmamali")
		}
	}
}

func TestDiffFrameHintedMatchesFullDiff(t *testing.T) {
	rng := rand.New(rand.NewSource(7))
	a := synthDesktop(1920, 1080, rng)
	for trial := 0; trial < 20; trial++ {
		b := image.NewRGBA(a.Rect)
		copy(b.Pix, a.Pix)
		var hint []image.Rectangle
		for k := 0; k < 1+rng.Intn(3); k++ {
			x, y := rng.Intn(1850), rng.Intn(1030)
			r := image.Rect(x, y, x+1+rng.Intn(40), y+1+rng.Intn(30)).Intersect(b.Rect)
			noise(b, r, rng)
			hint = append(hint, r)
		}
		want, tx, ty := diffTiles(a, b)
		got, gx, gy := diffFrame(a, b, srcHint{ok: true, rects: hint}, 1920, 1080)
		if tx != gx || ty != gy {
			t.Fatal("izgara farkli")
		}
		for i := range want {
			if want[i] != got[i] {
				t.Fatalf("deneme %d karo %d: tam=%v ipucu=%v", trial, i, want[i], got[i])
			}
		}
	}
	// Ipucu yok => tam fark.
	b := image.NewRGBA(a.Rect)
	copy(b.Pix, a.Pix)
	noise(b, image.Rect(300, 100, 320, 120), rng)
	w, _, _ := diffTiles(a, b)
	g, _, _ := diffFrame(a, b, srcHint{}, 1920, 1080)
	if !boolsEq(w, g) {
		t.Fatal("ipucusuz tam fark bekleniyordu")
	}
	// Bos ipucu => hicbir sey kirli degil.
	g, _, _ = diffFrame(a, a, srcHint{ok: true}, 1920, 1080)
	if anyTrue(g) {
		t.Fatal("bos ipucunda kirli karo olmamali")
	}
}

func boolsEq(a, b []bool) bool {
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

// Olcekli yolda ipucuyla uretilen kare, ipucusuz (tam karsilastirmali) olcekleyiciyle
// birebir ayni olmali; fark da ayni karolari bulmali.
func TestScaleHintedMatchesScale(t *testing.T) {
	rng := rand.New(rand.NewSource(11))
	base := synthDesktop(1920, 1080, rng)
	var fh, fp frameScaler
	prevHint := fh.ScaleHinted(base, 960, srcHint{})
	prevPlain := fp.Scale(base, 960)
	if !bytes.Equal(prevHint.Pix, prevPlain.Pix) {
		t.Fatal("ilk kare farkli")
	}
	cur := base
	for trial := 0; trial < 15; trial++ {
		nxt := image.NewRGBA(cur.Rect)
		copy(nxt.Pix, cur.Pix)
		var hint []image.Rectangle
		for k := 0; k < 1+rng.Intn(3); k++ {
			x, y := rng.Intn(1850), rng.Intn(1030)
			r := image.Rect(x, y, x+1+rng.Intn(60), y+1+rng.Intn(40)).Intersect(nxt.Rect)
			noise(nxt, r, rng)
			hint = append(hint, r)
		}
		h := fh.ScaleHinted(nxt, 960, srcHint{ok: true, rects: hint})
		p := fp.Scale(nxt, 960)
		if !bytes.Equal(h.Pix, p.Pix) {
			t.Fatalf("deneme %d: ipucuyla olceklenen kare farkli", trial)
		}
		wd, _, _ := diffTiles(prevPlain, p)
		gd, _, _ := diffFrame(prevHint, h, srcHint{ok: true, rects: hint}, 1920, 1080)
		if !boolsEq(wd, gd) {
			t.Fatalf("deneme %d: karo farki farkli", trial)
		}
		prevHint, prevPlain, cur = h, p, nxt
	}
}

// --- boru hatti: ipuclu sahte yakalayici -------------------------------------

type hintStep struct {
	img   *image.RGBA // nil => degisim yok
	hint  []image.Rectangle
	plain bool // true => hinted=false (tam goruntu)
}

type fakeHintCap struct {
	fakeCap
	steps []hintStep
	calls int
	done  chan struct{}
}

func (f *fakeHintCap) CaptureHinted(int) (*image.RGBA, []image.Rectangle, bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.calls >= len(f.steps) {
		select {
		case <-f.done:
		default:
			close(f.done)
		}
		return nil, nil, true, nil // sonrasi: degisim yok
	}
	st := f.steps[f.calls]
	f.calls++
	if st.img != nil {
		c := image.NewRGBA(st.img.Rect)
		copy(c.Pix, st.img.Pix)
		return c, st.hint, !st.plain, nil
	}
	return nil, nil, true, nil
}

func TestPipelineUsesHints(t *testing.T) {
	base := image.NewRGBA(image.Rect(0, 0, 320, 192))
	fill(base, base.Rect, 30, 30, 30)
	changed := image.NewRGBA(base.Rect)
	copy(changed.Pix, base.Pix)
	fill(changed, image.Rect(70, 70, 80, 80), 250, 0, 0)

	fc := &fakeHintCap{fakeCap: fakeCap{img: base}, done: make(chan struct{}), steps: []hintStep{
		{img: base, hint: []image.Rectangle{base.Rect}}, // ilk kare: tum ekran
		{}, // degisim yok
		{img: changed, hint: []image.Rectangle{image.Rect(70, 70, 80, 80)}},
	}}
	col := &collector{}
	s := newScreen2Session("scr_h", 3, fc, newGoTileEncoder(), col.send, screen2Params{FPS: 30, Quality: 70, MaxWidth: 4000})
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- s.run(ctx) }()

	waitFor(t, func() bool { return len(col.snapshot()) >= 2 })
	msgs := col.snapshot()
	if !msgs[0].Keyframe {
		t.Fatalf("ilk kare anahtar olmali: %+v", msgs[0])
	}
	if m := msgs[1]; m.Keyframe || len(m.Rects) != 1 || m.Rects[0].X != 64 || m.Rects[0].Y != 64 {
		t.Fatalf("ipucu karesi tek karo: %+v", m)
	}
	// Anahtar kare istegi: degisim yok turunda bile son goruntuyle tam kare gider.
	<-fc.done
	s.Keyframe()
	waitFor(t, func() bool { return len(col.snapshot()) >= 3 })
	m := col.snapshot()[2]
	if !m.Keyframe || m.W != 320 || m.H != 192 {
		t.Fatalf("ipuclu anahtar kare: %+v", m)
	}
	// Anahtar karenin icerigi son goruntu (degismis kare) olmali.
	canvas := image.NewRGBA(base.Rect)
	for _, mm := range col.snapshot()[2:] {
		applyMsg(t, canvas, mm)
	}
	if d := maxDiff(canvas, changed); d > 40 {
		t.Fatalf("anahtar kare icerigi son goruntu degil (fark %d)", d)
	}
	// Degisim yok turlari mesaj uretmez.
	n := len(col.snapshot())
	time.Sleep(150 * time.Millisecond)
	if len(col.snapshot()) != n {
		t.Fatal("degisim yokken mesaj gitti")
	}
	cancel()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if s.met.idleCount() == 0 {
		t.Fatal("bosta tur sayaci artmaliydi")
	}
}
