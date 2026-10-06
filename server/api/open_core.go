package api

// ACIK CEKIRDEK (open-core) uyumluluk katmani.
//
// Ticari katmandaki ozellikler (denetim kaydi, cihaz erisim politikasi/sifir
// guven, servis hesaplari) acik surumde yoktur; cekirdek kodun cagirdigi
// noktalar burada NO-OP / sade davranislarla karsilanir. Boylece cekirdek
// handler'lari ticari kod olmadan ayni imzayla derlenir.

import (
	"net/http"
	"regexp"
	"strings"

	"github.com/tkodcumpeg4/zorven/server/store"
)

// audit / auditFor: denetim kaydi acikta NO-OP'tur (AuditSink).
func (s *Server) audit(_ *http.Request, _, _, _ string)       {}
func (s *Server) auditFor(_ *http.Request, _, _, _, _ string) {}

// isPrivilegedRole, owner/admin rolu mu.
func isPrivilegedRole(role string) bool {
	return role == store.RoleOwner || role == "admin"
}

// requireDeviceAccess: acikta cihaz erisimi uye rollerine baglidir; kiracinin
// her uyesi kiracinin tum cihazlarina erisir.
func (s *Server) requireDeviceAccess(_ http.ResponseWriter, _ *http.Request, _, _ string) bool {
	return true
}

// filterClientsForCaller: acikta liste filtrelenmez.
func (s *Server) filterClientsForCaller(_ *http.Request, _ string, list []store.Client) ([]store.Client, error) {
	return list, nil
}

// requirePrivileged, hassas yazma islemleri icin owner/admin sart kosar.
// Kullaniciya bagli olmayan kimlik (admin anahtari, kiraci token'i) gecer.
func (s *Server) requirePrivileged(w http.ResponseWriter, r *http.Request, tenantID string) bool {
	if isPlatformAdmin(r.Context()) {
		return true
	}
	u, ok := userFromContext(r.Context())
	if !ok {
		return true
	}
	role := u.Role
	if role == "" || role == "api_token" {
		var err error
		role, err = s.Store.GetMemberRole(r.Context(), tenantID, u.ID)
		if err != nil {
			s.fail(w, err)
			return false
		}
	}
	if isPrivilegedRole(role) {
		return true
	}
	writeJSONError(w, http.StatusForbidden, "forbidden",
		"bu islem yalnizca owner veya admin tarafindan yapilabilir")
	return false
}

// --- Cihaz etiketleri (meta veri) -------------------------------------------

// Etiket anahtari: kucuk harf, rakam, nokta, tire, alt cizgi; 1-63 karakter.
var tagKeyRe = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]{0,62}$`)

const (
	maxTagsPerDevice = 32
	maxTagValueLen   = 128
)

func validateTags(tags map[string]string) string {
	if len(tags) > maxTagsPerDevice {
		return "bir cihazda en fazla 32 etiket olabilir"
	}
	for k, v := range tags {
		if !tagKeyRe.MatchString(strings.ToLower(strings.TrimSpace(k))) {
			return "gecersiz etiket anahtari: " + k
		}
		if len(v) > maxTagValueLen || strings.ContainsAny(v, "\r\n") {
			return "gecersiz etiket degeri: " + k
		}
	}
	return ""
}

func (s *Server) getDeviceTags(w http.ResponseWriter, r *http.Request) {
	if !requireScope(w, r, ScopeClientsRead) {
		return
	}
	tenantID, ok := s.tenantFor(r)
	if !ok {
		writeJSONError(w, http.StatusInternalServerError, "no_tenant", "istek kiraci kapsami olmadan ulasti")
		return
	}
	id := r.PathValue("id")
	if _, err := s.Store.GetClient(r.Context(), tenantID, id); err != nil {
		s.fail(w, err)
		return
	}
	tags, err := s.Store.GetDeviceTags(r.Context(), tenantID, id)
	if err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"tags": tags})
}

func (s *Server) setDeviceTags(w http.ResponseWriter, r *http.Request) {
	if !requireScope(w, r, ScopeClientsWrite) {
		return
	}
	tenantID, ok := s.tenantFor(r)
	if !ok {
		writeJSONError(w, http.StatusInternalServerError, "no_tenant", "istek kiraci kapsami olmadan ulasti")
		return
	}
	if !s.requirePrivileged(w, r, tenantID) {
		return
	}
	id := r.PathValue("id")
	if _, err := s.Store.GetClient(r.Context(), tenantID, id); err != nil {
		s.fail(w, err)
		return
	}
	var body struct {
		Tags map[string]string `json:"tags"`
	}
	if !decode(w, r, &body) {
		return
	}
	if body.Tags == nil {
		body.Tags = map[string]string{}
	}
	if verr := validateTags(body.Tags); verr != "" {
		writeJSONError(w, http.StatusUnprocessableEntity, "invalid_tags", verr)
		return
	}
	if err := s.Store.SetDeviceTags(r.Context(), tenantID, id, body.Tags); err != nil {
		s.fail(w, err)
		return
	}
	tags, _ := s.Store.GetDeviceTags(r.Context(), tenantID, id)
	writeJSON(w, http.StatusOK, map[string]any{"tags": tags})
}
