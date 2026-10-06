package api

// FAZ 4 / F21 — Tunel basina yuk dengeleme + saglik kontrolu.
//
//	GET /api/v1/tunnels/{id}/lb   — yapilandirma + adaylar + CANLI saglik durumu
//	PUT /api/v1/tunnels/{id}/lb   — yapilandirmayi yaz (router aninda yenilenir)

import (
	"net/http"
	"strings"

	"github.com/tkodcumpeg4/zorven/server/entitlements"
	"github.com/tkodcumpeg4/zorven/server/ingress"
	"github.com/tkodcumpeg4/zorven/server/store"
)

// Ayar sinirlari: uc degerler ya ajani/yerel servisi bosuna yorar ya da
// denetimi anlamsizlastirir.
const (
	lbMinIntervalSec = 2
	lbMaxIntervalSec = 3600
	lbMinTimeoutSec  = 1
	lbMaxTimeoutSec  = 30
	lbMaxThreshold   = 10
	lbMaxWeight      = 1000
)

func validStrategy(s string) bool {
	switch s {
	case store.LBRoundRobin, store.LBWeighted, store.LBLeastConnections, store.LBLatency:
		return true
	}
	return false
}

// validateTunnelLB, yapilandirmayi dogrular. candidates: tunelin istemcileri.
func validateTunnelLB(lb store.TunnelLB, candidates []string) string {
	if !validStrategy(lb.Strategy) {
		return "strategy round_robin, weighted, least_connections veya latency olmalı"
	}
	ok := make(map[string]bool, len(candidates))
	for _, c := range candidates {
		ok[c] = true
	}
	for cid, w := range lb.Weights {
		if !ok[cid] {
			return "ağırlık verilen istemci bu tünelin replikası değil: " + cid
		}
		if w < 0 || w > lbMaxWeight {
			return "ağırlık 0 ile 1000 arasında olmalı"
		}
	}
	if lb.HealthEnabled {
		if !strings.HasPrefix(lb.HealthPath, "/") || strings.ContainsAny(lb.HealthPath, " \r\n") {
			return "sağlık yolu '/' ile başlamalı ve boşluk içermemeli"
		}
		if lb.IntervalSec < lbMinIntervalSec || lb.IntervalSec > lbMaxIntervalSec {
			return "denetim aralığı 2 ile 3600 saniye arasında olmalı"
		}
		if lb.TimeoutSec < lbMinTimeoutSec || lb.TimeoutSec > lbMaxTimeoutSec {
			return "zaman aşımı 1 ile 30 saniye arasında olmalı"
		}
		if lb.TimeoutSec >= lb.IntervalSec {
			return "zaman aşımı denetim aralığından kısa olmalı"
		}
		if lb.UnhealthyThreshold < 1 || lb.UnhealthyThreshold > lbMaxThreshold ||
			lb.HealthyThreshold < 1 || lb.HealthyThreshold > lbMaxThreshold {
			return "eşikler 1 ile 10 arasında olmalı"
		}
	}
	return ""
}

// lbCandidates, tunelin birincil istemcisi + replikalari.
func (s *Server) lbCandidates(r *http.Request, tenantID string, tun store.Tunnel) ([]string, error) {
	reps, err := s.Store.ListTunnelReplicas(r.Context(), tenantID, tun.ID)
	if err != nil {
		return nil, err
	}
	out := []string{tun.ClientID}
	for _, c := range reps {
		if c != tun.ClientID {
			out = append(out, c)
		}
	}
	return out, nil
}

func (s *Server) getTunnelLB(w http.ResponseWriter, r *http.Request) {
	if !requireScope(w, r, ScopeTunnelsRead) {
		return
	}
	tenantID, ok := s.tenantFor(r)
	if !ok {
		writeJSONError(w, http.StatusInternalServerError, "no_tenant", "istek kiraci kapsami olmadan ulasti")
		return
	}
	tun, err := s.Store.GetTunnel(r.Context(), tenantID, r.PathValue("id"))
	if err != nil {
		s.fail(w, err)
		return
	}
	cfg, err := s.Store.GetTunnelLB(r.Context(), tenantID, tun.ID)
	if err != nil {
		s.fail(w, err)
		return
	}
	cands, err := s.lbCandidates(r, tenantID, tun)
	if err != nil {
		s.fail(w, err)
		return
	}
	var health []ingress.BackendStatus
	if s.LBHealth != nil {
		health = s.LBHealth(tun.ID, cands)
	}
	writeJSON(w, http.StatusOK, map[string]any{"config": cfg, "candidates": cands, "health": health})
}

func (s *Server) setTunnelLB(w http.ResponseWriter, r *http.Request) {
	if !requireScope(w, r, ScopeTunnelsWrite) {
		return
	}
	tenantID, ok := s.tenantFor(r)
	if !ok {
		writeJSONError(w, http.StatusInternalServerError, "no_tenant", "istek kiraci kapsami olmadan ulasti")
		return
	}
	tun, err := s.Store.GetTunnel(r.Context(), tenantID, r.PathValue("id"))
	if err != nil {
		s.fail(w, err)
		return
	}
	var body store.TunnelLB
	if !decode(w, r, &body) {
		return
	}
	body.TunnelID = tun.ID
	body.Strategy = strings.TrimSpace(body.Strategy)
	if body.Strategy == "" {
		body.Strategy = store.LBRoundRobin
	}
	if body.Weights == nil {
		body.Weights = map[string]int{}
	}
	body.HealthPath = strings.TrimSpace(body.HealthPath)
	if body.HealthPath == "" {
		body.HealthPath = "/"
	}
	cands, err := s.lbCandidates(r, tenantID, tun)
	if err != nil {
		s.fail(w, err)
		return
	}
	if verr := validateTunnelLB(body, cands); verr != "" {
		writeJSONError(w, http.StatusUnprocessableEntity, "invalid_lb", verr)
		return
	}
	// Gelismis dengeleme (varsayilan disi strateji veya saglik denetimi) Team+.
	if (body.Strategy != store.LBRoundRobin || body.HealthEnabled) && s.Entitlements != nil {
		if err := s.Entitlements.CheckFeature(r.Context(), tenantID, entitlements.FeatureAdvancedLB); err != nil {
			writeEntitlementError(w, err)
			return
		}
	}
	if err := s.Store.SetTunnelLB(r.Context(), tenantID, body); err != nil {
		s.fail(w, err)
		return
	}
	s.tunnelsChanged() // router yenilenir; denetleyici yeni ayari gorur
	s.audit(r, "tunnel.lb.update", tun.ID, body.Strategy)
	writeJSON(w, http.StatusOK, body)
}
