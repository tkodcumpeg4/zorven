// Package ratelimit, anahtar basina token-bucket hiz sinirlamasi saglar.
//
// Iki yerde kullanilir:
//   - Kimlik dogrulama denemeleri (anahtar: istemci IP'si)
//   - Tunellenen istekler (anahtar: tunel ID'si)
package ratelimit

import (
	"sync"
	"time"

	"golang.org/x/time/rate"
)

// defaultTTL, bir kova bu sure boyunca kullanilmazsa dusurulur.
const defaultTTL = 10 * time.Minute

// Limiter, anahtar basina ayri bir token-bucket tutar.
//
// Kovalar sonsuza kadar birikmez: her benzersiz anahtar bellek tuketir ve
// saldirgan rastgele anahtarlarla (or. sahte IP'lerle) bellegi sisirebilirdi.
// Bu yuzden bir temizlik goroutine'i kullanilmayan kovalari duserur.
type Limiter struct {
	limit rate.Limit
	burst int
	ttl   time.Duration

	mu      sync.Mutex
	buckets map[string]*bucket

	stop chan struct{}
	once sync.Once
}

type bucket struct {
	lim      *rate.Limiter
	lastSeen time.Time
}

// New, saniyede perSecond islem ve burst kapasiteli bir sinirlayici olusturur.
// Donen sinirlayici bir temizlik goroutine'i baslatir; Close ile durdurulur.
func New(perSecond float64, burst int) *Limiter {
	l := &Limiter{
		limit:   rate.Limit(perSecond),
		burst:   burst,
		ttl:     defaultTTL,
		buckets: make(map[string]*bucket),
		stop:    make(chan struct{}),
	}
	go l.janitor()
	return l
}

// Allow, anahtarin kotasi varsa true doner ve bir jeton harcar.
func (l *Limiter) Allow(key string) bool {
	l.mu.Lock()
	b, ok := l.buckets[key]
	if !ok {
		b = &bucket{lim: rate.NewLimiter(l.limit, l.burst)}
		l.buckets[key] = b
	}
	b.lastSeen = time.Now()
	l.mu.Unlock()

	return b.lim.Allow()
}

// RetryAfter, kotasi biten bir anahtarin ne kadar beklemesi gerektigini
// kabaca soyler. 429 yanitindaki Retry-After basligi icin kullanilir.
func (l *Limiter) RetryAfter() time.Duration {
	if l.limit <= 0 {
		return time.Second
	}
	d := time.Duration(float64(time.Second) / float64(l.limit))
	if d < time.Second {
		return time.Second
	}
	return d
}

// Burst, kova kapasitesi (X-RateLimit-Limit basligi icin).
func (l *Limiter) Burst() int { return l.burst }

// Remaining, anahtarin su an kalan yaklasik jeton sayisi (X-RateLimit-Remaining).
// Anahtar hic gorulmediyse burst (dolu kova) doner. Jeton harcamaz.
func (l *Limiter) Remaining(key string) int {
	l.mu.Lock()
	b, ok := l.buckets[key]
	l.mu.Unlock()
	if !ok {
		return l.burst
	}
	n := int(b.lim.Tokens())
	if n < 0 {
		return 0
	}
	return n
}

// Len, izlenen anahtar sayisi (teshis/test icin).
func (l *Limiter) Len() int {
	l.mu.Lock()
	defer l.mu.Unlock()
	return len(l.buckets)
}

// Close, temizlik goroutine'ini durdurur. Birden fazla cagri guvenlidir.
func (l *Limiter) Close() {
	l.once.Do(func() { close(l.stop) })
}

func (l *Limiter) janitor() {
	ticker := time.NewTicker(l.ttl / 2)
	defer ticker.Stop()

	for {
		select {
		case <-l.stop:
			return
		case now := <-ticker.C:
			l.mu.Lock()
			for k, b := range l.buckets {
				if now.Sub(b.lastSeen) > l.ttl {
					delete(l.buckets, k)
				}
			}
			l.mu.Unlock()
		}
	}
}
