package api

// ACIK CEKIRDEK (open-core) uyumluluk katmani.
//
// Ticari katmandaki ozellikler (denetim kaydi, cihaz erisim politikasi/sifir
// guven, servis hesaplari) acik surumde yoktur; cekirdek kodun cagirdigi
// noktalar burada NO-OP / sade davranislarla karsilanir. Boylece cekirdek
// handler'lari ticari kod olmadan ayni imzayla derlenir.

import (
	"net/http"

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
