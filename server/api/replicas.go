package api

import (
	"net/http"
	"strings"
)

// FAZ 5 / HA — tünel replikaları.
//
//	GET    /api/v1/tunnels/{id}/replicas             — replikaları listele
//	POST   /api/v1/tunnels/{id}/replicas             — replika ekle {client_id}
//	DELETE /api/v1/tunnels/{id}/replicas/{clientID}  — replika kaldır
//
// Bir tünel, birincil istemcisine ek olarak başka istemciler tarafından da
// servis edilebilir; ingress istekleri çevrimiçi üyeler arasında dağıtır.

type replicaOut struct {
	ClientID string `json:"client_id"`
	Name     string `json:"name"`
	Online   bool   `json:"online"`
}

func (s *Server) getTunnelReplicas(w http.ResponseWriter, r *http.Request) {
	if !requireScope(w, r, ScopeTunnelsRead) {
		return
	}
	tenantID, ok := s.tenantFor(r)
	if !ok {
		writeJSONError(w, http.StatusInternalServerError, "no_tenant", "istek kiraci kapsami olmadan ulasti")
		return
	}
	ids, err := s.Store.ListTunnelReplicas(r.Context(), tenantID, r.PathValue("id"))
	if err != nil {
		s.fail(w, err)
		return
	}
	out := make([]replicaOut, 0, len(ids))
	for _, cid := range ids {
		ro := replicaOut{ClientID: cid}
		if c, cerr := s.Store.GetClient(r.Context(), tenantID, cid); cerr == nil {
			ro.Name = c.Name
		}
		if s.Hub != nil {
			_, ro.Online = s.Hub.Get(cid)
		}
		out = append(out, ro)
	}
	writeJSON(w, http.StatusOK, map[string]any{"replicas": out, "count": len(out)})
}

func (s *Server) addTunnelReplica(w http.ResponseWriter, r *http.Request) {
	if !requireScope(w, r, ScopeTunnelsWrite) {
		return
	}
	tenantID, ok := s.tenantFor(r)
	if !ok {
		writeJSONError(w, http.StatusInternalServerError, "no_tenant", "istek kiraci kapsami olmadan ulasti")
		return
	}
	tunnelID := r.PathValue("id")
	var body struct {
		ClientID string `json:"client_id"`
	}
	if !decode(w, r, &body) {
		return
	}
	clientID := strings.TrimSpace(body.ClientID)
	if clientID == "" {
		writeJSONError(w, http.StatusUnprocessableEntity, "missing_fields", "client_id zorunlu")
		return
	}
	// Birincil istemci zaten servis ediyor; replika olarak eklemenin anlamı yok.
	if tun, terr := s.Store.GetTunnel(r.Context(), tenantID, tunnelID); terr == nil && tun.ClientID == clientID {
		writeJSONError(w, http.StatusConflict, "already_primary", "bu istemci tunelin birincil istemcisi")
		return
	}
	if err := s.Store.AddTunnelReplica(r.Context(), tenantID, tunnelID, clientID); err != nil {
		s.fail(w, err)
		return
	}
	// Replika istemciye tüneli (hedefiyle) bildir + router snapshot'ını tazele.
	s.pushClientConfig(clientID)
	s.tunnelsChanged()
	writeJSON(w, http.StatusCreated, map[string]any{"ok": true, "client_id": clientID})
}

func (s *Server) removeTunnelReplica(w http.ResponseWriter, r *http.Request) {
	if !requireScope(w, r, ScopeTunnelsWrite) {
		return
	}
	if !s.requirePrivilegedCaller(w, r) {
		return
	}
	tenantID, ok := s.tenantFor(r)
	if !ok {
		writeJSONError(w, http.StatusInternalServerError, "no_tenant", "istek kiraci kapsami olmadan ulasti")
		return
	}
	tunnelID := r.PathValue("id")
	clientID := r.PathValue("clientID")
	if err := s.Store.RemoveTunnelReplica(r.Context(), tenantID, tunnelID, clientID); err != nil {
		s.fail(w, err)
		return
	}
	// Replikadan tüneli düşür + router snapshot'ını tazele.
	s.pushClientConfig(clientID)
	s.tunnelsChanged()
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}
