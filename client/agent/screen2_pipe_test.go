package agent

import (
	"bytes"
	"context"
	"encoding/binary"
	"image"
	"image/draw"
	"image/jpeg"
	"image/png"
	"math/rand"
	"sync"
	"testing"
	"time"

	"github.com/tkodcumpeg4/zorven/shared/protocol"
)

func TestSlotMergesDroppedJobs(t *testing.T) {
	q := newS2Slot()
	a := &s2Job{layers: []s2LayerJob{
		{layer: -1, dirty: []bool{true, false, false, false}, tx: 2, ty: 2},
		{layer: 1, dirty: []bool{false, true}, tx: 2, ty: 1, key: true},
	}}
	b := &s2Job{layers: []s2LayerJob{
		{layer: -1, dirty: []bool{false, false, true, false}, tx: 2, ty: 2},
	}}
	q.put(a)
	q.put(b) // a atilir, b'ye birlesir
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	got, ok := q.take(ctx)
	if !ok || got != b {
		t.Fatal("en son is donmeli")
	}
	if q.dropped != 1 {
		t.Fatalf("atilan=%d", q.dropped)
	}
	want := []bool{true, false, true, false}
	for i, v := range want {
		if got.layers[0].dirty[i] != v {
			t.Fatalf("birlesik maske %v", got.layers[0].dirty)
		}
	}
	if len(got.layers) != 2 || got.layers[1].layer != 1 || !got.layers[1].key {
		t.Fatalf("yenide olmayan katman (anahtar bayrakli) tasinmali: %+v", got.layers)
	}
	// anahtar bayragi korunur
	q.put(&s2Job{layers: []s2LayerJob{{layer: 0, dirty: []bool{true}, tx: 1, ty: 1, key: true}}})
	q.put(&s2Job{layers: []s2LayerJob{{layer: 0, dirty: []bool{false}, tx: 1, ty: 1}}})
	got, _ = q.take(ctx)
	if !got.layers[0].key {
		t.Fatal("anahtar bayragi kayboldu")
	}
	// bos yuva + iptal
	c2, cancel2 := context.WithCancel(context.Background())
	cancel2()
	if _, ok := q.take(c2); ok {
		t.Fatal("iptalde false donmeli")
	}
}

func TestEncodeRectsParallelMatchesSerialAndKeepsOrder(t *testing.T) {
	src := synthDesktop(1280, 720, rand.New(rand.NewSource(4)))
	noise(src, image.Rect(600, 100, 1000, 500), rand.New(rand.NewSource(5)))
	var rects []image.Rectangle
	for y := 0; y < 720; y += 128 {
		for x := 0; x < 1280; x += 256 {
			rects = append(rects, image.Rect(x, y, min(x+256, 1280), min(y+128, 720)))
		}
	}
	enc := newGoTileEncoder()
	serial, err := encodeRectsParallel(enc, src, rects, 70, 1)
	if err != nil {
		t.Fatal(err)
	}
	par, err := encodeRectsParallel(enc, src, rects, 70, 7)
	if err != nil {
		t.Fatal(err)
	}
	if len(serial) != len(par) {
		t.Fatalf("rect sayisi %d != %d", len(serial), len(par))
	}
	for i := range serial {
		a, b := serial[i], par[i]
		if a.X != b.X || a.Y != b.Y || a.W != b.W || a.H != b.H || a.Enc != b.Enc || !bytes.Equal(a.Payload, b.Payload) {
			t.Fatalf("rect %d farkli", i)
		}
	}
}

// applyMsg, mesajdaki dikdortgenleri tuvale uygular (solid/PNG/JPEG).
func applyMsg(t *testing.T, canvas *image.RGBA, m protocol.Screen2Message) {
	for _, r := range m.Rects {
		rr := image.Rect(int(r.X), int(r.Y), int(r.X)+int(r.W), int(r.Y)+int(r.H))
		switch r.Enc {
		case protocol.Screen2EncSolid:
			c := binary.BigEndian.Uint32(r.Payload)
			fill(canvas, rr, uint8(c>>24), uint8(c>>16), uint8(c>>8))
		default:
			var im image.Image
			var err error
			if r.Enc == protocol.Screen2EncPNG {
				im, err = png.Decode(bytes.NewReader(r.Payload))
			} else {
				im, err = jpeg.Decode(bytes.NewReader(r.Payload))
			}
			if err != nil {
				t.Errorf("cozme: %v", err)
				return
			}
			draw.Draw(canvas, rr, im, im.Bounds().Min, draw.Src)
		}
	}
}

// maxDiff, iki goruntunun en buyuk kanal farki.
func maxDiff(a, b *image.RGBA) int {
	d := 0
	for i := 0; i < len(a.Pix); i += 4 {
		for c := 0; c < 3; c++ {
			x := int(a.Pix[i+c]) - int(b.Pix[i+c])
			if x < 0 {
				x = -x
			}
			d = max(d, x)
		}
	}
	return d
}

// mutCap, her yakalamada farkli bir 64 px blogu degistirir (sirayla).
type mutCap struct {
	mu  sync.Mutex
	img *image.RGBA
	n   int
}

func (m *mutCap) NumDisplays() int           { return 1 }
func (m *mutCap) Bounds(int) image.Rectangle { return m.img.Rect }
func (m *mutCap) Capture(int) (*image.RGBA, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.n < 60 { // 60 yakalamada degisir, sonra sabit
		k := m.n % 15
		fill(m.img, image.Rect(k*64, (m.n%4)*64, k*64+64, (m.n%4)*64+64), uint8(m.n*4+1), uint8(m.n), 7)
		m.n++
	}
	c := image.NewRGBA(m.img.Rect)
	copy(c.Pix, m.img.Pix)
	return c, nil
}

// Takilan gonderici: ara kareler birlesir ama son durum tam dogru olmali.
func TestPipelineSlowSenderConverges(t *testing.T) {
	src := image.NewRGBA(image.Rect(0, 0, 960, 256))
	fill(src, src.Rect, 10, 10, 10)
	mc := &mutCap{img: src}
	var mu sync.Mutex
	canvas := image.NewRGBA(src.Rect)
	var msgs []protocol.Screen2Message
	first := true
	send := func(ctx context.Context, b []byte) error {
		if first { // ag takilmasi: ilk yazma 400 ms bekler, bu sirada kareler birlesir
			first = false
			time.Sleep(400 * time.Millisecond)
		}
		m, err := protocol.DecodeScreen2(b)
		if err != nil {
			return err
		}
		mu.Lock()
		msgs = append(msgs, m)
		applyMsg(t, canvas, m)
		mu.Unlock()
		return nil
	}
	s := newScreen2Session("s", 1, mc, newGoTileEncoder(), send, screen2Params{FPS: 30, Quality: 70, MaxWidth: 4000})
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- s.run(ctx) }()
	waitFor(t, func() bool {
		mc.mu.Lock()
		n := mc.n
		mc.mu.Unlock()
		return n >= 60
	})
	// son mesajlar gonderilsin
	time.Sleep(300 * time.Millisecond)
	cancel()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(msgs) == 0 || len(msgs) >= 60 {
		t.Fatalf("birlestirme beklenirdi: %d mesaj / 60 yakalama", len(msgs))
	}
	for i, m := range msgs { // sira korunur
		if m.Seq != uint32(i) {
			t.Fatalf("seq sirasi: %d. mesaj seq=%d", i, m.Seq)
		}
	}
	if d := maxDiff(canvas, src); d > 24 {
		t.Fatalf("alici tuvali son yakalanan kareyle ayni degil (guncelleme kayboldu), fark=%d", d)
	}
}

// Kodlayici hata verirse run hatayi dondurur ve donguler durur.
type failEnc struct{}

func (failEnc) Encode(*image.RGBA, image.Rectangle, int) (uint8, []byte, error) {
	return 0, nil, context.DeadlineExceeded
}

func TestPipelineEncoderErrorStopsRun(t *testing.T) {
	src := image.NewRGBA(image.Rect(0, 0, 128, 128))
	fill(src, src.Rect, 1, 2, 3)
	s := newScreen2Session("s", 1, &fakeCap{img: src}, failEnc{}, func(context.Context, []byte) error { return nil },
		screen2Params{FPS: 30, MaxWidth: 4000})
	done := make(chan error, 1)
	go func() { done <- s.run(context.Background()) }()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("hata bekleniyordu")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("run durmadi")
	}
}
