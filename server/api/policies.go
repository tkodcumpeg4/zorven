package api

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"github.com/tkodcumpeg4/zorven/server/entitlements"
	"github.com/tkodcumpeg4/zorven/server/ingress"
	"github.com/tkodcumpeg4/zorven/server/store"
)

// FAZ 1 (F04): Birlesik Policy motoru REST uclari. Her degisiklikte router snapshot'i
// tazelenir (tunnelsChanged). Pro+ plan gerektirir.

func (s *Server) listPolicies(w http.ResponseWriter, r *http.Request) {
	tenantID, ok := s.tenantFor(r)
	if !ok {
		writeJSONError(w, http.StatusInternalServerError, "no_tenant", "istek kiraci kapsami olmadan ulasti")
		return
	}
	projID, _ := s.projectFor(r)

	policies, err := s.Store.ListPolicies(r.Context(), tenantID, projID)
	if err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"policies": policies,
		"count":    len(policies),
	})
}

func (s *Server) getPolicy(w http.ResponseWriter, r *http.Request) {
	tenantID, ok := s.tenantFor(r)
	if !ok {
		writeJSONError(w, http.StatusInternalServerError, "no_tenant", "istek kiraci kapsami olmadan ulasti")
		return
	}
	id := r.PathValue("id")
	pol, err := s.Store.GetPolicy(r.Context(), tenantID, id)
	if err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, pol)
}

func (s *Server) createPolicy(w http.ResponseWriter, r *http.Request) {
	tenantID, ok := s.tenantFor(r)
	if !ok {
		writeJSONError(w, http.StatusInternalServerError, "no_tenant", "istek kiraci kapsami olmadan ulasti")
		return
	}
	if s.Entitlements != nil {
		if err := s.Entitlements.CheckFeature(r.Context(), tenantID, entitlements.FeaturePolicyEngine); err != nil {
			writeEntitlementError(w, err)
			return
		}
	}
	projID, _ := s.projectFor(r)

	var body struct {
		Name     string          `json:"name"`
		Config   json.RawMessage `json:"config"`
		Priority int             `json:"priority"`
	}
	if !decode(w, r, &body) {
		return
	}
	if strings.TrimSpace(body.Name) == "" {
		writeJSONError(w, http.StatusBadRequest, "bad_request", "policy adı gerekli")
		return
	}
	if err := ingress.ValidatePolicyConfig(body.Config); err != nil {
		writeJSONError(w, http.StatusUnprocessableEntity, "invalid_policy", err.Error())
		return
	}

	pol, err := s.Store.CreatePolicy(r.Context(), tenantID, projID, body.Name, body.Config, body.Priority)
	if err != nil {
		if errors.Is(err, store.ErrPolicyNameTaken) {
			writeJSONError(w, http.StatusConflict, "policy_name_taken", "bu isimde bir policy zaten var")
			return
		}
		s.fail(w, err)
		return
	}
	s.audit(r, "policy.create", pol.ID, pol.Name)
	s.tunnelsChanged()
	writeJSON(w, http.StatusCreated, pol)
}

func (s *Server) updatePolicy(w http.ResponseWriter, r *http.Request) {
	tenantID, ok := s.tenantFor(r)
	if !ok {
		writeJSONError(w, http.StatusInternalServerError, "no_tenant", "istek kiraci kapsami olmadan ulasti")
		return
	}
	if s.Entitlements != nil {
		if err := s.Entitlements.CheckFeature(r.Context(), tenantID, entitlements.FeaturePolicyEngine); err != nil {
			writeEntitlementError(w, err)
			return
		}
	}
	id := r.PathValue("id")

	// Kismi guncelleme: gonderilmeyen alan MEVCUT degerini korur. Gecmiste
	// {"enabled":false} gonderimi adi ve config'i siliyordu.
	var body struct {
		Name     *string         `json:"name"`
		Config   json.RawMessage `json:"config"`
		Enabled  *bool           `json:"enabled"`
		Priority *int            `json:"priority"`
	}
	if !decode(w, r, &body) {
		return
	}
	cur, err := s.Store.GetPolicy(r.Context(), tenantID, id)
	if err != nil {
		s.fail(w, err)
		return
	}
	name, config, enabled, priority := cur.Name, cur.Config, cur.Enabled, cur.Priority
	if body.Name != nil {
		name = strings.TrimSpace(*body.Name)
		if name == "" {
			writeJSONError(w, http.StatusBadRequest, "bad_request", "policy adı boş olamaz")
			return
		}
	}
	if len(body.Config) > 0 {
		if verr := ingress.ValidatePolicyConfig(body.Config); verr != nil {
			writeJSONError(w, http.StatusUnprocessableEntity, "invalid_policy", verr.Error())
			return
		}
		config = body.Config
	}
	if body.Enabled != nil {
		enabled = *body.Enabled
	}
	if body.Priority != nil && *body.Priority != 0 {
		priority = *body.Priority
	}

	pol, err := s.Store.UpdatePolicy(r.Context(), tenantID, id, name, config, enabled, priority)
	if err != nil {
		if errors.Is(err, store.ErrPolicyNameTaken) {
			writeJSONError(w, http.StatusConflict, "policy_name_taken", "bu isimde bir policy zaten var")
			return
		}
		s.fail(w, err)
		return
	}
	s.audit(r, "policy.update", pol.ID, pol.Name)
	s.tunnelsChanged()
	writeJSON(w, http.StatusOK, pol)
}

func (s *Server) deletePolicy(w http.ResponseWriter, r *http.Request) {
	tenantID, ok := s.tenantFor(r)
	if !ok {
		writeJSONError(w, http.StatusInternalServerError, "no_tenant", "istek kiraci kapsami olmadan ulasti")
		return
	}
	id := r.PathValue("id")
	if err := s.Store.DeletePolicy(r.Context(), tenantID, id); err != nil {
		s.fail(w, err)
		return
	}
	s.audit(r, "policy.delete", id, "")
	s.tunnelsChanged()
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) bindPolicy(w http.ResponseWriter, r *http.Request) {
	s.mutatePolicyBinding(w, r, true)
}

func (s *Server) unbindPolicy(w http.ResponseWriter, r *http.Request) {
	s.mutatePolicyBinding(w, r, false)
}

func (s *Server) mutatePolicyBinding(w http.ResponseWriter, r *http.Request, bind bool) {
	tenantID, ok := s.tenantFor(r)
	if !ok {
		writeJSONError(w, http.StatusInternalServerError, "no_tenant", "istek kiraci kapsami olmadan ulasti")
		return
	}
	policyID := r.PathValue("id")

	var body struct {
		TunnelID string `json:"tunnel_id"`
		Hostname string `json:"hostname"`
	}
	if !decode(w, r, &body) {
		return
	}
	b := store.PolicyBinding{
		PolicyID: policyID,
		TunnelID: strings.TrimSpace(body.TunnelID),
		Hostname: strings.TrimSpace(body.Hostname),
	}
	if b.TunnelID == "" && b.Hostname == "" {
		writeJSONError(w, http.StatusBadRequest, "bad_request", "tunnel_id veya hostname gerekli")
		return
	}

	var err error
	if bind {
		err = s.Store.BindPolicy(r.Context(), tenantID, policyID, b)
	} else {
		err = s.Store.UnbindPolicy(r.Context(), tenantID, policyID, b)
	}
	if err != nil {
		s.fail(w, err)
		return
	}
	action := "policy.bind"
	if !bind {
		action = "policy.unbind"
	}
	s.audit(r, action, policyID, b.TunnelID+b.Hostname)
	s.tunnelsChanged()
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}
