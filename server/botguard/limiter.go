package botguard

import (
	"hash/fnv"
	"sync"
	"time"
)

const (
	limiterShards = 64
	// DefaultLimiterCap, tum parcalar toplami en fazla bucket sayisi.
	DefaultLimiterCap = 100_000
)

type bucket struct {
	tokens float64
	last   time.Time
}

type limShard struct {
	mu sync.Mutex
	m  map[string]*bucket
}

// Limiter, anahtar (tunel+IP onegi) basina token bucket hiz sinirlayicisidir.
// 64 parcaya bolunmustur (tek global kilit yok). Bellek sinirlidir: bir parca
// dolunca once dolmus (bosta kalmis) kovalar silinir, hala doluysa en eski
// gorulen kova atilir.
type Limiter struct {
	shards   [limiterShards]limShard
	perShard int
	now      func() time.Time
}

// NewLimiter, toplam en fazla max kova tutan sinirlayici.
func NewLimiter(max int) *Limiter {
	if max <= 0 {
		max = DefaultLimiterCap
	}
	l := &Limiter{perShard: (max + limiterShards - 1) / limiterShards, now: time.Now}
	for i := range l.shards {
		l.shards[i].m = make(map[string]*bucket)
	}
	return l
}

// SetClock, testler icin saat.
func (l *Limiter) SetClock(f func() time.Time) { l.now = f }

// Allow, anahtar icin bir istek hakki harcamayi dener. Kapasite = requests
// (patlama), dolum hizi = requests/window. mult>1 kapasiteyi ve hizi carpar
// (cerez sahibi icin 3). Reddedilirse bir sonraki hakkin gelecegi sure doner.
func (l *Limiter) Allow(key string, requests int, window time.Duration, mult int) (bool, time.Duration) {
	if mult < 1 {
		mult = 1
	}
	capTok := float64(requests * mult)
	rate := capTok / window.Seconds() // token / sn
	now := l.now()
	h := fnv.New32a()
	h.Write([]byte(key))
	s := &l.shards[h.Sum32()%limiterShards]
	s.mu.Lock()
	defer s.mu.Unlock()
	b, ok := s.m[key]
	if !ok {
		if len(s.m) >= l.perShard {
			l.evict(s, now)
		}
		b = &bucket{tokens: capTok, last: now}
		s.m[key] = b
	} else {
		el := now.Sub(b.last).Seconds()
		if el > 0 {
			b.tokens += el * rate
			if b.tokens > capTok {
				b.tokens = capTok
			}
			b.last = now
		}
	}
	if b.tokens >= 1 {
		b.tokens--
		return true, 0
	}
	wait := time.Duration((1 - b.tokens) / rate * float64(time.Second))
	if wait < time.Second {
		wait = time.Second
	}
	return false, wait
}

// evict (kilit tutulurken): once 1 sn'den eski ve dolmus gibi duranlar,
// yoksa en eski gorulen.
func (l *Limiter) evict(s *limShard, now time.Time) {
	var oldestK string
	var oldestT time.Time
	first := true
	for k, b := range s.m {
		// 10 dk hareketsiz kova artik dolmustur: guvenle silinebilir.
		if now.Sub(b.last) > 10*time.Minute {
			delete(s.m, k)
			continue
		}
		if first || b.last.Before(oldestT) {
			oldestK, oldestT, first = k, b.last, false
		}
	}
	if len(s.m) >= l.perShard && !first {
		delete(s.m, oldestK)
	}
}

// Len, toplam kova sayisi (test/izleme).
func (l *Limiter) Len() int {
	n := 0
	for i := range l.shards {
		l.shards[i].mu.Lock()
		n += len(l.shards[i].m)
		l.shards[i].mu.Unlock()
	}
	return n
}
