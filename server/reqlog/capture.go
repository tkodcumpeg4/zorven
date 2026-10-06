package reqlog

import (
	"sync"
	"time"
)

// FAZ 2 — İstek inspector: tam istek/yanıt (header + gövde) yakalama.
//
// Gizlilik + bellek: yakalama VARSAYILAN KAPALI ve KİRACI BAZINDA açılır
// (SetEnabled). Kapaliyken ingress hicbir tee/tampon yapmaz (sifir maliyet).
// Aciksa govde YALNIZCA MaxBodyBytes'a kadar tutulur; ustu "truncated" isaretlenir.
// Kayitlar sabit kapasiteli bir LRU'da tutulur (kalici degil).

// CaptureMaxBody, yakalanan istek/yanit govdesi tavani (bayt).
const CaptureMaxBody = 64 << 10 // 64 KB

// CaptureCapacity, bellekte tutulan en fazla yakalama sayisi.
const CaptureCapacity = 500

// Capture, tek bir istegin tam yakalanmis hali.
type Capture struct {
	ID       string    `json:"id"`
	TunnelID string    `json:"tunnel_id"`
	TenantID string    `json:"tenant_id,omitempty"`
	Hostname string    `json:"hostname,omitempty"`
	ClientIP string    `json:"client_ip,omitempty"`
	TS       time.Time `json:"ts"`

	Method string              `json:"method"`
	Path   string              `json:"path"`
	Query  string              `json:"query,omitempty"`
	ReqHeaders map[string][]string `json:"req_headers,omitempty"`
	ReqBody    []byte              `json:"-"`
	ReqBodyTruncated bool          `json:"req_body_truncated"`

	Status      int                 `json:"status"`
	RespHeaders map[string][]string `json:"resp_headers,omitempty"`
	RespBody    []byte              `json:"-"`
	RespBodyTruncated bool          `json:"resp_body_truncated"`
	DurationMS  int64               `json:"duration_ms"`
}

// CaptureStore, kiraci-bazli opt-in yakalama deposu (LRU, sabit kapasite).
type CaptureStore struct {
	mu      sync.RWMutex
	items   map[string]*Capture // id -> capture
	order   []string            // ekleme sirasi (LRU tahliye)
	cap     int
	enabled map[string]bool // tenantID -> yakalama acik mi
}

func NewCaptureStore(capacity int) *CaptureStore {
	if capacity <= 0 {
		capacity = CaptureCapacity
	}
	return &CaptureStore{
		items:   make(map[string]*Capture),
		order:   make([]string, 0, capacity),
		cap:     capacity,
		enabled: make(map[string]bool),
	}
}

// Enabled, verilen kiraci icin yakalama acik mi.
func (s *CaptureStore) Enabled(tenantID string) bool {
	if s == nil {
		return false
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.enabled[tenantID]
}

// SetEnabled, kiraci icin yakalamayi acar/kapatir. Kapatinca o kiracinin
// mevcut yakalamalari SILINIR (gizlilik).
func (s *CaptureStore) SetEnabled(tenantID string, on bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.enabled[tenantID] = on
	if !on {
		for id, c := range s.items {
			if c.TenantID == tenantID {
				delete(s.items, id)
			}
		}
		// order'i sadelestir (silinenleri at)
		kept := s.order[:0]
		for _, id := range s.order {
			if _, ok := s.items[id]; ok {
				kept = append(kept, id)
			}
		}
		s.order = kept
	}
}

// Put, bir yakalamayi ekler; kapasite asilirsa en eski dusurulur.
func (s *CaptureStore) Put(c *Capture) {
	if s == nil || c == nil || c.ID == "" {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, exists := s.items[c.ID]; !exists {
		s.order = append(s.order, c.ID)
	}
	s.items[c.ID] = c
	for len(s.order) > s.cap {
		oldest := s.order[0]
		s.order = s.order[1:]
		delete(s.items, oldest)
	}
}

// Get, bir yakalamayi kiraci kapsamiyla doner (baska kiracininki gorulemez).
func (s *CaptureStore) Get(tenantID, id string) (*Capture, bool) {
	if s == nil {
		return nil, false
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	c, ok := s.items[id]
	if !ok || (tenantID != "" && c.TenantID != tenantID) {
		return nil, false
	}
	return c, true
}
