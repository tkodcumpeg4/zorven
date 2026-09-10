package ratelimit

import (
	"sync"
	"testing"
	"time"
)

func TestAllowRespectsBurst(t *testing.T) {
	// Saniyede 1, burst 3: ilk 3 gecmeli, 4. reddedilmeli.
	l := New(1, 3)
	defer l.Close()

	for i := range 3 {
		if !l.Allow("ip-1") {
			t.Fatalf("%d. istek gecmeliydi (burst=3)", i+1)
		}
	}
	if l.Allow("ip-1") {
		t.Error("burst tukendikten sonra istek reddedilmeliydi")
	}
}

// Anahtarlar birbirini etkilememeli: bir IP'nin kotayi doldurmasi
// digerlerini kilitlememelidir.
func TestKeysAreIndependent(t *testing.T) {
	l := New(1, 2)
	defer l.Close()

	l.Allow("ip-1")
	l.Allow("ip-1")
	if l.Allow("ip-1") {
		t.Fatal("ip-1 kotasi dolmus olmaliydi")
	}
	if !l.Allow("ip-2") {
		t.Error("ip-2 ip-1'den etkilenmemeliydi")
	}
}

func TestRefillOverTime(t *testing.T) {
	// Saniyede 50 -> jeton ~20ms'de bir dolar.
	l := New(50, 1)
	defer l.Close()

	if !l.Allow("k") {
		t.Fatal("ilk istek gecmeliydi")
	}
	if l.Allow("k") {
		t.Fatal("hemen ardindan gelen istek reddedilmeliydi")
	}

	time.Sleep(60 * time.Millisecond)
	if !l.Allow("k") {
		t.Error("jeton dolduktan sonra istek gecmeliydi")
	}
}

// Kullanilmayan kovalar dusurulmeli; aksi halde rastgele anahtarlarla
// (sahte IP'lerle) bellek sisirilebilirdi.
func TestStaleBucketsAreEvicted(t *testing.T) {
	l := &Limiter{
		limit:   10,
		burst:   10,
		ttl:     40 * time.Millisecond,
		buckets: make(map[string]*bucket),
		stop:    make(chan struct{}),
	}
	go l.janitor()
	defer l.Close()

	for _, k := range []string{"a", "b", "c"} {
		l.Allow(k)
	}
	if got := l.Len(); got != 3 {
		t.Fatalf("Len() = %d, beklenen 3", got)
	}

	// TTL'den uzun bekle; temizlik en gec ttl/2'de bir calisir.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if l.Len() == 0 {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Errorf("bayat kovalar dusurulmedi, Len() = %d", l.Len())
}

func TestConcurrentAccessIsSafe(t *testing.T) {
	l := New(1000, 1000)
	defer l.Close()

	var wg sync.WaitGroup
	for i := range 50 {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			for range 20 {
				l.Allow("paylasilan")
				l.Allow(string(rune('a' + n%26)))
			}
		}(i)
	}
	wg.Wait() // -race ile calistirildiginda veri yarisi yakalanir
}

func TestCloseIsIdempotent(t *testing.T) {
	l := New(1, 1)
	l.Close()
	l.Close() // panic etmemeli
}

func TestRetryAfterIsAtLeastOneSecond(t *testing.T) {
	if got := New(100, 1).RetryAfter(); got < time.Second {
		t.Errorf("RetryAfter() = %v, en az 1sn olmaliydi", got)
	}
	if got := New(0, 1).RetryAfter(); got < time.Second {
		t.Errorf("sifir hizda RetryAfter() = %v, en az 1sn olmaliydi", got)
	}
}
