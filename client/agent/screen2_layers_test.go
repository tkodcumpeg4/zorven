package agent

import (
	"context"
	"image"
	"sync"
	"testing"
	"time"

	"github.com/tkodcumpeg4/zorven/shared/protocol"
)

func TestLayerTileRectsAligned(t *testing.T) {
	w, h := 300, 200
	tx, ty := 5, 4
	dirty := make([]bool, tx*ty)
	dirty[0], dirty[4], dirty[ty*tx-1], dirty[7] = true, true, true, true
	rects := layerTileRects(dirty, tx, ty, w, h)
	if len(rects) != 4 {
		t.Fatalf("rects=%v", rects)
	}
	for _, r := range rects {
		if r.Min.X%tileSize != 0 || r.Min.Y%tileSize != 0 {
			t.Errorf("hizasiz: %v", r)
		}
		if r.Dx() > tileSize || r.Dy() > tileSize || r.Max.X > w || r.Max.Y > h {
			t.Errorf("tuval/karo disi: %v", r)
		}
	}
	// son karo kenardan kirpilmis: x 256..300, y 192..200
	if last := rects[len(rects)-1]; last != image.Rect(256, 192, 300, 200) {
		t.Errorf("kenar karosu: %v", last)
	}
}

func TestLayerWidthResolution(t *testing.T) {
	two := []protocol.Screen2LayerSpec{{ID: 0, MaxWidth: 1200}, {ID: 1}}
	if w := layerWidth(two[0], two); w != 1200 {
		t.Errorf("katman0=%d", w)
	}
	if w := layerWidth(two[1], two); w != 600 {
		t.Errorf("katman1 yari olmali: %d", w)
	}
	only1 := []protocol.Screen2LayerSpec{{ID: 1}}
	if w := layerWidth(only1[0], only1); w != defaultMaxWidth/2 {
		t.Errorf("tek katman1=%d", w)
	}
}

// sayacli kodlayici: hangi genislikteki goruntuden kac karo kodlandi.
type countEnc struct {
	inner *goTileEncoder
	mu    sync.Mutex
	byW   map[int]int
}

func (c *countEnc) Encode(img *image.RGBA, r image.Rectangle, q int) (uint8, []byte, error) {
	c.mu.Lock()
	c.byW[img.Rect.Dx()]++
	c.mu.Unlock()
	return c.inner.Encode(img, r, q)
}

func (c *countEnc) count(w int) int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.byW[w]
}

func (c *collector) byLayer(l uint8) []protocol.Screen2Message {
	var out []protocol.Screen2Message
	for _, m := range c.snapshot() {
		if m.Layer == l {
			out = append(out, m)
		}
	}
	return out
}

func TestScreen2LayeredSession(t *testing.T) {
	src := image.NewRGBA(image.Rect(0, 0, 320, 192))
	fill(src, src.Rect, 40, 40, 40)
	fc := &fakeCap{img: src}
	col := &collector{}
	ce := &countEnc{inner: newGoTileEncoder(), byW: map[int]int{}}
	specs := []protocol.Screen2LayerSpec{{ID: 0, MaxWidth: 4000, Quality: 80}, {ID: 1, MaxWidth: 160, Quality: 40}}
	s := newScreen2Session("scr_l", 9, fc, ce, col.send,
		screen2Params{FPS: 20, Layers: specs})

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- s.run(ctx) }()

	// 1) ilk tur: iki katman da tam kare (keyframe), karo hizali, tuval boyutlari farkli
	waitFor(t, func() bool { return len(col.byLayer(0)) >= 1 && len(col.byLayer(1)) >= 1 })
	m0, m1 := col.byLayer(0)[0], col.byLayer(1)[0]
	if !m0.Keyframe || m0.W != 320 || m0.H != 192 || len(m0.Rects) != 5*3 {
		t.Fatalf("katman0 ilk kare: kf=%v %dx%d rects=%d", m0.Keyframe, m0.W, m0.H, len(m0.Rects))
	}
	if !m1.Keyframe || m1.W != 160 || m1.H != 96 || len(m1.Rects) != 3*2 {
		t.Fatalf("katman1 ilk kare: kf=%v %dx%d rects=%d", m1.Keyframe, m1.W, m1.H, len(m1.Rects))
	}
	for _, r := range m0.Rects {
		if r.X%tileSize != 0 || r.Y%tileSize != 0 || r.W > tileSize || r.H > tileSize {
			t.Fatalf("hizasiz dikdortgen %+v", r)
		}
	}

	// 2) kucuk degisiklik: her katmanda TEK karo
	fc.mu.Lock()
	fill(fc.img, image.Rect(70, 70, 80, 80), 200, 0, 0)
	fc.mu.Unlock()
	waitFor(t, func() bool { return len(col.byLayer(0)) >= 2 && len(col.byLayer(1)) >= 2 })
	d0, d1 := col.byLayer(0)[1], col.byLayer(1)[1]
	if d0.Keyframe || len(d0.Rects) != 1 || d0.Rects[0].X != 64 || d0.Rects[0].Y != 64 {
		t.Fatalf("katman0 delta: %+v", d0)
	}
	if d1.Keyframe || len(d1.Rects) != 1 || d1.Rects[0].X != 0 || d1.Rects[0].Y != 0 {
		t.Fatalf("katman1 delta: %+v", d1)
	}

	// 3) katman 1 kalkar: bir daha KODLANMAZ
	s.SetLayers(specs[:1], false)
	time.Sleep(150 * time.Millisecond)
	enc1 := ce.count(160)
	n1 := len(col.byLayer(1))
	fc.mu.Lock()
	fill(fc.img, image.Rect(10, 10, 20, 20), 9, 9, 200)
	fc.mu.Unlock()
	waitFor(t, func() bool { return len(col.byLayer(0)) >= 3 })
	time.Sleep(100 * time.Millisecond)
	if ce.count(160) != enc1 || len(col.byLayer(1)) != n1 {
		t.Fatalf("abonesiz katman kodlandi: enc %d->%d msgs %d->%d", enc1, ce.count(160), n1, len(col.byLayer(1)))
	}

	// 4) katman 1 yeniden eklenir: tam kare (keyframe)
	s.SetLayers(specs, true)
	waitFor(t, func() bool { return len(col.byLayer(1)) > n1 })
	re := col.byLayer(1)[n1]
	if !re.Keyframe || len(re.Rects) != 3*2 {
		t.Fatalf("yeniden eklenen katman keyframe olmali: %+v", re)
	}

	// 5) yalniz katman 1: katman 0 durur
	s.SetLayers(specs[1:], false)
	time.Sleep(150 * time.Millisecond)
	n0 := len(col.byLayer(0))
	fc.mu.Lock()
	fill(fc.img, image.Rect(200, 100, 210, 110), 1, 200, 1)
	fc.mu.Unlock()
	waitFor(t, func() bool { return len(col.byLayer(1)) > n1+1 })
	if len(col.byLayer(0)) != n0 {
		t.Fatal("abonesiz katman 0 mesaj gonderdi")
	}

	// 6) hic katman yok: yakalama bile yapilmaz
	s.SetLayers(nil, false)
	time.Sleep(250 * time.Millisecond)
	fc.mu.Lock()
	c0 := fc.n
	fc.mu.Unlock()
	time.Sleep(300 * time.Millisecond)
	fc.mu.Lock()
	c1 := fc.n
	fc.mu.Unlock()
	if c1 != c0 {
		t.Fatalf("katman yokken yakalama suruyor: %d -> %d", c0, c1)
	}

	cancel()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}

func TestApplyLayerUpdateKeepsKeyRequest(t *testing.T) {
	cur := applyLayerUpdate(nil, layerUpdate{set: true, layers: []protocol.Screen2LayerSpec{{ID: 0, MaxWidth: 800}}})
	if len(cur) != 1 || !cur[0].needKey {
		t.Fatal("yeni katman tam kare istemeli")
	}
	cur[0].needKey = false
	// ayni kume: degisiklik yok
	cur = applyLayerUpdate(cur, layerUpdate{set: true, layers: []protocol.Screen2LayerSpec{{ID: 0, MaxWidth: 800, Quality: 50}}})
	if cur[0].needKey {
		t.Fatal("ayni genislikte anahtar kare istenmemeli")
	}
	// genislik degisti
	cur = applyLayerUpdate(cur, layerUpdate{set: true, layers: []protocol.Screen2LayerSpec{{ID: 0, MaxWidth: 640}}})
	if !cur[0].needKey || cur[0].prev != nil {
		t.Fatal("genislik degisimi tam kare istemeli")
	}
	// gecersiz/yinelenen kimlikler elenir
	cur = applyLayerUpdate(cur, layerUpdate{set: true, layers: []protocol.Screen2LayerSpec{{ID: 9}, {ID: 1}, {ID: 1}}})
	if len(cur) != 1 || cur[0].spec.ID != 1 {
		t.Fatalf("%+v", cur)
	}
}
