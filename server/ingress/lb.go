package ingress

// FAZ 4 / F21 — Saglik durumlu, strateji secilebilir yuk dengeleme.
//
// Sicak yol kurali: istek basina DB sorgusu YOK. Yapilandirma router
// yenilemesinde belleğe alinir; saglik durumu arka plan denetleyicisi
// tarafindan yazilir, secim yalnizca atomik okumalar yapar.

import (
	"sort"
	"sync"
	"sync/atomic"
	"time"

	"github.com/tkodcumpeg4/zorven/server/store"
)

// lbEntry, bir tunelin yuk dengeleme gorunumu (router yenilemesinde kurulur).
type lbEntry struct {
	cfg     store.TunnelLB
	clients []string // birincil + replikalar
	proto   string
}

// backendHealth, tek bir (tunel, istemci) ciftinin saglik durumu.
// Varsayilan SAGLIKLI: denetim henuz calismadiysa trafik kesilmez.
type backendHealth struct {
	unhealthy atomic.Bool
	fails     atomic.Int32
	oks       atomic.Int32
	latencyNs atomic.Int64 // EWMA; 0 = henuz olculmedi
	lastCheck atomic.Int64 // unix nano
	lastErr   atomic.Value // string
}

// lbState, saglik tablosu + istemci basina acik istek sayaci.
type lbState struct {
	health   sync.Map // tunnelID+"|"+clientID -> *backendHealth
	inflight sync.Map // clientID -> *atomic.Int64
}

func healthKey(tunnelID, clientID string) string { return tunnelID + "|" + clientID }

func (s *lbState) healthOf(tunnelID, clientID string) *backendHealth {
	v, _ := s.health.LoadOrStore(healthKey(tunnelID, clientID), &backendHealth{})
	return v.(*backendHealth)
}

func (s *lbState) inflightOf(clientID string) *atomic.Int64 {
	v, _ := s.inflight.LoadOrStore(clientID, new(atomic.Int64))
	return v.(*atomic.Int64)
}

// begin/end, istemciye yonlendirilen istegi sayar (least_connections).
func (s *lbState) begin(clientID string) { s.inflightOf(clientID).Add(1) }
func (s *lbState) end(clientID string)   { s.inflightOf(clientID).Add(-1) }

// record, bir saglik denetimi sonucunu esiklere gore uygular.
// Durum yalnizca ESIK kadar ardisik sonuc gelince degisir (dalgalanma olmasin).
func (s *lbState) record(tunnelID, clientID string, ok bool, latency time.Duration, errMsg string, cfg store.TunnelLB) (changed bool, nowHealthy bool) {
	h := s.healthOf(tunnelID, clientID)
	h.lastCheck.Store(time.Now().UnixNano())
	if ok {
		h.fails.Store(0)
		n := h.oks.Add(1)
		// EWMA (alfa = 0.3): tek bir yavas olcum siralamayi altust etmesin.
		prev := h.latencyNs.Load()
		if prev == 0 {
			h.latencyNs.Store(int64(latency))
		} else {
			h.latencyNs.Store(int64(0.7*float64(prev) + 0.3*float64(latency)))
		}
		h.lastErr.Store("")
		if h.unhealthy.Load() && int(n) >= max(1, cfg.HealthyThreshold) {
			h.unhealthy.Store(false)
			return true, true
		}
		return false, !h.unhealthy.Load()
	}
	h.oks.Store(0)
	n := h.fails.Add(1)
	h.lastErr.Store(errMsg)
	if !h.unhealthy.Load() && int(n) >= max(1, cfg.UnhealthyThreshold) {
		h.unhealthy.Store(true)
		return true, false
	}
	return false, !h.unhealthy.Load()
}

// backendView, siralamanin ihtiyac duydugu okumalar (testte sahtesi verilir).
type backendView interface {
	online(clientID string) bool
	healthy(clientID string) bool
	latency(clientID string) time.Duration // 0 = bilinmiyor
	inflight(clientID string) int64
}

// orderBackends, adaylari secim sirasina dizer: ilk eleman secilen backend,
// kalanlar failover sirasidir. Hic cevrimici yoksa nil.
//
//  1. Cevrimdisi adaylar elenir.
//  2. Saglik denetimi aciksa saglikli olanlar tutulur. HIC saglikli yoksa
//     PANIK MODU: tum cevrimici adaylar kullanilir — hatali bir saglik
//     denetimi (or. yanlis yol) tum servisi dusurmesin.
//  3. Strateji siralamasi.
func orderBackends(cands []string, cfg store.TunnelLB, start uint64, v backendView) []string {
	var online []string
	for _, c := range cands {
		if v.online(c) {
			online = append(online, c)
		}
	}
	if len(online) == 0 {
		return nil
	}
	pool := online
	if cfg.HealthEnabled {
		var healthy []string
		for _, c := range online {
			if v.healthy(c) {
				healthy = append(healthy, c)
			}
		}
		if len(healthy) > 0 {
			pool = healthy
		}
	}

	// Ortak baslangic: round-robin dondurmesi (esitlikleri adil bozar).
	n := len(pool)
	rot := make([]string, n)
	for i := 0; i < n; i++ {
		rot[i] = pool[(int(start%uint64(n))+i)%n]
	}

	switch cfg.Strategy {
	case store.LBWeighted:
		return weightedOrder(pool, rot, cfg.Weights, start)
	case store.LBLeastConnections:
		sort.SliceStable(rot, func(i, j int) bool { return v.inflight(rot[i]) < v.inflight(rot[j]) })
		return rot
	case store.LBLatency:
		// Olculmemis gecikme SONA: olculmus (ve saglikli) backend once denenir.
		lat := func(c string) time.Duration {
			if d := v.latency(c); d > 0 {
				return d
			}
			return time.Duration(1<<62 - 1)
		}
		sort.SliceStable(rot, func(i, j int) bool { return lat(rot[i]) < lat(rot[j]) })
		return rot
	default:
		return rot
	}
}

// weightedOrder, agirlikli round-robin: sayac toplam agirlik uzerinden
// dolasir; secilen backend basa alinir, kalanlar dondurme sirasinda kalir.
// Agirligi tanimsiz istemci 1 sayilir; 0 agirlik "trafik alma" demektir —
// ama HEPSI 0 ise esit dagitima dusulur (yanlis ayar trafigi kesmesin).
func weightedOrder(pool, rot []string, weights map[string]int, start uint64) []string {
	w := func(c string) int {
		if weights == nil {
			return 1
		}
		x, ok := weights[c]
		if !ok {
			return 1
		}
		if x < 0 {
			return 0
		}
		return x
	}
	total := 0
	for _, c := range pool {
		total += w(c)
	}
	if total == 0 {
		return rot
	}
	pick := int(start % uint64(total))
	var chosen string
	for _, c := range pool {
		pick -= w(c)
		if pick < 0 {
			chosen = c
			break
		}
	}
	out := make([]string, 0, len(rot))
	out = append(out, chosen)
	for _, c := range rot {
		if c != chosen && w(c) > 0 {
			out = append(out, c)
		}
	}
	// Agirligi 0 olanlar yalnizca SON CARE failover olarak eklenir.
	for _, c := range rot {
		if c != chosen && w(c) == 0 {
			out = append(out, c)
		}
	}
	return out
}

// hubView, canli Hub + saglik tablosu uzerinden backendView.
type hubView struct {
	h        *Handler
	tunnelID string
}

func (v hubView) online(c string) bool {
	_, ok := v.h.Hub.Get(c)
	return ok
}
func (v hubView) healthy(c string) bool {
	return !v.h.Router.lb.healthOf(v.tunnelID, c).unhealthy.Load()
}
func (v hubView) latency(c string) time.Duration {
	return time.Duration(v.h.Router.lb.healthOf(v.tunnelID, c).latencyNs.Load())
}
func (v hubView) inflight(c string) int64 {
	return v.h.Router.lb.inflightOf(c).Load()
}

// BackendStatus, panelde gosterilen canli saglik durumu (F21).
type BackendStatus struct {
	ClientID  string     `json:"client_id"`
	Healthy   bool       `json:"healthy"`
	Checked   bool       `json:"checked"` // en az bir denetim yapildi mi
	LatencyMS float64    `json:"latency_ms,omitempty"`
	LastCheck *time.Time `json:"last_check,omitempty"`
	LastError string     `json:"last_error,omitempty"`
	InFlight  int64      `json:"in_flight"`
}

// HealthStatus, tunelin adaylarinin canli saglik durumu. Tunelin yuk
// dengeleme kaydi yoksa verilen adaylar icin varsayilan (saglikli, denetlenmedi).
func (r *Router) HealthStatus(tunnelID string, clients []string) []BackendStatus {
	if e, ok := r.lbFor(tunnelID); ok {
		clients = e.clients
	}
	out := make([]BackendStatus, 0, len(clients))
	for _, cid := range clients {
		h := r.lb.healthOf(tunnelID, cid)
		st := BackendStatus{
			ClientID: cid,
			Healthy:  !h.unhealthy.Load(),
			InFlight: r.lb.inflightOf(cid).Load(),
		}
		if ns := h.lastCheck.Load(); ns > 0 {
			t := time.Unix(0, ns).UTC()
			st.LastCheck = &t
			st.Checked = true
		}
		if l := h.latencyNs.Load(); l > 0 {
			st.LatencyMS = float64(l) / float64(time.Millisecond)
		}
		if msg, ok := h.lastErr.Load().(string); ok {
			st.LastError = msg
		}
		out = append(out, st)
	}
	return out
}
