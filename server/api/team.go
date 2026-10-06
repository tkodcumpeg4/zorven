package api

import (
	"context"
	"net/http"
	"strings"

	"github.com/tkodcumpeg4/zorven/server/auth"
	"github.com/tkodcumpeg4/zorven/server/store"
)

// listTeamMembers, kiraciya ait tum ekip uyelerini ve plan kotalarini doner.
func (s *Server) listTeamMembers(w http.ResponseWriter, r *http.Request) {
	tenantID, ok := s.tenantFor(r)
	if !ok {
		writeJSONError(w, http.StatusInternalServerError, "no_tenant", "istek kiraci kapsami olmadan ulasti")
		return
	}

	members, err := s.Store.ListTeamMembers(r.Context(), tenantID)
	if err != nil {
		s.fail(w, err)
		return
	}
	if members == nil {
		members = []store.TeamMember{}
	}

	sub, err := s.Store.GetSubscription(r.Context(), tenantID)
	if err != nil {
		s.fail(w, err)
		return
	}

	var maxMembers *int
	if sub.PlanDetails != nil {
		maxMembers = sub.PlanDetails.MaxMembers
	} else if p, err := s.Store.GetPlan(r.Context(), sub.Plan); err == nil {
		maxMembers = p.MaxMembers
	}

	canAdd := true
	if s.Entitlements != nil {
		if err := s.Entitlements.CanAddMember(r.Context(), tenantID); err != nil {
			canAdd = false
		}
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"members":     members,
		"count":       len(members),
		"max_members": maxMembers,
		"can_add":     canAdd,
		"plan":        sub.Plan,
	})
}

// inviteTeamMember, ekibe yeni bir uye ekler / davet eder.
func (s *Server) inviteTeamMember(w http.ResponseWriter, r *http.Request) {
	tenantID, ok := s.tenantFor(r)
	if !ok {
		writeJSONError(w, http.StatusInternalServerError, "no_tenant", "istek kiraci kapsami olmadan ulasti")
		return
	}
	// Ekip yonetimi yalnizca owner/admin (member kendini admin yapamasin).
	if !s.requirePrivileged(w, r, tenantID) {
		return
	}

	var body struct {
		Email string `json:"email"`
		Name  string `json:"name"`
		Role  string `json:"role"`
	}
	if !decode(w, r, &body) {
		return
	}

	email := strings.TrimSpace(strings.ToLower(body.Email))
	if email == "" || !strings.Contains(email, "@") {
		writeJSONError(w, http.StatusUnprocessableEntity, "invalid_email", "Gecerli bir e-posta adresi girin")
		return
	}

	role := strings.TrimSpace(body.Role)
	if role != "admin" && role != "member" {
		role = "member"
	}

	// Entitlements limit kontrolu
	if s.Entitlements != nil {
		if err := s.Entitlements.CanAddMember(r.Context(), tenantID); err != nil {
			writeEntitlementError(w, err)
			return
		}
	}

	// Daveti yapan Better Auth kullanicisinin id'si (invitation.inviterId icin).
	// Admin-key ile gelen super-admin'de kullanici olmayabilir → "" gecilir.
	inviterID := ""
	if u, ok := userFromContext(r.Context()); ok && u != nil {
		inviterID = u.ID
	}

	m, err := s.Store.AddTeamMember(r.Context(), tenantID, email, strings.TrimSpace(body.Name), role, inviterID)
	if err != nil {
		if strings.Contains(err.Error(), "zaten ekibin bir uyesi") || strings.Contains(err.Error(), "zaten bekleyen bir davet") {
			writeJSONError(w, http.StatusConflict, "already_member", err.Error())
			return
		}
		s.fail(w, err)
		return
	}

	// Davet e-postasi: uyelik ancak davetli linkten kabul edince olusur.
	sent := false
	if inv, ierr := s.Store.GetInvitation(r.Context(), m.ID); ierr == nil {
		sent = s.sendInvitationEmail(inv)
	}
	m.EmailSent = &sent
	s.audit(r, "team.invite", m.ID, m.Email)

	writeJSON(w, http.StatusCreated, m)
}

// updateMemberRole, bir ekip uyesinin rolunu ('admin' / 'member') degistirir.
func (s *Server) updateMemberRole(w http.ResponseWriter, r *http.Request) {
	tenantID, ok := s.tenantFor(r)
	if !ok {
		writeJSONError(w, http.StatusInternalServerError, "no_tenant", "istek kiraci kapsami olmadan ulasti")
		return
	}
	// Ekip yonetimi yalnizca owner/admin (member kendini admin yapamasin).
	if !s.requirePrivileged(w, r, tenantID) {
		return
	}

	memberID := r.PathValue("id")
	if memberID == "" {
		writeJSONError(w, http.StatusBadRequest, "invalid_id", "uye id belirtilmedi")
		return
	}

	var body struct {
		Role string `json:"role"`
	}
	if !decode(w, r, &body) {
		return
	}

	role := strings.TrimSpace(body.Role)
	if role != "admin" && role != "member" {
		writeJSONError(w, http.StatusUnprocessableEntity, "invalid_role", "Rol yalnizca 'admin' veya 'member' olabilir")
		return
	}

	target, ok := s.guardMemberTarget(w, r, tenantID, memberID, true)
	if !ok {
		return
	}

	if err := s.Store.UpdateTeamMemberRole(r.Context(), tenantID, memberID, role); err != nil {
		s.fail(w, err)
		return
	}
	// Go oturum onbellegi rolu ~30 sn tasir; yeni rol hemen gecerli olsun.
	if s.BetterAuth != nil {
		s.BetterAuth.InvalidateUser(target.UserID)
	}
	s.audit(r, "team.role", memberID, role)

	writeJSON(w, http.StatusOK, map[string]any{"success": true})
}

// removeTeamMember, uyeyi organizasyondan cikarir ve ona ait istemcileri temizler.
func (s *Server) removeTeamMember(w http.ResponseWriter, r *http.Request) {
	tenantID, ok := s.tenantFor(r)
	if !ok {
		writeJSONError(w, http.StatusInternalServerError, "no_tenant", "istek kiraci kapsami olmadan ulasti")
		return
	}
	// Ekip yonetimi yalnizca owner/admin (member kendini admin yapamasin).
	if !s.requirePrivileged(w, r, tenantID) {
		return
	}

	memberID := r.PathValue("id")
	if memberID == "" {
		writeJSONError(w, http.StatusBadRequest, "invalid_id", "uye id belirtilmedi")
		return
	}

	target, ok := s.guardMemberTarget(w, r, tenantID, memberID, false)
	if !ok {
		return
	}

	// Cikarilan uyenin istemcileri silinecek; canli baglantilari da kapatilmali.
	var memberClients []store.Client
	if target.UserID != "" {
		memberClients, _ = s.Store.ListMemberClients(r.Context(), tenantID, target.UserID)
	}

	if err := s.Store.RemoveTeamMember(r.Context(), tenantID, memberID); err != nil {
		s.fail(w, err)
		return
	}
	// Onbellekteki oturum eski uyeligi ~30 sn tasimasin.
	if s.BetterAuth != nil {
		s.BetterAuth.InvalidateUser(target.UserID)
	}
	if s.Hub != nil {
		for _, c := range memberClients {
			if sess, ok := s.Hub.Get(c.ID); ok {
				sess.Close("member removed")
			}
		}
	}
	s.audit(r, "team.remove", memberID, target.Email)

	writeJSON(w, http.StatusOK, map[string]any{"success": true})
}

// resolveUserIDForMember, memberID veya doğrudan userID alarak user_id'yi cozer.
func (s *Server) resolveUserIDForMember(ctx context.Context, tenantID, idOrUserID string) (string, error) {
	members, err := s.Store.ListTeamMembers(ctx, tenantID)
	if err != nil {
		return "", err
	}
	for _, m := range members {
		if m.ID == idOrUserID || (m.UserID != "" && m.UserID == idOrUserID) {
			// Bekleyen davetlerde (status=pending) UserID bostur: henuz gercek bir
			// kullanici yok, dolayisiyla jeton uretilemez/listelenemez.
			if m.UserID == "" {
				return "", store.ErrNotFound
			}
			return m.UserID, nil
		}
	}
	return "", store.ErrNotFound
}

// listMemberTokens, belirli bir ekip uyesine atanmis istemci jetonlarini listeler.
func (s *Server) listMemberTokens(w http.ResponseWriter, r *http.Request) {
	tenantID, ok := s.tenantFor(r)
	if !ok {
		writeJSONError(w, http.StatusInternalServerError, "no_tenant", "istek kiraci kapsami olmadan ulasti")
		return
	}
	// Ekip yonetimi yalnizca owner/admin (member kendini admin yapamasin).
	if !s.requirePrivileged(w, r, tenantID) {
		return
	}

	memberID := r.PathValue("id")
	userID, err := s.resolveUserIDForMember(r.Context(), tenantID, memberID)
	if err != nil {
		s.fail(w, err)
		return
	}

	clients, err := s.Store.ListMemberClients(r.Context(), tenantID, userID)
	if err != nil {
		s.fail(w, err)
		return
	}
	if clients == nil {
		clients = []store.Client{}
	}
	for i := range clients {
		clients[i] = s.enrich(clients[i])
	}

	writeJSON(w, http.StatusOK, clients)
}

// createMemberToken, belirli bir ekip uyesi adina yeni bir istemci jetonu uretir.
func (s *Server) createMemberToken(w http.ResponseWriter, r *http.Request) {
	tenantID, ok := s.tenantFor(r)
	if !ok {
		writeJSONError(w, http.StatusInternalServerError, "no_tenant", "istek kiraci kapsami olmadan ulasti")
		return
	}
	// Ekip yonetimi yalnizca owner/admin (member kendini admin yapamasin).
	if !s.requirePrivileged(w, r, tenantID) {
		return
	}

	memberID := r.PathValue("id")
	userID, err := s.resolveUserIDForMember(r.Context(), tenantID, memberID)
	if err != nil {
		s.fail(w, err)
		return
	}

	var body struct {
		Name string `json:"name"`
	}
	if !decode(w, r, &body) {
		return
	}

	name := strings.TrimSpace(body.Name)
	if name == "" {
		name = "Ekip İstemcisi"
	}

	if s.Entitlements != nil {
		if err := s.Entitlements.CanCreateClient(r.Context(), tenantID); err != nil {
			writeEntitlementError(w, err)
			return
		}
	}

	full, tokenID, hash, err := auth.GenerateClient()
	if err != nil {
		s.fail(w, err)
		return
	}

	c, err := s.Store.CreateMemberClient(r.Context(), tenantID, userID, name, tokenID, hash)
	if err != nil {
		s.fail(w, err)
		return
	}

	writeJSON(w, http.StatusCreated, map[string]any{
		"client": s.enrich(c),
		"token":  full,
	})
}

// revokeMemberToken, uyeye ait belirli bir istemci jetonunu siler ve canli baglantisini sonlandirir.
func (s *Server) revokeMemberToken(w http.ResponseWriter, r *http.Request) {
	tenantID, ok := s.tenantFor(r)
	if !ok {
		writeJSONError(w, http.StatusInternalServerError, "no_tenant", "istek kiraci kapsami olmadan ulasti")
		return
	}
	// Ekip yonetimi yalnizca owner/admin (member kendini admin yapamasin).
	if !s.requirePrivileged(w, r, tenantID) {
		return
	}

	clientID := r.PathValue("client_id")
	if clientID == "" {
		writeJSONError(w, http.StatusBadRequest, "invalid_id", "istemci id belirtilmedi")
		return
	}

	// Istemci gercekten {id} uyesine ait olmali; aksi halde bu uc kiracidaki
	// HERHANGI bir istemciyi silmek icin kullanilabilirdi.
	userID, err := s.resolveUserIDForMember(r.Context(), tenantID, r.PathValue("id"))
	if err != nil {
		s.fail(w, err)
		return
	}
	if c, err := s.Store.GetClient(r.Context(), tenantID, clientID); err != nil || c.UserID != userID {
		writeJSONError(w, http.StatusNotFound, "not_found", "kayit bulunamadi")
		return
	}

	if err := s.Store.DeleteClient(r.Context(), tenantID, clientID); err != nil {
		s.fail(w, err)
		return
	}

	// Eger istemci su an bagliysa oturumu aninda kapat
	if sess, ok := s.Hub.Get(clientID); ok {
		sess.Close("token revoked")
	}

	writeJSON(w, http.StatusOK, map[string]any{"success": true})
}

// guardMemberTarget, rol degistirme/cikarma hedefini dogrular: owner'a
// dokunulamaz; selfForbidden ise kisi kendi uyeligini de degistiremez (bir
// admin'in kendini dusurup org'u yoneticisiz birakmasini da onler). Bekleyen
// davetler (pending) icin kural yok. Reddedilirse yanit yazilir ve false doner;
// kabul edilirse hedef kayit (onbellek temizligi icin UserID) doner.
func (s *Server) guardMemberTarget(w http.ResponseWriter, r *http.Request, tenantID, memberID string, selfForbidden bool) (store.TeamMember, bool) {
	members, err := s.Store.ListTeamMembers(r.Context(), tenantID)
	if err != nil {
		s.fail(w, err)
		return store.TeamMember{}, false
	}
	for _, m := range members {
		if m.ID != memberID {
			continue
		}
		if m.Role == "owner" {
			writeJSONError(w, http.StatusForbidden, "owner_protected", "organizasyon sahibinin rolu degistirilemez veya sahip cikarilamaz")
			return store.TeamMember{}, false
		}
		if selfForbidden && m.UserID != "" {
			if u, ok := userFromContext(r.Context()); ok && u != nil && u.ID == m.UserID {
				writeJSONError(w, http.StatusForbidden, "self_role_change", "kendi rolunuzu degistiremezsiniz")
				return store.TeamMember{}, false
			}
		}
		return m, true
	}
	writeJSONError(w, http.StatusNotFound, "not_found", "uye bulunamadi")
	return store.TeamMember{}, false
}
