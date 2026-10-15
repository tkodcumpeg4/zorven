package api

import "net/http"

// ACIK SURUM override (scripts/oss/overrides): acik cekirdekte resauthz ve
// servis hesabi yoktur; kural aynidir: yalniz kiraci owner/admin ve gercek
// platform yoneticisi uzak ekrani denetler, uyeler yalnizca izler.
// Kullanici YOKKEN izin verilmez (fail-closed).

// screenControlAllowed, istegin sahibinin uzak ekrani denetleyip
// denetleyemeyecegini doner.
func (s *Server) screenControlAllowed(r *http.Request, tenantID string) bool {
	if isPlatformAdmin(r.Context()) {
		return true
	}
	u, ok := userFromContext(r.Context())
	if !ok || u == nil {
		return false
	}
	role := u.Role
	if role == "" || role == "api_token" {
		if s.Store == nil {
			return false
		}
		var err error
		role, err = s.Store.GetMemberRole(r.Context(), tenantID, u.ID)
		if err != nil {
			return false
		}
	}
	return isPrivilegedRole(role)
}
