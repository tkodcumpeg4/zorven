package api

import (
	"encoding/json"
	"net/http"

	"github.com/tkodcumpeg4/zorven/server/store"
)

// FAZ 6 — trafik politikası uçları.
//
//	GET /api/v1/tunnels/{id}/traffic  — politikayı getir
//	PUT /api/v1/tunnels/{id}/traffic  — politikayı kaydet {enabled, config}
//
// config JSON şeması (ingress/traffic.go ile aynı):
//
//	{
//	  "request_headers":  {"set": {"X-Foo":"bar"}, "remove": ["X-Bar"]},
//	  "response_headers": {"set": {"X-Frame-Options":"DENY"}, "remove": ["Server"]},
//	  "redirects": [{"match_prefix":"/eski", "location":"/yeni", "status":301}]
//	}

func (s *Server) getTunnelTraffic(w http.ResponseWriter, r *http.Request) {
	if !requireScope(w, r, ScopeTunnelsRead) {
		return
	}
	tenantID, ok := s.tenantFor(r)
	if !ok {
		writeJSONError(w, http.StatusInternalServerError, "no_tenant", "istek kiraci kapsami olmadan ulasti")
		return
	}
	p, err := s.Store.GetTunnelTrafficPolicy(r.Context(), tenantID, r.PathValue("id"))
	if err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"tunnel_id": p.TunnelID,
		"enabled":   p.Enabled,
		"config":    json.RawMessage(p.Config),
	})
}

func (s *Server) setTunnelTraffic(w http.ResponseWriter, r *http.Request) {
	if !requireScope(w, r, ScopeTunnelsWrite) {
		return
	}
	tenantID, ok := s.tenantFor(r)
	if !ok {
		writeJSONError(w, http.StatusInternalServerError, "no_tenant", "istek kiraci kapsami olmadan ulasti")
		return
	}
	id := r.PathValue("id")
	var body struct {
		Enabled bool            `json:"enabled"`
		Config  json.RawMessage `json:"config"`
	}
	if !decode(w, r, &body) {
		return
	}
	// Config'i doğrula: geçerli JSON nesnesi olmalı (boş da olabilir).
	if len(body.Config) > 0 {
		var probe map[string]any
		if err := json.Unmarshal(body.Config, &probe); err != nil {
			writeJSONError(w, http.StatusUnprocessableEntity, "invalid_config", "config gecerli bir JSON nesnesi olmalidir")
			return
		}
	}
	if err := s.Store.SetTunnelTrafficPolicy(r.Context(), tenantID, store.TunnelTrafficPolicy{
		TunnelID: id, Enabled: body.Enabled, Config: body.Config,
	}); err != nil {
		s.fail(w, err)
		return
	}
	// Router snapshot'ını tazele ki kurallar beklemeden etkin olsun.
	s.tunnelsChanged()
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "enabled": body.Enabled})
}
