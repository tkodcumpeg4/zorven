package tunnel

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/tkodcumpeg4/zorven/shared/protocol"
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
	// takeovers, istemci basina yakin zamandaki devralma zamanlari (ping-pong korumasi).
	takeovers map[string][]time.Time
	now       func() time.Time
}

func NewHub() *Hub {
	return &Hub{sessions: make(map[string]*Session), takeovers: make(map[string][]time.Time), now: time.Now}
}

// ErrTakeoverThrottled, ayni istemci kimligi icin devralmalar cok sik
// tekrarlandiginda (ayni anahtarla iki istemci birbirini dusuruyor) doner.
var ErrTakeoverThrottled = errors.New("cok sik devralma")

const (
	// takeoverWindow icinde en fazla takeoverMax devralmaya izin verilir; asilirsa
	// yeni baglanti 429 alir ve pencere bosalana dek bekler. Uyku/ag degisimi
	// sonrasi normal yeniden baglanma bu esige yaklasmaz.
	takeoverWindow = 60 * time.Second
	takeoverMax    = 3
)

// TakeoverBlocked, istemci icin yeni bir devralmanin simdi engellenip engellenmedigini soyler.
func (h *Hub) TakeoverBlocked(clientID string) bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	return len(h.recentTakeoversLocked(clientID)) >= takeoverMax
}

func (h *Hub) recentTakeoversLocked(clientID string) []time.Time {
	cut := h.now().Add(-takeoverWindow)
	ts := h.takeovers[clientID]
	i := 0
	for i < len(ts) && ts[i].Before(cut) {
		i++
	}
	ts = ts[i:]
	if len(ts) == 0 {
		delete(h.takeovers, clientID)
	} else {
		h.takeovers[clientID] = ts
	}
	return ts
}

// Takeover, s'yi kaydeder; ayni istemcinin eski oturumu varsa onu yerinden eder ve
// doner (cagiran eski oturumu kapatir). Uyku/ag degisimi sonrasi eski soket yari
// olu kalir; yeni dogrulanmis baglanti onun yerini almalidir (409 kilitlenmesi
// olmaz). Devralmalar takeoverWindow icinde takeoverMax'i asarsa
// ErrTakeoverThrottled doner (iki canli istemci ayni anahtarla birbirini dusurmesin).
func (h *Hub) Takeover(s *Session) (old *Session, err error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	cur, exists := h.sessions[s.ClientID]
	if exists {
		recent := h.recentTakeoversLocked(s.ClientID)
		if len(recent) >= takeoverMax {
			return nil, ErrTakeoverThrottled
		}
		h.takeovers[s.ClientID] = append(recent, h.now())
		old = cur
	}
	h.sessions[s.ClientID] = s
	return old, nil
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
//
// Kayit hala bu oturumsa true doner; devralinmis (yerine baska oturum gecmis)
// oturum false alir ve istemci-bazli temizlik (kume kaydi, cevrimdisi olayi)
// YAPMAMALIDIR.
func (h *Hub) Unregister(s *Session) bool {
	h.mu.Lock()
	defer h.mu.Unlock()

	if cur, ok := h.sessions[s.ClientID]; ok && cur == s {
		delete(h.sessions, s.ClientID)
		return true
	}
	return false
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
	Online       bool
	Version      string
	AppKind      string
	Shells       []protocol.ShellInfo
	DefaultShell string
	Platform     string
	RemoteAddr   string
	LastSeen     time.Time
}

// Statuses, tum bagli istemcilerin durumunu doner.
func (h *Hub) Statuses() map[string]Status {
	h.mu.RLock()
	defer h.mu.RUnlock()

	out := make(map[string]Status, len(h.sessions))
	for id, s := range h.sessions {
		out[id] = Status{
			Online:       true,
			Version:      s.Version,
			AppKind:      s.AppKind,
			Shells:       s.Shells,
			DefaultShell: s.DefaultShell,
			Platform:     s.Platform,
			RemoteAddr:   s.RemoteAddr,
			LastSeen:     s.LastSeen(),
		}
	}
	return out
}

// BroadcastUpdateAvailable, tum bagli istemcilere "yeni surum yayinlandi,
// manifest'i kontrol et" sinyali gonderir. Kilidi tutmadan gonderir (Send
// bloklayabilir); ulasilan istemci sayisini doner.
func (h *Hub) BroadcastUpdateAvailable(ctx context.Context, version string) int {
	h.mu.RLock()
	snapshot := make([]*Session, 0, len(h.sessions))
	for _, s := range h.sessions {
		snapshot = append(snapshot, s)
	}
	h.mu.RUnlock()

	msg := protocol.UpdateAvailable{Type: protocol.TypeUpdateAvailable, Version: version}
	sent := 0
	for _, s := range snapshot {
		if err := s.Send(ctx, msg, protocol.TypeUpdateAvailable); err == nil {
			sent++
		}
	}
	return sent
}

// Count, bagli istemci sayisi.
func (h *Hub) Count() int {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return len(h.sessions)
}
