package agent

import (
	"context"
	"sync"

	"github.com/tkodcumpeg4/zorven/shared/protocol"
)

// windowTable, istek basina gonderim kredisini tutar.
//
// NEDEN: sunucu, govde cercevelerini TEK bir oturum okuma dongusunde isler.
// Kredi olmadan, yavas bir tuketici o donguyu bloklar ve ayni istemcideki tum
// diger istekler durur (head-of-line blocking). Kredi ile, penceresi biten
// akis bekler; her istek KENDI goroutine'inde aktigi icin (proxy.go
// handleRequest) diger akislar etkilenmez.
type windowTable struct {
	mu sync.Mutex
	// remaining, req_id -> kalan kredi. Kayit yoksa baslangic penceresi gecerli.
	remaining map[uint64]int
	// waiters, kredi gelince uyandirilacak akislar (req_id basina bir kanal).
	waiters map[uint64]chan struct{}
}

func newWindowTable() *windowTable {
	return &windowTable{
		remaining: make(map[uint64]int),
		waiters:   make(map[uint64]chan struct{}),
	}
}

// acquire, n bayt gondermek icin kredi ayirir; yeterli kredi yoksa gelene
// kadar bekler. ctx iptal edilirse hata doner (goroutine sizmaz).
func (w *windowTable) acquire(ctx context.Context, reqID uint64, n int) error {
	for {
		w.mu.Lock()
		rem, ok := w.remaining[reqID]
		if !ok {
			rem = protocol.InitialWindowBytes
		}
		if rem >= n {
			w.remaining[reqID] = rem - n
			w.mu.Unlock()
			return nil
		}
		ch, ok := w.waiters[reqID]
		if !ok {
			ch = make(chan struct{})
			w.waiters[reqID] = ch
		}
		w.mu.Unlock()

		select {
		case <-ch:
			// Kredi geldi; dongude yeniden dene (baska bir bekleyen krediyi
			// kapmis olabilir, o yuzden kosulu yeniden kontrol ediyoruz).
		case <-ctx.Done():
			return ctx.Err()
		}
	}
}

// add, sunucudan gelen krediyi ekler ve bekleyenleri uyandirir.
func (w *windowTable) add(reqID uint64, n int) {
	if n <= 0 {
		return
	}
	w.mu.Lock()
	rem, ok := w.remaining[reqID]
	if !ok {
		rem = protocol.InitialWindowBytes
	}
	w.remaining[reqID] = rem + n
	ch := w.waiters[reqID]
	delete(w.waiters, reqID)
	w.mu.Unlock()

	if ch != nil {
		close(ch) // bekleyenlerin HEPSINI uyandir
	}
}

// release, istek bitince kaydi temizler (sizinti onlemi) ve varsa bekleyeni
// serbest birakir.
func (w *windowTable) release(reqID uint64) {
	w.mu.Lock()
	delete(w.remaining, reqID)
	ch := w.waiters[reqID]
	delete(w.waiters, reqID)
	w.mu.Unlock()

	if ch != nil {
		close(ch)
	}
}
