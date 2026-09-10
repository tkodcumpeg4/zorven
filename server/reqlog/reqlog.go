// Package reqlog, tunellerden gecen son istekleri BELLEKTE tutar.
//
// Kalici degildir (api_contract.md karar 2): sunucu yeniden baslarsa gecmis
// sifirlanir. Amac canli hata ayiklama; kalici analitik R4'te Prometheus ile.
package reqlog

import (
	"fmt"
	"sync"
	"sync/atomic"
	"time"
)

// DefaultCapacity, kontratla ayni: 1000 kayit, ~200 bayt/kayit -> ~200 KB tavan.
const DefaultCapacity = 1000

// Entry, tek bir tunellenmis istegin ozeti. Alan adlari api_contract.md §1
// "RequestLog" ile birebir ayni (JSON etiketleri Nuxt tarafina gider).
type Entry struct {
	ID         string    `json:"id"`
	TunnelID   string    `json:"tunnel_id"`
	TenantID   string    `json:"tenant_id,omitempty"`
	Hostname   string    `json:"hostname,omitempty"`
	ClientIP   string    `json:"client_ip,omitempty"`
	TS         time.Time `json:"ts"`
	Method     string    `json:"method"`
	Path       string    `json:"path"`
	Status     int       `json:"status"`
	DurationMS int64     `json:"duration_ms"`
	BytesIn    int64     `json:"bytes_in"`
	BytesOut   int64     `json:"bytes_out"`
}

// Filter, kalici log sorgusu icin gelismis filtre olcutleri. Bos alanlar
// (sifir deger) o boyutta filtre uygulanmadigi anlamina gelir.
type Filter struct {
	TenantID  string    // zorunlu kapsam (bos = tum kiracilar, yalnizca platform admin)
	TunnelID  string    // belirli tunel
	Hostname  string    // belirli hostname
	Method    string    // GET/POST/...
	StatusMin int       // dahil (or. 400)
	StatusMax int       // dahil (or. 499)
	Query     string    // path icinde arama (ILIKE)
	Since     time.Time // bu andan sonra
	Until     time.Time // bu ana kadar
	MinDurMS  int64     // en az sure
	MaxDurMS  int64     // en fazla sure (0 = sinirsiz)
	Limit     int       // sayfa boyutu
	Offset    int       // sayfalama
}

// Ring, sabit kapasiteli halka tampon.
//
// Sabit kapasite bilincli: sinirsiz bir gunluk, yogun trafikte sunucu
// bellegini tuketirdi — yani gunlugun kendisi bir DoS vektoru olurdu.
type Ring struct {
	mu   sync.RWMutex
	buf  []Entry
	next int  // bir sonraki yazma konumu
	full bool // tampon en az bir kez tamamen doldu mu

	seq atomic.Uint64
}

func New(capacity int) *Ring {
	if capacity <= 0 {
		capacity = DefaultCapacity
	}
	return &Ring{buf: make([]Entry, capacity)}
}

// Add, kaydi ekler ve uretilen ID'yi doner. En eski kayit sessizce dusurulur.
func (r *Ring) Add(e Entry) Entry {
	if e.ID == "" {
		e.ID = fmt.Sprintf("req_%08x", r.seq.Add(1))
	}
	if e.TS.IsZero() {
		e.TS = time.Now().UTC()
	}

	r.mu.Lock()
	r.buf[r.next] = e
	r.next = (r.next + 1) % len(r.buf)
	if r.next == 0 {
		r.full = true
	}
	r.mu.Unlock()
	return e
}

// List, en YENIDEN eskiye dogru en fazla limit kayit doner.
// tunnelID bos degilse yalnizca o tunelin kayitlari sizilir.
func (r *Ring) List(limit int, tunnelID string) []Entry {
	r.mu.RLock()
	defer r.mu.RUnlock()

	n := len(r.buf)
	count := r.next
	if r.full {
		count = n
	}
	if limit <= 0 || limit > count {
		limit = count
	}

	out := make([]Entry, 0, limit)
	// En son yazilandan geriye dogru yuru.
	for i := 0; i < count && len(out) < limit; i++ {
		idx := (r.next - 1 - i + n) % n
		e := r.buf[idx]
		if tunnelID != "" && e.TunnelID != tunnelID {
			continue
		}
		out = append(out, e)
	}
	return out
}

// Len, tamponda tutulan kayit sayisi.
func (r *Ring) Len() int {
	r.mu.RLock()
	defer r.mu.RUnlock()
	if r.full {
		return len(r.buf)
	}
	return r.next
}
