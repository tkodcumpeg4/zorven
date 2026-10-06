package api

import (
	"net/http"
	"strings"

	"github.com/tkodcumpeg4/zorven/server/store"
)

// FAZ 6.5 — yol tabanlı yönlendirme uçları (hostname bazlı).
//
//	GET    /api/v1/hostnames/{id}/paths            — hostname'in yol kuralları
//	POST   /api/v1/hostnames/{id}/paths            — kural ekle {path_prefix, tunnel_id}
//	DELETE /api/v1/hostnames/{id}/paths/{routeID}  — kural sil

func (s *Server) listPathRoutes(w http.ResponseWriter, r *http.Request) {
	if !requireScope(w, r, ScopeHostnamesRead) {
		return
	}
	tenantID, ok := s.tenantFor(r)
	if !ok {
		writeJSONError(w, http.StatusInternalServerError, "no_tenant", "istek kiraci kapsami olmadan ulasti")
		return
	}
	h, err := s.Store.GetHostnameByID(r.Context(), tenantID, r.PathValue("id"))
	if err != nil {
		writeJSONError(w, http.StatusNotFound, "not_found", "hostname bulunamadi")
		return
	}
	routes, err := s.Store.ListPathRoutes(r.Context(), tenantID, h.FQDN)
	if err != nil {
		s.fail(w, err)
		return
	}
	if routes == nil {
		routes = []store.PathRoute{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"fqdn": h.FQDN, "routes": routes})
}

func (s *Server) addPathRoute(w http.ResponseWriter, r *http.Request) {
	if !requireScope(w, r, ScopeHostnamesWrite) {
		return
	}
	tenantID, ok := s.tenantFor(r)
	if !ok {
		writeJSONError(w, http.StatusInternalServerError, "no_tenant", "istek kiraci kapsami olmadan ulasti")
		return
	}
	h, err := s.Store.GetHostnameByID(r.Context(), tenantID, r.PathValue("id"))
	if err != nil {
		writeJSONError(w, http.StatusNotFound, "not_found", "hostname bulunamadi")
		return
	}
	var body struct {
		PathPrefix string `json:"path_prefix"`
		TunnelID   string `json:"tunnel_id"`
	}
	if !decode(w, r, &body) {
		return
	}
	if strings.TrimSpace(body.PathPrefix) == "" || strings.TrimSpace(body.TunnelID) == "" {
		writeJSONError(w, http.StatusUnprocessableEntity, "missing_fields", "path_prefix ve tunnel_id zorunlu")
		return
	}
	pr, err := s.Store.AddPathRoute(r.Context(), tenantID, h.FQDN, body.PathPrefix, strings.TrimSpace(body.TunnelID))
	if err != nil {
		s.fail(w, err)
		return
	}
	s.tunnelsChanged()
	writeJSON(w, http.StatusCreated, pr)
}

func (s *Server) deletePathRoute(w http.ResponseWriter, r *http.Request) {
	if !requireScope(w, r, ScopeHostnamesWrite) {
		return
	}
	tenantID, ok := s.tenantFor(r)
	if !ok {
		writeJSONError(w, http.StatusInternalServerError, "no_tenant", "istek kiraci kapsami olmadan ulasti")
		return
	}
	if err := s.Store.DeletePathRoute(r.Context(), tenantID, r.PathValue("routeID")); err != nil {
		s.fail(w, err)
		return
	}
	s.tunnelsChanged()
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}
