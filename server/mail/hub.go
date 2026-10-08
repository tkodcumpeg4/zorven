package mail

import (
	"strings"
	"sync"
)

// Hub, bir posta kutusunda degisiklik (yeni mail, bayrak, silme) oldugunu
// acik IMAP oturumlarina (IDLE) bildiren surec ici yayin noktasidir. Birden
// fazla replika calisiyorsa diger replikalar periyodik yoklamayla yakalar.
type Hub struct {
	mu   sync.Mutex
	subs map[string]map[chan struct{}]struct{}
}

// NewHub, bos bir Hub olusturur.
func NewHub() *Hub { return &Hub{subs: make(map[string]map[chan struct{}]struct{})} }

// DefaultHub, SMTP alicisi ile IMAP sunucusunun paylastigi varsayilan hub'dir.
var DefaultHub = NewHub()

func hubKey(tenantID, mailbox string) string {
	return tenantID + "\x00" + strings.ToLower(strings.TrimSpace(mailbox))
}

// Subscribe, kutudaki degisiklikler icin bir kanal ve iptal fonksiyonu doner.
func (h *Hub) Subscribe(tenantID, mailbox string) (<-chan struct{}, func()) {
	ch := make(chan struct{}, 1)
	key := hubKey(tenantID, mailbox)
	h.mu.Lock()
	if h.subs[key] == nil {
		h.subs[key] = make(map[chan struct{}]struct{})
	}
	h.subs[key][ch] = struct{}{}
	h.mu.Unlock()
	return ch, func() {
		h.mu.Lock()
		delete(h.subs[key], ch)
		if len(h.subs[key]) == 0 {
			delete(h.subs, key)
		}
		h.mu.Unlock()
	}
}

// Notify, kutuya abone tum oturumlari uyandirir (bloklamaz).
func (h *Hub) Notify(tenantID, mailbox string) {
	key := hubKey(tenantID, mailbox)
	h.mu.Lock()
	defer h.mu.Unlock()
	for ch := range h.subs[key] {
		select {
		case ch <- struct{}{}:
		default:
		}
	}
}
