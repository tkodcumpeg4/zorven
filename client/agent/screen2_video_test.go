package agent

import (
	"bytes"
	"context"
	"errors"
	"image"
	"math"
	"math/rand"
	"sync"
	"testing"
	"time"

	"github.com/tkodcumpeg4/zorven/shared/protocol"
)

// --- Annex-B / avcC ------------------------------------------------------------

var (
	testSPS = []byte{0x67, 0x42, 0xc0, 0x1f, 0xda, 0x01, 0x40, 0x16, 0xe8}
	testPPS = []byte{0x68, 0xce, 0x3c, 0x80}
	testIDR = []byte{0x65, 0x88, 0x84, 0x00, 0x21, 0xff}
	testP   = []byte{0x41, 0x9a, 0x02, 0x03}
)

func annexB(long bool, nals ...[]byte) []byte {
	var b []byte
	for i, n := range nals {
		if long || i > 0 {
			b = append(b, 0, 0, 0, 1)
		} else {
			b = append(b, 0, 0, 1)
		}
		b = append(b, n...)
	}
	return b
}

func TestSplitAnnexBAndAVCC(t *testing.T) {
	for _, long := range []bool{true, false} {
		in := annexB(long, []byte{0x09, 0xf0}, testSPS, testPPS, testIDR)
		nals := splitAnnexB(in)
		if len(nals) != 4 || !bytes.Equal(nals[1], testSPS) || !bytes.Equal(nals[2], testPPS) || !bytes.Equal(nals[3], testIDR) {
			t.Fatalf("long=%v nals=%x", long, nals)
		}
	}
	au := parseAnnexBAU(annexB(true, []byte{0x09, 0xf0}, testSPS, testPPS, testIDR))
	if !au.IDR || !bytes.Equal(au.SPS, testSPS) || !bytes.Equal(au.PPS, testPPS) {
		t.Fatalf("au=%+v", au)
	}
	// AVCC: yalniz kesit, 4 bayt uzunluk onekli (AUD/SPS/PPS atildi).
	want := append([]byte{0, 0, 0, byte(len(testIDR))}, testIDR...)
	if !bytes.Equal(au.AVCC, want) {
		t.Fatalf("avcc=%x want %x", au.AVCC, want)
	}
	p := parseAnnexBAU(annexB(true, testP))
	if p.IDR || p.SPS != nil || len(p.AVCC) != 4+len(testP) {
		t.Fatalf("p=%+v", p)
	}
	if len(splitAnnexB(nil)) != 0 || len(splitAnnexB([]byte{1, 2, 3})) != 0 {
		t.Fatal("bicimsiz girdi bos olmali")
	}
}

func TestAVCDecoderConfig(t *testing.T) {
	cfg, err := avcDecoderConfig(testSPS, testPPS)
	if err != nil {
		t.Fatal(err)
	}
	// ISO 14496-15: surum, profil, uyumluluk, seviye, 0xff (4 bayt uzunluk), 0xe1 (1 SPS).
	if !bytes.Equal(cfg[:6], []byte{1, 0x42, 0xc0, 0x1f, 0xff, 0xe1}) {
		t.Fatalf("baslik %x", cfg[:6])
	}
	n := int(cfg[6])<<8 | int(cfg[7])
	if n != len(testSPS) || !bytes.Equal(cfg[8:8+n], testSPS) {
		t.Fatalf("sps %x", cfg)
	}
	o := 8 + n
	if cfg[o] != 1 || int(cfg[o+1])<<8|int(cfg[o+2]) != len(testPPS) || !bytes.Equal(cfg[o+3:], testPPS) {
		t.Fatalf("pps %x", cfg[o:])
	}
	if got := avcCodecString(testSPS); got != "avc1.42c01f" {
		t.Fatalf("codec=%s", got)
	}
	if _, err := avcDecoderConfig([]byte{1}, testPPS); err == nil {
		t.Fatal("kisa SPS kabul edildi")
	}
	if avcCodecString([]byte{1}) != "" {
		t.Fatal("kisa SPS")
	}
}

// --- NV12 ---------------------------------------------------------------------

func refYUV(r, g, b float64, bt709 bool) (y, u, v float64) {
	if bt709 {
		y = 16 + 0.183*r + 0.614*g + 0.062*b
		u = 128 - 0.101*r - 0.339*g + 0.439*b
		v = 128 + 0.439*r - 0.399*g - 0.040*b
		return
	}
	y = 16 + 0.257*r + 0.504*g + 0.098*b
	u = 128 - 0.148*r - 0.291*g + 0.439*b
	v = 128 + 0.439*r - 0.368*g - 0.071*b
	return
}

func TestRGBAToNV12(t *testing.T) {
	for _, dims := range [][2]int{{8, 6}, {64, 720}} {
		w, h := dims[0], dims[1]
		img := image.NewRGBA(image.Rect(0, 0, w, h))
		fill(img, img.Rect, 200, 40, 90)
		fill(img, image.Rect(0, 0, w/2, h/2), 255, 255, 255) // sol-ust cyrek beyaz
		dst := make([]byte, nv12Size(w, h))
		rgbaToNV12(dst, img, w, h)
		bt := h >= 720
		check := func(name string, got byte, want float64) {
			if math.Abs(float64(got)-want) > 1.5 {
				t.Errorf("%dx%d %s: %d, beklenen %.1f", w, h, name, got, want)
			}
		}
		y, u, v := refYUV(255, 255, 255, bt)
		check("Y beyaz", dst[0], y)
		check("U beyaz", dst[w*h], u)
		check("V beyaz", dst[w*h+1], v)
		y, u, v = refYUV(200, 40, 90, bt)
		check("Y renk", dst[(h-1)*w+w-1], y)
		check("U renk", dst[w*h+(h/2-1)*w+w-2], u)
		check("V renk", dst[w*h+(h/2-1)*w+w-1], v)
	}
	// Ofsetli alt-goruntu (Rect.Min != 0) ve gecersiz boyut zarar vermez.
	base := image.NewRGBA(image.Rect(0, 0, 32, 32))
	fill(base, base.Rect, 0, 0, 0)
	fill(base, image.Rect(8, 8, 16, 16), 255, 255, 255)
	sub := base.SubImage(image.Rect(8, 8, 16, 16)).(*image.RGBA)
	dst := make([]byte, nv12Size(8, 8))
	rgbaToNV12(dst, sub, 8, 8)
	if dst[0] < 230 {
		t.Fatalf("alt-goruntu ofseti hatali: Y=%d", dst[0])
	}
	rgbaToNV12(dst, sub, 7, 8) // tek boyut: yok sayilir (panik yok)
}

func BenchmarkRGBAToNV12_1080p(b *testing.B) {
	img := image.NewRGBA(image.Rect(0, 0, 1920, 1080))
	noise(img, img.Rect, rand.New(rand.NewSource(1)))
	dst := make([]byte, nv12Size(1920, 1080))
	b.SetBytes(1920 * 1080 * 4)
	for i := 0; i < b.N; i++ {
		rgbaToNV12(dst, img, 1920, 1080)
	}
}

// --- gecis durum makinesi ---------------------------------------------------------

func TestVideoSwitchHysteresis(t *testing.T) {
	var s videoSwitch
	t0 := time.Unix(1000, 0)
	at := func(ms int) time.Time { return t0.Add(time.Duration(ms) * time.Millisecond) }
	step := func(ms int, ratio float64) bool {
		v, _ := s.step(at(ms), ratio, protocol.Screen2VideoAuto)
		return v
	}
	// Kisa patlama (<500 ms) video'ya gecirmez.
	for ms := 0; ms <= 400; ms += 50 {
		if step(ms, 0.9) {
			t.Fatalf("%d ms'de erken gecis", ms)
		}
	}
	step(450, 0.05) // sakin tur seriyi sifirlar
	for ms := 500; ms <= 900; ms += 50 {
		if step(ms, 0.9) {
			t.Fatalf("seri sifirlanmadi (%d ms)", ms)
		}
	}
	// Kesintisiz >=500 ms hareket: gecer.
	if !step(1000, 0.9) {
		t.Fatal("0.5 sn kesintisiz hareketten sonra video beklenirdi")
	}
	// Video'da orta oran (0.1-0.3) modu degistirmez, sakin sayaci sifirlar.
	for ms := 1050; ms <= 6000; ms += 50 {
		if !step(ms, 0.2) {
			t.Fatalf("orta oranda cikildi (%d ms)", ms)
		}
	}
	// Sakin 2 sn'den once cikmaz; kesintiye ugrarsa sayac sifirlanir.
	step(6100, 0.01)
	step(7000, 0.01)
	step(7100, 0.2) // kesinti
	for ms := 7200; ms <= 9000; ms += 100 {
		if !step(ms, 0.0) {
			t.Fatalf("erken cikis (%d ms)", ms)
		}
	}
	if step(9300, 0.0) {
		t.Fatal(">2 sn sakin: karoya donmeli")
	}
	// Karoya dondukten sonra minimum bekleme: hemen tekrar girmez.
	for ms := 9400; ms <= 9900; ms += 50 {
		step(ms, 0.9)
	}
	if !step(10900, 0.9) {
		t.Fatal("bekleme sonrasi tekrar video beklenirdi")
	}
	// Zorlama tercihleri.
	var f videoSwitch
	if v, ch := f.step(at(0), 0, protocol.Screen2VideoOn); !v || !ch {
		t.Fatal("video zorlamasi")
	}
	if v, ch := f.step(at(10), 1, protocol.Screen2VideoTiles); v || !ch {
		t.Fatal("tiles zorlamasi")
	}
}

func TestDirtyRatio(t *testing.T) {
	if dirtyRatio(nil) != 0 || dirtyRatio([]bool{true, false, false, true}) != 0.5 {
		t.Fatal("dirtyRatio")
	}
}

func TestVideoRateAdaptation(t *testing.T) {
	r := newVideoRate(1920, 1080, 30)
	start := r.bps
	if start != 4354560 { // 1920*1080*30*0.07
		t.Fatalf("baslangic %d", start)
	}
	t0 := time.Unix(0, 0)
	if !r.observe(t0, true) || r.bps != start*3/4 {
		t.Fatalf("azaltma: %d", r.bps)
	}
	if r.observe(t0.Add(100*time.Millisecond), true) {
		t.Fatal("hiz siniri (500 ms) uygulanmadi")
	}
	for i := 1; i < 40; i++ {
		r.observe(t0.Add(time.Duration(i)*time.Second), true)
	}
	if r.bps != videoMinBitrate {
		t.Fatalf("taban: %d", r.bps)
	}
	// Temizken artar ama baslangic tavanini asmaz.
	for i := 100; i < 400; i++ {
		r.observe(t0.Add(time.Duration(i)*2*time.Second), false)
	}
	if r.bps != r.ceil {
		t.Fatalf("tavana cikmali: %d / %d", r.bps, r.ceil)
	}
	if small := newVideoRate(320, 240, 10); small.bps != videoMinBitrate {
		t.Fatalf("kucuk cozunurluk tabana kirpilmali: %d", small.bps)
	}
	if !feedbackCongested(protocol.Screen2Feedback{Layers: []protocol.Screen2FeedbackLayer{{ID: 0, QueueMs: 400}}}) ||
		!feedbackCongested(protocol.Screen2Feedback{Layers: []protocol.Screen2FeedbackLayer{{ID: 0, Skipped: 3}}}) ||
		feedbackCongested(protocol.Screen2Feedback{Layers: []protocol.Screen2FeedbackLayer{{ID: 0, FPS: 3, QueueMs: 50}}}) {
		t.Fatal("feedbackCongested")
	}
}

// --- uctan uca: sahte kodlayici ile oturum --------------------------------------------

type fakeVideoEnc struct {
	mu     sync.Mutex
	idrs   int
	frames int
	bps    []int
	closed bool
}

func (f *fakeVideoEnc) Name() string { return "fake" }
func (f *fakeVideoEnc) Encode(nv12 []byte, _ time.Duration, force bool) ([]byte, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.frames++
	if force || f.frames == 1 {
		f.idrs++
		return annexB(true, testSPS, testPPS, testIDR), nil
	}
	return annexB(true, testP), nil
}
func (f *fakeVideoEnc) SetBitrate(b int) error {
	f.mu.Lock()
	f.bps = append(f.bps, b)
	f.mu.Unlock()
	return nil
}
func (f *fakeVideoEnc) Close() { f.mu.Lock(); f.closed = true; f.mu.Unlock() }

// noiseCap, her yakalamada tum kareyi degistirir (oran ~1.0).
type noiseCap struct {
	mu     sync.Mutex
	rng    *rand.Rand
	rect   image.Rectangle
	static bool
	img    *image.RGBA
}

func (c *noiseCap) NumDisplays() int           { return 1 }
func (c *noiseCap) Bounds(int) image.Rectangle { return c.rect }
func (c *noiseCap) Capture(int) (*image.RGBA, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.img == nil || !c.static {
		c.img = image.NewRGBA(c.rect)
		noise(c.img, c.rect, c.rng)
	}
	cp := image.NewRGBA(c.rect)
	copy(cp.Pix, c.img.Pix)
	return cp, nil
}

type rawCollector struct {
	mu   sync.Mutex
	msgs [][]byte
}

func (c *rawCollector) send(_ context.Context, b []byte) error {
	c.mu.Lock()
	c.msgs = append(c.msgs, append([]byte(nil), b...))
	c.mu.Unlock()
	return nil
}

func (c *rawCollector) snapshot() [][]byte {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([][]byte(nil), c.msgs...)
}

func (c *rawCollector) count(pred func([]byte) bool) int {
	n := 0
	for _, m := range c.snapshot() {
		if pred(m) {
			n++
		}
	}
	return n
}

func isVideoKind(kind uint8) func([]byte) bool {
	return func(b []byte) bool { return len(b) > 14 && b[0] == protocol.FrameVideo && b[14] == kind }
}

func isTile(b []byte) bool { return len(b) > 0 && b[0] == protocol.FrameScreen }

func withFakeEncoder(t *testing.T) *fakeVideoEnc {
	t.Helper()
	fe := &fakeVideoEnc{}
	old := newVideoEncoderFn
	newVideoEncoderFn = func(w, h, fps, bps int, preferSW bool) (videoEncoder, error) { return fe, nil }
	t.Cleanup(func() { newVideoEncoderFn = old })
	return fe
}

func waitCond(t *testing.T, what string, d time.Duration, f func() bool) {
	t.Helper()
	dl := time.Now().Add(d)
	for time.Now().Before(dl) {
		if f() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("zaman asimi: %s", what)
}

func TestVideoModeEndToEnd(t *testing.T) {
	fe := withFakeEncoder(t)
	col := &rawCollector{}
	cp := &noiseCap{rng: rand.New(rand.NewSource(1)), rect: image.Rect(0, 0, 256, 192)}
	s := newScreen2Session("s", 7, cp, newGoTileEncoder(), col.send, screen2Params{FPS: 30, Quality: 70, MaxWidth: 4000})
	s.vid = newVideoCtl(s)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- s.run(ctx) }()
	defer func() { cancel(); <-done }()

	// Once karo (tam kare), sonra 0.5 sn hareketle video.
	waitCond(t, "ilk karo karesi", 3*time.Second, func() bool { return col.count(isTile) > 0 })
	waitCond(t, "video config", 5*time.Second, func() bool { return col.count(isVideoKind(protocol.VideoKindConfig)) > 0 })
	waitCond(t, "video chunk", 3*time.Second, func() bool { return col.count(isVideoKind(protocol.VideoKindChunk)) > 3 })

	// Mesaj sirasi/icerigi: config, ardindan IDR chunk; chunk'lar AVCC.
	var sawCfg, sawIDR bool
	for _, b := range col.snapshot() {
		m, err := protocol.DecodeVideo(b)
		if err != nil {
			continue
		}
		if m.StreamID != 7 {
			t.Fatalf("streamID=%d", m.StreamID)
		}
		switch m.Kind {
		case protocol.VideoKindConfig:
			sawCfg = true
			if m.Codec != "avc1.42c01f" || m.W != 256 || m.H != 192 {
				t.Fatalf("config %+v", m)
			}
			wantDesc, _ := avcDecoderConfig(testSPS, testPPS)
			if !bytes.Equal(m.Desc, wantDesc) {
				t.Fatalf("description %x", m.Desc)
			}
		case protocol.VideoKindChunk:
			if !sawCfg {
				t.Fatal("config'ten once chunk")
			}
			if m.Keyframe {
				sawIDR = true
			}
			if len(m.Data) < 5 || m.Data[0] != 0 || m.Data[1] != 0 {
				t.Fatalf("chunk AVCC degil: %x", m.Data)
			}
		}
	}
	if !sawIDR {
		t.Fatal("IDR chunk yok")
	}
	if !s.vid.activA.Load() {
		t.Fatal("video aktif degil")
	}

	// Video akarken karo mesaji uretilmez.
	n := col.count(isTile)
	time.Sleep(300 * time.Millisecond)
	if col.count(isTile) != n {
		t.Fatal("video modunda karo gonderildi")
	}

	// Anahtar kare istegi => IDR (karo degil).
	idrs := fe.idrs
	s.Keyframe()
	waitCond(t, "IDR", 2*time.Second, func() bool {
		fe.mu.Lock()
		defer fe.mu.Unlock()
		return fe.idrs > idrs
	})
	if col.count(isTile) != n {
		t.Fatal("anahtar kare istegi video modunda karo uretti")
	}

	// Sunucu karoyu zorlar: stop + tam karo anahtar karesi.
	s.vid.SetPref(protocol.Screen2VideoTiles)
	waitCond(t, "stop", 3*time.Second, func() bool { return col.count(isVideoKind(protocol.VideoKindStop)) > 0 })
	waitCond(t, "karoya donus", 3*time.Second, func() bool { return col.count(isTile) > n })
	var sawKey bool
	for _, b := range col.snapshot()[1:] {
		if isTile(b) {
			if m, err := protocol.DecodeScreen2(b); err == nil && m.Keyframe && m.First {
				sawKey = true
			}
		}
	}
	if !sawKey {
		t.Fatal("karoya donuste anahtar kare yok")
	}
	if s.vid.activA.Load() {
		t.Fatal("video hala aktif")
	}
	fe.mu.Lock()
	closed := fe.closed
	fe.mu.Unlock()
	if !closed {
		t.Fatal("kodlayici kapatilmadi")
	}
}

func TestVideoForcedOnAndAutoReturn(t *testing.T) {
	withFakeEncoder(t)
	col := &rawCollector{}
	cp := &noiseCap{rng: rand.New(rand.NewSource(2)), rect: image.Rect(0, 0, 192, 128), static: true}
	s := newScreen2Session("s", 1, cp, newGoTileEncoder(), col.send, screen2Params{FPS: 30, Quality: 70, MaxWidth: 4000})
	s.vid = newVideoCtl(s)
	s.vid.SetPref(protocol.Screen2VideoOn) // durgun ekranda da video
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- s.run(ctx) }()
	defer func() { cancel(); <-done }()
	waitCond(t, "zorlanmis video", 3*time.Second, func() bool { return col.count(isVideoKind(protocol.VideoKindChunk)) > 0 })

	// Auto'ya dusunce durgun ekran ~2 sn sonra karoya doner.
	s.vid.SetPref(protocol.Screen2VideoAuto)
	waitCond(t, "otomatik karoya donus", 6*time.Second, func() bool { return col.count(isVideoKind(protocol.VideoKindStop)) > 0 })
	waitCond(t, "karo anahtar karesi", 3*time.Second, func() bool { return col.count(isTile) > 0 })
}

type failVideoEnc struct{ fakeVideoEnc }

func (f *failVideoEnc) Encode([]byte, time.Duration, bool) ([]byte, error) {
	return nil, errors.New("kodlayici cokt")
}

func TestVideoEncoderFailureFallsBackToTiles(t *testing.T) {
	old := newVideoEncoderFn
	newVideoEncoderFn = func(w, h, fps, bps int, preferSW bool) (videoEncoder, error) { return &failVideoEnc{}, nil }
	defer func() { newVideoEncoderFn = old }()
	col := &rawCollector{}
	cp := &noiseCap{rng: rand.New(rand.NewSource(3)), rect: image.Rect(0, 0, 192, 128)}
	s := newScreen2Session("s", 1, cp, newGoTileEncoder(), col.send, screen2Params{FPS: 30, Quality: 70, MaxWidth: 4000})
	s.vid = newVideoCtl(s)
	s.vid.SetPref(protocol.Screen2VideoOn)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- s.run(ctx) }()
	defer func() { cancel(); <-done }()
	waitCond(t, "hata sonrasi karo", 4*time.Second, func() bool {
		return s.vid.broken.Load() && col.count(isVideoKind(protocol.VideoKindChunk)) == 0 && col.count(isTile) >= 2
	})
	if s.vid.activA.Load() {
		t.Fatal("kodlayici bozukken video aktif kaldi")
	}
}

func TestVideoStatsFill(t *testing.T) {
	s := &screen2Session{ctl: newAdaptCtl(70, 30)}
	v := newVideoCtl(s)
	var st protocol.Screen2Stats
	v.fillStats(&st, time.Now())
	if st.Mode != "tiles" || !st.Valid() {
		t.Fatalf("%+v", st)
	}
	v.setActive(true)
	b := "fake"
	v.backend.Store(&b)
	v.curBps.Store(2_000_000)
	v.bytesTotal.Store(125_000)
	now := time.Now()
	v.fillStats(&st, now)
	v.bytesTotal.Store(250_000)
	v.fillStats(&st, now.Add(time.Second))
	if st.Mode != "video" || st.Backend != "fake" || st.VideoBitrate != 2_000_000 || st.VideoKbps != 1000 || !st.Valid() {
		t.Fatalf("%+v", st)
	}
}

func TestVideoEncoderUnavailableOffWindows(t *testing.T) {
	if _, err := platNewVideoEncoder(320, 240, 30, 1_000_000, false); err == nil {
		t.Skip("bu platformda gercek kodlayici var")
	}
}
