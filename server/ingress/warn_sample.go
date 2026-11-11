package ingress

import (
	"sync"
	"time"
)

// warnSampler, ret basina WARN satirini (IP izin listesi, WAF, webhook imzasi,
// hiz siniri) tunel+neden basina saniyede en fazla BIR satira indirir; arada
// bastirilan olaylar bir sonraki satirda "bastirilan=N" olarak raporlanir.
//
// NEDEN: bir tarama/bot seli ret basina bir satirla 75 sn'de 194 bin satir
// (35 MB), 429 seli 10 sn'de 21 MB log uretti (O5). Kayit (request_logs)
// zaten rejectLimiter ile ornekleniyor; bu, ayni korumayi surec loguna getirir.
// Zero-value kullanima hazirdir.
type warnSampler struct {
	mu     sync.Mutex
	m      map[string]*warnSlot
	now    func() time.Time // testlerde sabitlenir
	pruned time.Time
}

type warnSlot struct {
	last       time.Time
	suppressed int64
}

const (
	warnSampleEvery = time.Second
	// warnSamplePrune: bu kadar sessiz kalan anahtarlar silinir (bellek siniri:
	// cok sayida tunel/neden bilesimi kalici olarak tutulmasin).
	warnSamplePrune = 5 * time.Minute
)

// allow, (neden, tunel) icin simdi bir satir yazilabilir mi soyler. true ise
// suppressed, son yazilan satirdan bu yana bastirilan olay sayisidir.
func (s *warnSampler) allow(reason, tunnelID string) (ok bool, suppressed int64) {
	now := time.Now()
	if s.now != nil {
		now = s.now()
	}
	key := reason + "\x00" + tunnelID
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.m == nil {
		s.m = make(map[string]*warnSlot)
	}
	if now.Sub(s.pruned) > warnSamplePrune {
		s.pruned = now
		for k, sl := range s.m {
			if now.Sub(sl.last) > warnSamplePrune {
				delete(s.m, k)
			}
		}
	}
	sl := s.m[key]
	if sl == nil {
		s.m[key] = &warnSlot{last: now}
		return true, 0
	}
	if now.Sub(sl.last) < warnSampleEvery {
		sl.suppressed++
		return false, 0
	}
	n := sl.suppressed
	sl.suppressed = 0
	sl.last = now
	return true, n
}
