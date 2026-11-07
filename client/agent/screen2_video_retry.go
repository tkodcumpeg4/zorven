package agent

import (
	"sync"
	"time"
)

// Video kodlayici hata politikasi.
//
// Tek bir gecici kodlayici hatasi (surucu sifirlama, kisa bellek baskisi,
// UAC/masaustu degisimi) videoyu OTURUM BOYUNCA kalici olarak kapatmamali.
// Ussel geri cekilmeyle birkac kez yeniden denenir; videoRetryMax ardisik
// basarisizliktan sonra karo modunda kalinir (kullanici modu degistirene kadar).

const (
	videoRetryMax  = 5
	videoRetryBase = 2 * time.Second
	videoRetryCap  = 30 * time.Second
)

type videoRetry struct {
	mu    sync.Mutex
	fails int
	until time.Time
	perm  bool
}

// Fail, bir kodlayici hatasini kaydeder. wait: yeniden denemeye kadar sure;
// perm: vazgecildi (karo modunda kal).
func (r *videoRetry) Fail(now time.Time) (wait time.Duration, perm bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.fails++
	if r.fails >= videoRetryMax {
		r.perm = true
		return 0, true
	}
	wait = videoRetryBase
	for i := 1; i < r.fails && wait < videoRetryCap; i++ {
		wait *= 2
	}
	wait = min(wait, videoRetryCap)
	r.until = now.Add(wait)
	return wait, false
}

// Ready, kodlayiciya yeniden denenebilecek mi (kalici degil ve bekleme bitti).
func (r *videoRetry) Ready(now time.Time) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return !r.perm && !now.Before(r.until)
}

// Succeed, basarili bir kare gonderildiginde hata serisini sifirlar.
func (r *videoRetry) Succeed() {
	r.mu.Lock()
	r.fails = 0
	r.mu.Unlock()
}

// Reset, kullanici modu degistirince politikayi yeniden silahlandirir.
func (r *videoRetry) Reset() {
	r.mu.Lock()
	r.fails, r.perm, r.until = 0, false, time.Time{}
	r.mu.Unlock()
}

// Permanent, vazgecilip vazgecilmedigi.
func (r *videoRetry) Permanent() bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.perm
}
