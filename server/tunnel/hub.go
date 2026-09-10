package tunnel

import (
	"errors"
	"sync"
	"time"
)

// ErrAlreadyConnected, ayni istemci ikinci kez baglanmaya calistiginda doner.
// api_contract.md §2: bu durumda WSS upgrade 409 ile reddedilir.
var ErrAlreadyConnected = errors.New("istemci zaten bagli")

// Hub, canli istemci oturumlarinin kaydini tutar.
// Bu kayit BELLEKTE yasar — kalici degildir; sunucu yeniden baslarsa
// istemciler yeniden baglanir (client tarafi backoff ile dener).
type Hub struct {
	mu       sync.RWMutex
	sessions map[string]*Session // clientID -> session
}

func NewHub() *Hub {
	return &Hub{sessions: make(map[string]*Session)}
}

// Register, oturumu kaydeder. Ayni clientID zaten bagliysa ErrAlreadyConnected doner.
func (h *Hub) Register(s *Session) error {
	h.mu.Lock()
	defer h.mu.Unlock()

	if _, exists := h.sessions[s.ClientID]; exists {
		return ErrAlreadyConnected
	}
	h.sessions[s.ClientID] = s
	return nil
}

// Unregister, oturumu kayittan dusurur.
// Yalnizca verilen oturum hala kayitliysa siler — boylece gec kalan bir
// Unregister, ayni istemcinin yeni oturumunu yanlislikla dusurmez.
func (h *Hub) Unregister(s *Session) {
	h.mu.Lock()
	defer h.mu.Unlock()

	if cur, ok := h.sessions[s.ClientID]; ok && cur.ID == s.ID {
		delete(h.sessions, s.ClientID)
	}
}

// Get, istemcinin canli oturumunu doner.
func (h *Hub) Get(clientID string) (*Session, bool) {
	h.mu.RLock()
	defer h.mu.RUnlock()
	s, ok := h.sessions[clientID]
	return s, ok
}

// Status, istemcinin canli durumunu ozetler. API katmani DB kaydini
// bu bilgiyle zenginlestirir (status/version/last_seen DB'de tutulmaz).
type Status struct {
	Online     bool
	Version    string
	Platform   string
	RemoteAddr string
	LastSeen   time.Time
}

// Statuses, tum bagli istemcilerin durumunu doner.
func (h *Hub) Statuses() map[string]Status {
	h.mu.RLock()
	defer h.mu.RUnlock()

	out := make(map[string]Status, len(h.sessions))
	for id, s := range h.sessions {
		out[id] = Status{
			Online:     true,
			Version:    s.Version,
			Platform:   s.Platform,
			RemoteAddr: s.RemoteAddr,
			LastSeen:   s.LastSeen(),
		}
	}
	return out
}

// Count, bagli istemci sayisi.
func (h *Hub) Count() int {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return len(h.sessions)
}
