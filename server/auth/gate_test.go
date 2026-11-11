package auth

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// withGate, testin suresince kapiyi verilen boyut/bekleme ile kurar.
func withGate(t *testing.T, size int, wait time.Duration) {
	t.Helper()
	gateOnce.Do(initGate)
	oldG, oldW := gate, gateWait
	configureGate(size, wait)
	t.Cleanup(func() { gate, gateWait = oldG, oldW })
}

// Kapi doluyken bekleme suresi asilinca ErrBusy donmeli; argon2 calismamali.
func TestVerifyBusyWhenGateFull(t *testing.T) {
	_, _, hash, err := GenerateAPIKey()
	if err != nil {
		t.Fatal(err)
	}
	withGate(t, 1, 50*time.Millisecond)
	gate <- struct{}{} // tek yeri baska bir "dogrulama" tutuyor
	defer func() { <-gate }()

	start := time.Now()
	_, err = Verify("x", hash)
	if !errors.Is(err, ErrBusy) {
		t.Fatalf("hata %v, beklenen ErrBusy", err)
	}
	if d := time.Since(start); d > time.Second {
		t.Fatalf("bekleme %v, ust sinir ~50ms olmaliydi", d)
	}
	if err := VerifyDummy("x"); !errors.Is(err, ErrBusy) {
		t.Fatalf("VerifyDummy hata %v, beklenen ErrBusy", err)
	}
}

// Iptal edilen istek kuyrukta beklemeyi hemen birakmali.
func TestVerifyContextCanceledLeavesQueue(t *testing.T) {
	_, _, hash, _ := GenerateAPIKey()
	withGate(t, 1, time.Hour)
	gate <- struct{}{}
	defer func() { <-gate }()
	ctx, cancel := context.WithCancel(context.Background())
	go func() { time.Sleep(30 * time.Millisecond); cancel() }()
	if _, err := VerifyContext(ctx, "x", hash); !errors.Is(err, ErrBusy) || !errors.Is(err, context.Canceled) {
		t.Fatalf("hata %v, beklenen ErrBusy+Canceled", err)
	}
}

// Eszamanli dogrulama sayisi kapasiteyi asmamali (bellek siniri = kapasite x 32 MB).
func TestVerifyConcurrencyBounded(t *testing.T) {
	_, _, hash, _ := GenerateAPIKey()
	withGate(t, 2, 10*time.Second)
	var peak atomic.Int64
	stop := make(chan struct{})
	go func() {
		for {
			select {
			case <-stop:
				return
			default:
			}
			if n := inflight.Load(); n > peak.Load() {
				peak.Store(n)
			}
		}
	}()
	var wg sync.WaitGroup
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := Verify("yanlis", hash); err != nil {
				t.Errorf("Verify: %v", err)
			}
		}()
	}
	wg.Wait()
	close(stop)
	if p := peak.Load(); p > 2 {
		t.Fatalf("tepe eszamanli argon2 = %d, kapasite 2", p)
	}
	if inflight.Load() != 0 {
		t.Fatalf("inflight sifira donmedi: %d", inflight.Load())
	}
}

func TestGateSizeFromEnv(t *testing.T) {
	env := func(m map[string]string) func(string) string { return func(k string) string { return m[k] } }
	if n := gateSizeFromEnv(env(map[string]string{envGateSize: "7"})); n != 7 {
		t.Fatalf("env 7 -> %d", n)
	}
	for _, bad := range []string{"", "0", "-3", "abc"} {
		if n := gateSizeFromEnv(env(map[string]string{envGateSize: bad})); n < 2 {
			t.Fatalf("gecersiz %q -> %d, en az 2 olmali", bad, n)
		}
	}
	if d := gateWaitFromEnv(env(map[string]string{envGateWait: "500ms"})); d != 500*time.Millisecond {
		t.Fatalf("bekleme %v", d)
	}
	if d := gateWaitFromEnv(env(map[string]string{envGateWait: "x"})); d != defaultGateWait {
		t.Fatalf("gecersiz bekleme -> %v", d)
	}
}

// Onbellek isabeti argon2 calistirmaz: kapi tamamen doluyken bile gecerli
// sir (yakinda dogrulanmis) kabul edilir.
func TestVerifyCachedHitSkipsArgon2(t *testing.T) {
	full, id, hash, _ := GenerateAPIKey()
	_, secret, _ := Split(full)
	scope := "api:" + id
	if ok, err := VerifyCached(context.Background(), scope, secret, hash); err != nil || !ok {
		t.Fatalf("ilk dogrulama ok=%v err=%v", ok, err)
	}
	withGate(t, 1, 20*time.Millisecond)
	gate <- struct{}{}
	defer func() { <-gate }()
	if ok, err := VerifyCached(context.Background(), scope, secret, hash); err != nil || !ok {
		t.Fatalf("onbellek isabeti ok=%v err=%v (argon2 calismamaliydi)", ok, err)
	}
	// Yanlis sir onbellekten GECMEZ: argon2 gerekir, kapi dolu -> ErrBusy.
	if _, err := VerifyCached(context.Background(), scope, secret+"x", hash); !errors.Is(err, ErrBusy) {
		t.Fatalf("yanlis sir: %v, beklenen ErrBusy (onbellek atlanmamali)", err)
	}
}

// Hash degisirse (token yenilendi) eski sir onbellekten kabul edilmemeli;
// basarisiz sonuc onbelleklenmemeli; TTL dolunca kayit dusmeli.
func TestVerifyCachedInvalidation(t *testing.T) {
	full, id, hash, _ := GenerateAPIKey()
	_, secret, _ := Split(full)
	_, _, hash2, _ := GenerateAPIKey()
	ctx := context.Background()
	if ok, _ := VerifyCached(ctx, "api:"+id, secret, hash); !ok {
		t.Fatal("ilk dogrulama basarisiz")
	}
	if ok, _ := VerifyCached(ctx, "api:"+id, secret, hash2); ok {
		t.Fatal("hash degisti, eski sir kabul edilmemeliydi")
	}
	if ok, _ := VerifyCached(ctx, "api:"+id, "yanlis", hash); ok {
		t.Fatal("yanlis sir kabul edildi")
	}
	k, _ := okCache.digest("api:"+id, "yanlis", hash)
	if okCache.hit(k) {
		t.Fatal("basarisiz sonuc onbelleklenmis")
	}
	// Farkli kapsam ayni sir/hash ile karismamali.
	k2, _ := okCache.digest("basic:"+id, secret, hash)
	if okCache.hit(k2) {
		t.Fatal("kapsamlar karisti")
	}

	c := newVerifiedCache()
	now := time.Unix(1000, 0)
	c.now = func() time.Time { return now }
	kk, _ := c.digest("s", "x", "h")
	c.put(kk)
	if !c.hit(kk) {
		t.Fatal("taze kayit bulunamadi")
	}
	now = now.Add(verifiedTTL)
	if c.hit(kk) {
		t.Fatal("TTL dolan kayit hala gecerli")
	}
}

func TestVerifiedCacheBounded(t *testing.T) {
	c := newVerifiedCache()
	for i := range verifiedMax + 100 {
		k, _ := c.digest("s", string(rune(i)), "h")
		c.put(k)
	}
	if n := c.len(); n > verifiedMax {
		t.Fatalf("onbellek %d kayit, ust sinir %d", n, verifiedMax)
	}
}

func TestLogSampler(t *testing.T) {
	now := time.Unix(0, 0)
	s := &logSampler{window: 10 * time.Second, limit: 2, now: func() time.Time { return now }}
	for i := range 2 {
		if ok, _ := s.allow(); !ok {
			t.Fatalf("%d. satir izinli olmaliydi", i)
		}
	}
	for range 5 {
		if ok, _ := s.allow(); ok {
			t.Fatal("pencere siniri asildi")
		}
	}
	now = now.Add(10 * time.Second)
	ok, sup := s.allow()
	if !ok || sup != 5 {
		t.Fatalf("yeni pencere ok=%v bastirilan=%d, beklenen 5", ok, sup)
	}
}
