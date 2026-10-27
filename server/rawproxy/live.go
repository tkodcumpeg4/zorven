package rawproxy

// ZTNA Z3 / Z2 eksigi: ham TCP/UDP icin tunel Geo kurali ve ACIK akislarin yeniden
// degerlendirilmesi.
//
//   - Geo: admit()'te, IP filtresi ve kapi grant'inden ONCE uygulanir (yasakli
//     ulkeden gelen IP'ye grant verilmis olsa da girilemez).
//   - Canli akis kayit defteri: kabul edilen TUM baglanti/akislar kaydedilir;
//     politika, IP listesi, kapi veya tunel Geo ayari degisince Reevaluate acik
//     akislari yeniden degerlendirir ve izni kalkanlari keser.

import (
	"strings"
	"sync"

	"github.com/tkodcumpeg4/zorven/server/tunsec"
)

// SetSecurity, tunel kimligi -> derlenmis WAF/Geo ayari haritasini degistirir
// (yalniz Geo kullanilir; WAF HTTP katmanindadir). Harita sonradan DEGISTIRILMEMELI.
func (m *Manager) SetSecurity(sec map[string]*tunsec.Compiled) {
	m.sec.Store(&sec)
}

func (m *Manager) secFor(tunnelID string) *tunsec.Compiled {
	p := m.sec.Load()
	if p == nil {
		return nil
	}
	return (*p)[tunnelID]
}

// geoAllowed, tunelin ulke kurali bu IP'ye izin veriyor mu. Kural yoksa true.
func (m *Manager) geoAllowed(tunnelID, ip string) (ok bool, country string) {
	sec := m.secFor(tunnelID)
	if !sec.GeoActive() {
		return true, ""
	}
	return sec.GeoCheck(m.Geo, ip)
}

// liveFlow, kabul edilmis tek bir baglanti/akis.
type liveFlow struct {
	tunnelID, tenantID, ip string
	byPort                 bool // rezerve-port tuneli (dinleyici kaldirilinca kesilir)
	closeFn                func()
}

type liveRegistry struct {
	mu   sync.Mutex
	next uint64
	m    map[uint64]*liveFlow
}

func (r *liveRegistry) add(f *liveFlow) (remove func()) {
	r.mu.Lock()
	if r.m == nil {
		r.m = map[uint64]*liveFlow{}
	}
	r.next++
	id := r.next
	r.m[id] = f
	r.mu.Unlock()
	return func() {
		r.mu.Lock()
		delete(r.m, id)
		r.mu.Unlock()
	}
}

func (r *liveRegistry) snapshot(tenantID string) []*liveFlow {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]*liveFlow, 0, len(r.m))
	for _, f := range r.m {
		if tenantID == "" || f.tenantID == tenantID {
			out = append(out, f)
		}
	}
	return out
}

// LiveFlows, acik akis sayisi (tenantID bos = hepsi).
func (m *Manager) LiveFlows(tenantID string) int { return len(m.live.snapshot(tenantID)) }

// registerLive, kabul edilen akisi kaydeder; donen fonksiyon akis bitince cagrilir.
func (m *Manager) registerLive(ti TunnelInfo, ip string, byPort bool, closeFn func()) (remove func()) {
	return m.live.add(&liveFlow{tunnelID: ti.TunnelID, tenantID: ti.TenantID, ip: ip, byPort: byPort, closeFn: closeFn})
}

// portTunnelActive, rezerve-port tunelinin dinleyicisi hala tanimli mi.
func (m *Manager) portTunnelActive(tunnelID string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, ti := range m.byPort {
		if ti.TunnelID == tunnelID {
			return true
		}
	}
	return false
}

// Reevaluate, kiracinin (bos = tum kiracilar) ACIK ham TCP/UDP akislarini yeniden
// degerlendirir: tunel Geo kurali, IP izin listesi ve kapi grant'i bugunku haliyle
// admit() kararindan gecer; dinleyicisi kaldirilmis rezerve-port tunelinin akislari
// kesilir. Izni kalkan akislar kapatilir; kesilen sayi doner. Karar verilemeyen
// (gecici hata) durumda kesilmez: IPFilter hatasi admit'te RET sayilir, bu yuzden
// yalniz kalici ret kapatir.
func (m *Manager) Reevaluate(tenantID string) int {
	flows := m.live.snapshot(tenantID)
	closed := 0
	for _, f := range flows {
		ti := TunnelInfo{TunnelID: f.tunnelID, TenantID: f.tenantID}
		reason := ""
		switch {
		case f.byPort && !m.portTunnelActive(f.tunnelID):
			reason = "tunnel_removed"
		default:
			if ok, _, why := m.admitCheck(m.ctx(), ti, f.ip, false); !ok {
				reason = why
			}
		}
		if reason == "" {
			continue
		}
		f.closeFn()
		closed++
		if m.Log != nil {
			m.Log.Info("rawproxy: acik akis yeniden degerlendirmede kesildi",
				"tunnel", f.tunnelID, "ip", f.ip, "neden", reason)
		}
	}
	return closed
}

// geoReason, admit ret nedeni etiketi.
func geoReason(country string) string {
	return "geo:" + strings.ToLower(country)
}
