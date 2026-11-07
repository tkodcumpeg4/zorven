package agent

import (
	"context"
	"errors"
	"image"
	"math/rand"
	"sync"
	"testing"
	"time"

	"github.com/tkodcumpeg4/zorven/shared/protocol"
)

func TestVideoRetryPolicy(t *testing.T) {
	var r videoRetry
	t0 := time.Unix(1000, 0)
	prev := time.Duration(0)
	for i := 1; i < videoRetryMax; i++ {
		w, perm := r.Fail(t0)
		if perm {
			t.Fatalf("hata %d kalici olmamali", i)
		}
		if w < prev || w > videoRetryCap {
			t.Fatalf("bekleme monoton ve sinirli olmali: %v (onceki %v)", w, prev)
		}
		prev = w
		if r.Ready(t0) {
			t.Fatal("bekleme suresince hazir olmamali")
		}
		if !r.Ready(t0.Add(w)) {
			t.Fatal("bekleme bitince hazir olmali")
		}
	}
	if _, perm := r.Fail(t0); !perm || !r.Permanent() {
		t.Fatal("videoRetryMax sonrasi kalici olmali")
	}
	if r.Ready(t0.Add(time.Hour)) {
		t.Fatal("kalici hata sonrasi hazir olmamali")
	}
	r.Reset()
	if r.Permanent() || !r.Ready(t0) {
		t.Fatal("Reset yeniden silahlandirmali")
	}
}

func TestVideoRetrySucceedResets(t *testing.T) {
	var r videoRetry
	for i := 0; i < videoRetryMax-1; i++ {
		r.Fail(time.Unix(1, 0))
	}
	r.Succeed()
	if w, perm := r.Fail(time.Unix(2, 0)); perm || w != videoRetryBase {
		t.Fatalf("basaridan sonra seri sifirlanmali: %v %v", w, perm)
	}
}

func TestFillParamSets(t *testing.T) {
	au := parseAnnexBAU(annexB(true, testIDR)) // SPS/PPS akis icinde yok
	if au.SPS != nil || au.PPS != nil || !au.IDR {
		t.Fatalf("on kosul: %+v", au)
	}
	au = fillParamSets(au, annexB(true, testSPS, testPPS))
	if string(au.SPS) != string(testSPS) || string(au.PPS) != string(testPPS) {
		t.Fatalf("dizi basligindan tamamlanmali: %+v", au)
	}
	// Baslik bossa ya da AU'da zaten varsa degismez.
	same := fillParamSets(au, nil)
	if string(same.SPS) != string(testSPS) {
		t.Fatal("bos baslik bozmamali")
	}
}

// Kodlayici SPS/PPS'i akis icine koymuyor, SequenceHeader() ile veriyor.
type noInlineEnc struct {
	mu     sync.Mutex
	frames int
}

func (f *noInlineEnc) Name() string { return "noinline" }
func (f *noInlineEnc) Encode([]byte, time.Duration, bool) ([]byte, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.frames++
	if f.frames == 1 {
		return annexB(true, testIDR), nil
	}
	return annexB(true, testP), nil
}
func (f *noInlineEnc) SetBitrate(int) error { return nil }
func (f *noInlineEnc) Close()               {}
func (f *noInlineEnc) SequenceHeader() []byte {
	return annexB(true, testSPS, testPPS)
}

func TestVideoUsesSequenceHeaderWhenNotInline(t *testing.T) {
	old := newVideoEncoderFn
	newVideoEncoderFn = func(w, h, fps, bps int, preferSW bool) (videoEncoder, error) { return &noInlineEnc{}, nil }
	defer func() { newVideoEncoderFn = old }()
	col := &rawCollector{}
	cp := &noiseCap{rng: rand.New(rand.NewSource(5)), rect: image.Rect(0, 0, 192, 128)}
	s := newScreen2Session("s", 1, cp, newGoTileEncoder(), col.send, screen2Params{FPS: 30, Quality: 70, MaxWidth: 4000})
	s.vid = newVideoCtl(s)
	s.vid.SetPref(protocol.Screen2VideoOn)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- s.run(ctx) }()
	defer func() { cancel(); <-done }()
	waitCond(t, "config+chunk", 3*time.Second, func() bool {
		return col.count(isVideoKind(protocol.VideoKindConfig)) > 0 && col.count(isVideoKind(protocol.VideoKindChunk)) > 0
	})
	if s.vid.broken.Load() {
		t.Fatal("SPS/PPS dizi basligindan alinmaliydi, kodlayici bozuk sayilmamali")
	}
}

// Gecici bir kodlayici hatasi videoyu KALICI kapatmamali.
type flakyVideoEnc struct {
	fakeVideoEnc
	failN *int
}

func (f *flakyVideoEnc) Encode(nv12 []byte, ts time.Duration, force bool) ([]byte, error) {
	f.mu.Lock()
	if *f.failN > 0 {
		*f.failN--
		f.mu.Unlock()
		return nil, errors.New("gecici surucu hatasi")
	}
	f.mu.Unlock()
	return f.fakeVideoEnc.Encode(nv12, ts, force)
}

func TestVideoRecoversFromTransientEncoderError(t *testing.T) {
	failN := 1
	old := newVideoEncoderFn
	newVideoEncoderFn = func(w, h, fps, bps int, preferSW bool) (videoEncoder, error) {
		return &flakyVideoEnc{failN: &failN}, nil
	}
	defer func() { newVideoEncoderFn = old }()
	col := &rawCollector{}
	cp := &noiseCap{rng: rand.New(rand.NewSource(6)), rect: image.Rect(0, 0, 192, 128)}
	s := newScreen2Session("s", 1, cp, newGoTileEncoder(), col.send, screen2Params{FPS: 30, Quality: 70, MaxWidth: 4000})
	s.vid = newVideoCtl(s)
	s.vid.SetPref(protocol.Screen2VideoOn)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- s.run(ctx) }()
	defer func() { cancel(); <-done }()
	// Ilk hata -> karo; videoRetryBase sonra video geri gelmeli.
	waitCond(t, "hata sonrasi video geri", videoRetryBase+4*time.Second, func() bool {
		return col.count(isVideoKind(protocol.VideoKindChunk)) > 0
	})
	if s.vid.retry.Permanent() {
		t.Fatal("tek gecici hata kalici olmamali")
	}
}
