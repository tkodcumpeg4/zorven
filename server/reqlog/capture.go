package reqlog

import (
	"sync"
	"time"
)

// FAZ 2 — İstek inspector: tam istek/yanıt (header + gövde) yakalama.
//
// Gizlilik + bellek: yakalama VARSAYILAN KAPALI ve KİRACI BAZINDA açılır
// (SetEnabled). Kapaliyken ingress hicbir tee/tampon yapmaz (sifir maliyet).
// Aciksa govde YALNIZCA MaxBody() baytina kadar tutulur; ustu "truncated" isaretlenir.
// Kayitlar bellekte sabit kapasiteli bir LRU "sicak onbellek"te tutulur; kalici
// depo (CaptureBackend, Postgres) baglanmissa sifreli+sikistirilmis olarak
// asenkron yazilir (bkz. capture_persist.go).

// DefaultCaptureMaxBody, yakalanan istek/yanit govdesi varsayilan tavani (bayt).
// ZORVEN_CAPTURE_MAX_BODY ile degistirilir (CaptureStore.MaxBody()).
const DefaultCaptureMaxBody = 256 << 10 // 256 KB

// CaptureCapacity, bellek sicak onbelleginde tutulan en fazla yakalama sayisi.
const CaptureCapacity = 500

// DefaultCaptureCacheBytes, sicak onbellegin govde bayt butcesi. 256 KB tavanla
// 500 kayit en kotu ~256 MB tutardi; butce asilinca en eski kayitlar bellekten
// dusurulur (kalici depo varsa DB'den okunur).
const DefaultCaptureCacheBytes = 32 << 20

// Capture, tek bir istegin tam yakalanmis hali.
type Capture struct {
	ID       string    `json:"id"`
	TunnelID string    `json:"tunnel_id"`
	TenantID string    `json:"tenant_id,omitempty"`
	Hostname string    `json:"hostname,omitempty"`
	ClientIP string    `json:"client_ip,omitempty"`
	TS       time.Time `json:"ts"`

	Method           string              `json:"method"`
	Path             string              `json:"path"`
	Query            string              `json:"query,omitempty"`
	ReqHeaders       map[string][]string `json:"req_headers,omitempty"`
	ReqBody          []byte              `json:"-"`
	ReqBodyTruncated bool                `json:"req_body_truncated"`

	Status            int                 `json:"status"`
	RespHeaders       map[string][]string `json:"resp_headers,omitempty"`
	RespBody          []byte              `json:"-"`
	RespBodyTruncated bool                `json:"resp_body_truncated"`
	DurationMS        int64               `json:"duration_ms"`
}

func (c *Capture) bodyBytes() int64 { return int64(len(c.ReqBody) + len(c.RespBody)) }

// CaptureStore, kiraci-bazli opt-in yakalama deposu. Bellekte sabit kapasiteli
// bir LRU "sicak onbellek" tutar; kalici bir CaptureBackend baglanmissa
// (AttachBackend) yazimlar kuyruk + batch ile DB'ye gider ve Get onbellekte
// yoksa DB'den okur.
type CaptureStore struct {
	mu       sync.RWMutex
	items    map[string]*Capture // id -> capture
	order    []string            // ekleme sirasi (LRU tahliye)
	cap      int
	bytes    int64 // onbellekteki govde bayti
	maxBytes int64
	maxBody  int
	enabled  map[string]bool // tenantID -> yakalama acik mi

	persist *capturePersist // nil: yalniz bellek
}

func NewCaptureStore(capacity int) *CaptureStore {
	if capacity <= 0 {
		capacity = CaptureCapacity
	}
	return &CaptureStore{
		items:    make(map[string]*Capture),
		order:    make([]string, 0, capacity),
		cap:      capacity,
		maxBytes: DefaultCaptureCacheBytes,
		maxBody:  DefaultCaptureMaxBody,
		enabled:  make(map[string]bool),
	}
}

// SetLimits, govde tavanini ve onbellek bayt butcesini ayarlar (<=0: degismez).
// Baslangicta, ingress trafigi baslamadan once cagrilmalidir.
func (s *CaptureStore) SetLimits(maxBody int, cacheBytes int64) {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if maxBody > 0 {
		s.maxBody = maxBody
	}
	if cacheBytes > 0 {
		s.maxBytes = cacheBytes
	}
}

// MaxBody, yakalanan govde tavani (bayt). nil depoda varsayilan.
func (s *CaptureStore) MaxBody() int {
	if s == nil {
		return DefaultCaptureMaxBody
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.maxBody
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
// mevcut yakalamalari SILINIR (gizlilik): bellekten hemen, DB'den (varsa)
// arka planda.
func (s *CaptureStore) SetEnabled(tenantID string, on bool) {
	s.mu.Lock()
	s.enabled[tenantID] = on
	if !on {
		s.dropTenantLocked(tenantID)
	}
	p := s.persist
	s.mu.Unlock()
	if !on && p != nil {
		p.deleteTenant(tenantID)
	}
}

// ForgetTenant, kiracinin bellek kayitlarini ve acik bayragini atar (kiraci
// silindiginde; DB satirlari silme islemi tarafindan zaten kaldirilmistir).
func (s *CaptureStore) ForgetTenant(tenantID string) {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.enabled, tenantID)
	s.dropTenantLocked(tenantID)
}

func (s *CaptureStore) dropTenantLocked(tenantID string) {
	for id, c := range s.items {
		if c.TenantID == tenantID {
			s.bytes -= c.bodyBytes()
			delete(s.items, id)
		}
	}
	kept := s.order[:0]
	for _, id := range s.order {
		if _, ok := s.items[id]; ok {
			kept = append(kept, id)
		}
	}
	s.order = kept
}

// Put, bir yakalamayi onbellege ekler; kapasite/bayt butcesi asilirsa en eski
// dusurulur. Kalici depo bagliysa kayit ASENKRON kuyruga da girer (bloklamaz).
func (s *CaptureStore) Put(c *Capture) {
	if s == nil || c == nil || c.ID == "" {
		return
	}
	s.mu.Lock()
	if old, exists := s.items[c.ID]; exists {
		s.bytes -= old.bodyBytes()
	} else {
		s.order = append(s.order, c.ID)
	}
	s.items[c.ID] = c
	s.bytes += c.bodyBytes()
	for len(s.order) > s.cap || (s.bytes > s.maxBytes && len(s.order) > 1) {
		oldest := s.order[0]
		s.order = s.order[1:]
		if oc, ok := s.items[oldest]; ok {
			s.bytes -= oc.bodyBytes()
			delete(s.items, oldest)
		}
	}
	p := s.persist
	s.mu.Unlock()
	if p != nil {
		p.enqueue(c)
	}
}

// Get, bir yakalamayi kiraci kapsamiyla doner (baska kiracininki gorulemez).
// Once sicak onbellek, yoksa kalici depo.
func (s *CaptureStore) Get(tenantID, id string) (*Capture, bool) {
	if s == nil {
		return nil, false
	}
	s.mu.RLock()
	c, ok := s.items[id]
	p := s.persist
	s.mu.RUnlock()
	if ok {
		if tenantID != "" && c.TenantID != tenantID {
			return nil, false
		}
		return c, true
	}
	if p == nil {
		return nil, false
	}
	return p.get(tenantID, id)
}
