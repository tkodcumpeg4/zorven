package ingress

import (
	"sync"
	"sync/atomic"

	"github.com/tkodcumpeg4/zorven/server/store"
)

// FAZ 5 / HA — bir hostname birden çok istemci agent tarafından servis
// edilebilir. Ingress, çevrimiçi üyeler arasında round-robin dağıtır ve
// çevrimdışı olanı atlar (failover). Tek üyeli (replikasız) tüneller tam olarak
// eskisi gibi çalışır.

// roundRobin, anahtar (hostname) başına artan bir sayaç tutar. Zero-value
// kullanıma hazırdır (sync.Map). Handler tek sefer kurulur ve pointer ile
// kullanılır, bu yüzden değer alanı olarak gömülmesi güvenlidir.
type roundRobin struct {
	ctr sync.Map // key string -> *atomic.Uint64
}

// next, anahtar için bir sonraki sayacı döner (ilk çağrıda 0). Round-robin
// başlangıç ofseti olarak kullanılır.
func (rr *roundRobin) next(key string) uint64 {
	v, _ := rr.ctr.LoadOrStore(key, new(atomic.Uint64))
	return v.(*atomic.Uint64).Add(1) - 1
}

// candidateClients, tünelin tüm aday servis-eden istemcilerini döner:
// birincil ClientID + replikalar (tekrarları ayıklanmış, sıralı).
func candidateClients(tun store.HostRoute) []string {
	out := make([]string, 0, 1+len(tun.ReplicaClientIDs))
	seen := make(map[string]struct{}, 1+len(tun.ReplicaClientIDs))
	for _, c := range append([]string{tun.ClientID}, tun.ReplicaClientIDs...) {
		if c == "" {
			continue
		}
		if _, dup := seen[c]; dup {
			continue
		}
		seen[c] = struct{}{}
		out = append(out, c)
	}
	return out
}

// orderOnline, adayları `start` ofsetinden itibaren döndürerek (round-robin) ve
// yalnızca isOnline(c)==true olanları koruyarak sıralar. İlk eleman seçilecek
// backend'dir; kalanlar failover sırasıdır. Hiç çevrimiçi yoksa nil döner.
func orderOnline(candidates []string, start uint64, isOnline func(string) bool) []string {
	n := len(candidates)
	if n == 0 {
		return nil
	}
	var out []string
	for i := 0; i < n; i++ {
		c := candidates[(int(start%uint64(n))+i)%n]
		if isOnline(c) {
			out = append(out, c)
		}
	}
	return out
}
