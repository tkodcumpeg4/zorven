package ingress

// Ham TCP/UDP tunellerinde (rawproxy) replika secimi.
//
// HTTP ile AYNI algoritma ve AYNI tablolar kullanilir: tunelin yuk dengeleme
// kaydi varsa orderBackends (strateji, agirlik, saglik, panik modu), yoksa
// orderOnline (cevrimici uyeler arasinda sirali dagitim). Saglik durumunu
// HealthChecker yazar, acik baglanti/flow sayaci lbState'te tutulur; boylece
// panelde gorunen saglik ve en-az-baglanti sayaci HTTP ve ham trafik icin
// ortaktir. rawproxy ingress'e bagimli olmasin diye rawproxy.Balancer arayuzu
// bu metotlarla karsilanir (main.go: rawMgr.Balancer = router).

import (
	"context"
	"time"

	"github.com/tkodcumpeg4/zorven/server/store"
)

// rawView, ham tunel secimi icin backendView: cevrimicilik cagirandan (rawproxy
// Hub'a bakar), saglik/gecikme/acik sayaci router tablosundan gelir.
type rawView struct {
	lb       *lbState
	tunnelID string
	isOnline func(clientID string) bool
}

func (v rawView) online(c string) bool { return v.isOnline(c) }
func (v rawView) healthy(c string) bool {
	return !v.lb.healthOf(v.tunnelID, c).unhealthy.Load()
}
func (v rawView) latency(c string) time.Duration {
	return time.Duration(v.lb.healthOf(v.tunnelID, c).latencyNs.Load())
}
func (v rawView) inflight(c string) int64 { return v.lb.inflightOf(c).Load() }

// OrderClients, ham TCP/UDP tunelinin adaylarini (birincil + replikalar) secim
// sirasina dizer: ilk eleman secilen istemci, kalanlar failover sirasidir.
// online(c) false donen aday elenir; hic aday cevrimici degilse nil doner.
// start, tunel basina artan round-robin sayacidir (cagiran tutar).
func (r *Router) OrderClients(tunnelID string, candidates []string, start uint64, online func(clientID string) bool) []string {
	if e, ok := r.lbFor(tunnelID); ok {
		return orderBackends(candidates, e.cfg, start, rawView{lb: &r.lb, tunnelID: tunnelID, isOnline: online})
	}
	return orderOnline(candidates, start, online)
}

// ClientBegin / ClientEnd, istemciye yonlendirilen acik ham baglanti/flow
// sayacini tutar (least_connections; HTTP isteklerinin sayaciyla ortak).
func (r *Router) ClientBegin(clientID string) { r.lb.begin(clientID) }
func (r *Router) ClientEnd(clientID string)   { r.lb.end(clientID) }

// rawLBEntries, yuk dengeleme kaydi olup hostname'i olmayan (route'u bulunmayan)
// rezerve-port ham tunelleri icin lbEntry kurar. Boylece hostname'siz bir TCP/UDP
// tunelinde de strateji/agirlik/saglik ayari uygulanir ve saglik denetleyicisi
// replikalari yoklar. Yalnizca bu tur kayit varsa cagrilir.
func (r *Router) rawLBEntries(ctx context.Context, cfgs []store.TunnelLB) (map[string]*lbEntry, error) {
	tuns, err := r.st.ListReservedPortTunnels(ctx)
	if err != nil {
		return nil, err
	}
	reps, err := r.st.ListAllTunnelReplicas(ctx)
	if err != nil {
		return nil, err
	}
	byID := make(map[string]store.Tunnel, len(tuns))
	for _, t := range tuns {
		byID[t.ID] = t
	}
	out := make(map[string]*lbEntry, len(cfgs))
	for _, c := range cfgs {
		t, ok := byID[c.TunnelID]
		if !ok {
			continue
		}
		rt := store.HostRoute{ClientID: t.ClientID, ReplicaClientIDs: reps[t.ID]}
		out[c.TunnelID] = &lbEntry{cfg: c, clients: candidateClients(rt), proto: t.Proto}
	}
	return out, nil
}
