package api

// Zorven Net Mod B (OS TUN, W3) yonetim uclari. Hepsi owner/admin (ayricalikli).
//
//	GET    /api/v1/net/tun/rules        — Mod B izin kurallari
//	POST   /api/v1/net/tun/rules        — kural ekle {src:{type,id}, dst, proto, ports, name}
//	DELETE /api/v1/net/tun/rules/{id}   — kural sil
//	GET    /api/v1/net/network          — kiraci ic adres havuzu (CIDR)
//	PUT    /api/v1/net/network          — havuzu degistir {cidr} (yalniz hic dugum yokken)
//
// Karar (R8): Mod B dugumu cihaz kimligiyle girer; kurallar cihazdan cihaza
// tanimlanir (kaynak device | group:everyone, hedef cihaz | "*"). Varsayilan:
// hicbir sey (default deny); paylasim alicilari paylasilan porta ayrica erisir.

import (
	"net/http"
	"net/netip"

	"github.com/tkodcumpeg4/zorven/server/netctl"
)

type tunRuleView struct {
	netctl.TunRule
	SrcName string `json:"src_name,omitempty"`
	DstName string `json:"dst_name,omitempty"`
}

func (s *Server) listTunRules(w http.ResponseWriter, r *http.Request) {
	if !requireScope(w, r, ScopeNetRead) {
		return
	}
	tenantID, ok := s.netGate(w, r)
	if !ok {
		return
	}
	if !s.requirePrivileged(w, r, tenantID) {
		return
	}
	rules, err := s.Net.TunRules(r.Context(), tenantID)
	if err != nil {
		s.netFail(w, err)
		return
	}
	nodes, _ := s.Net.Nodes(r.Context(), tenantID)
	byClient := map[string]string{}
	for _, n := range nodes {
		if n.ClientID != "" {
			byClient[n.ClientID] = n.Name
		}
	}
	out := make([]tunRuleView, 0, len(rules))
	for _, ru := range rules {
		v := tunRuleView{TunRule: ru}
		if ru.Src.Type == netctl.GranteeDevice {
			v.SrcName = byClient[ru.Src.ID]
		}
		if ru.Dst != "*" {
			v.DstName = byClient[ru.Dst]
		}
		out = append(out, v)
	}
	writeJSON(w, http.StatusOK, map[string]any{"rules": out, "zone": netctl.TunZone})
}

type createTunRuleBody struct {
	Name  string         `json:"name"`
	Src   netctl.Grantee `json:"src"`
	Dst   string         `json:"dst"`
	Proto string         `json:"proto"`
	Ports string         `json:"ports"`
}

func (s *Server) createTunRule(w http.ResponseWriter, r *http.Request) {
	if !requireScope(w, r, ScopeNetWrite) {
		return
	}
	tenantID, ok := s.netGate(w, r)
	if !ok {
		return
	}
	if !s.requirePrivileged(w, r, tenantID) {
		return
	}
	var body createTunRuleBody
	if !decode(w, r, &body) {
		return
	}
	caller, _ := s.netCaller(r, tenantID)
	rule, err := s.Net.CreateTunRule(r.Context(), tenantID, netctl.TunRuleInput{
		Name: body.Name, Src: body.Src, Dst: body.Dst, Proto: body.Proto, Ports: body.Ports,
	}, netActor(r, caller))
	if err != nil {
		s.netFail(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, rule)
}

func (s *Server) deleteTunRule(w http.ResponseWriter, r *http.Request) {
	if !requireScope(w, r, ScopeNetWrite) {
		return
	}
	tenantID, ok := s.netGate(w, r)
	if !ok {
		return
	}
	if !s.requirePrivileged(w, r, tenantID) {
		return
	}
	caller, _ := s.netCaller(r, tenantID)
	if err := s.Net.DeleteTunRule(r.Context(), tenantID, r.PathValue("id"), netActor(r, caller)); err != nil {
		s.netFail(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) getNetNetwork(w http.ResponseWriter, r *http.Request) {
	if !requireScope(w, r, ScopeNetRead) {
		return
	}
	tenantID, ok := s.netGate(w, r)
	if !ok {
		return
	}
	pool, err := s.Net.Network(r.Context(), tenantID)
	if err != nil {
		s.netFail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"cidr": pool.String(), "resolver": netctl.ResolverIP(pool).String(), "zone": netctl.TunZone,
		"tun_enabled": s.Net.Tun,
	})
}

func (s *Server) putNetNetwork(w http.ResponseWriter, r *http.Request) {
	if !requireScope(w, r, ScopeNetWrite) {
		return
	}
	tenantID, ok := s.netGate(w, r)
	if !ok {
		return
	}
	if !s.requirePrivileged(w, r, tenantID) {
		return
	}
	var body struct {
		CIDR string `json:"cidr"`
	}
	if !decode(w, r, &body) {
		return
	}
	p, err := netip.ParsePrefix(body.CIDR)
	if err != nil {
		writeJSONError(w, http.StatusUnprocessableEntity, "invalid_cidr", "gecerli bir CIDR gerekli (ornek 10.99.0.0/16)")
		return
	}
	caller, _ := s.netCaller(r, tenantID)
	if err := s.Net.SetNetwork(r.Context(), tenantID, p, netActor(r, caller)); err != nil {
		s.netFail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"cidr": p.Masked().String(), "resolver": netctl.ResolverIP(p).String()})
}
