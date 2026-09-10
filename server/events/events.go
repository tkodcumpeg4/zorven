// Package events, dashboard'a SSE ile gonderilen canli olaylari dagitir.
package events

import (
	"sync"
	"time"
)

// Olay tipleri — api_contract.md §1 ile ayni.
const (
	TypeClientConnected    = "client.connected"
	TypeClientDisconnected = "client.disconnected"
	TypeTunnelCreated      = "tunnel.created"
	TypeTunnelUpdated      = "tunnel.updated"
	TypeTunnelDeleted      = "tunnel.deleted"
	TypeRequestCompleted   = "request.completed"
)

// Event, tek bir SSE olayi.
type Event struct {
	Type string
	Data any
}

// bufferPerSubscriber, abone basina tamponlanacak olay sayisi.
const bufferPerSubscriber = 64

// Broker, olaylari bagli tum dashboard'lara dagitir.
type Broker struct {
	mu   sync.RWMutex
	subs map[chan Event]struct{}

	dropped uint64 // yavas abone yuzunden dusurulen olay sayisi
}

func New() *Broker {
	return &Broker{subs: make(map[chan Event]struct{})}
}

// Publish, olayi tum abonelere gonderir.
//
// GONDERIM BLOKLAMAZ: bir abone (or. ag'i yavas bir tarayici) okumakta
// geride kalirsa olay DUSURULUR. Aksi halde ingress sicak yolu bir
// tarayicinin hizina baglanirdi — tek yavas dashboard tum tuneli yavaslatirdi.
func (b *Broker) Publish(typ string, data any) {
	e := Event{Type: typ, Data: data}

	b.mu.RLock()
	defer b.mu.RUnlock()

	for ch := range b.subs {
		select {
		case ch <- e:
		default:
			b.dropped++
		}
	}
}

// Subscribe, yeni bir abone kanali ve onu kapatan fonksiyon doner.
// Cagiran, is bitince donen fonksiyonu MUTLAKA cagirmalidir.
func (b *Broker) Subscribe() (<-chan Event, func()) {
	ch := make(chan Event, bufferPerSubscriber)

	b.mu.Lock()
	b.subs[ch] = struct{}{}
	b.mu.Unlock()

	var once sync.Once
	return ch, func() {
		once.Do(func() {
			b.mu.Lock()
			delete(b.subs, ch)
			b.mu.Unlock()
			close(ch)
		})
	}
}

// Count, bagli abone sayisi.
func (b *Broker) Count() int {
	b.mu.RLock()
	defer b.mu.RUnlock()
	return len(b.subs)
}

// Dropped, yavas abone yuzunden dusurulen olay sayisi (teshis icin).
func (b *Broker) Dropped() uint64 {
	b.mu.RLock()
	defer b.mu.RUnlock()
	return b.dropped
}

// Heartbeat araligi: SSE baglantilarini ara proxy'ler bosta kapatmasin diye
// periyodik yorum satiri gonderilir.
const HeartbeatInterval = 25 * time.Second
