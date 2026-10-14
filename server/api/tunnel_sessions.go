package api

import (
	"context"
	"net/http"
	"strings"
	"time"

	"github.com/tkodcumpeg4/zorven/server/accesslog"
	"github.com/tkodcumpeg4/zorven/server/door"
)

// recordSessionAccessEvent, ziyaretci oturumu yonetim olayini (TTL degisimi / iptal)
// tunnel_access_events'e yazar (denetim izi). Identity = islemi yapan; iptalde
// "actor -> hedef kimlik".
func (s *Server) recordSessionAccessEvent(r *http.Request, tenantID, tunnelID, method, reason, target string) {
	if s.Store == nil {
		return
	}
	identity := auditActor(r)
	if target != "" {
		identity += " -> " + target
	}
	ev := accesslog.Event{
		ID: accesslog.NewID(), CreatedAt: time.Now().UTC(),
		TenantID: tenantID, TunnelID: tunnelID, Method: method,
		Identity: identity, Success: true, Reason: reason,
		ClientIP: clientIP(r), UserAgent: accesslog.TruncateUA(r.UserAgent()),
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = s.Store.InsertAccessEvents(ctx, []accesslog.Event{ev})
}

// revokeVisitorSessions, tunelin ziyaretci (Basic/OAuth) oturumlarini sonlandirir.
// Govde {"identity": "..."} verilirse yalniz o kimlik (e-posta / kullanici adi),
// bos ise tunelin TUM ziyaretci oturumlari ("*" iptal kaydi). Iptal kaydindan
// ONCE verilmis cerezler gecersiz olur; sonraki girisler etkilenmez.
func (s *Server) revokeVisitorSessions(w http.ResponseWriter, r *http.Request) {
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
	var body struct {
		Identity string `json:"identity"`
	}
	if r.ContentLength != 0 && !decode(w, r, &body) {
		return
	}
	tun, err := s.Store.GetTunnel(r.Context(), tenantID, r.PathValue("id"))
	if err != nil {
		s.fail(w, err)
		return
	}
	ident := door.NormIdentity(body.Identity)
	if len(ident) > 200 {
		writeJSONError(w, http.StatusUnprocessableEntity, "invalid_identity", "identity cok uzun")
		return
	}
	reason, target, scope := accesslog.ReasonSessionRevokedForUser, ident, "identity"
	if ident == "" || ident == door.AllIdentities {
		ident, reason, target, scope = door.AllIdentities, accesslog.ReasonSessionsRevokedAll, "", "all"
	}
	if s.Door != nil {
		err = s.Door.RecordRevocation(r.Context(), tun.ID, ident)
	} else {
		err = s.Store.RecordDoorRevocation(r.Context(), tun.ID, ident, time.Now().UTC())
	}
	if err != nil {
		s.fail(w, err)
		return
	}
	method := ""
	if pol, perr := s.Store.GetTunnelAccessPolicy(r.Context(), tenantID, tun.ID); perr == nil {
		method = pol.Mode
	}
	if method != accesslog.MethodBasic && method != accesslog.MethodOAuth {
		method = ""
	}
	s.audit(r, "tunnel.access.revoke_sessions", tun.ID, strings.TrimSpace(scope+" "+target))
	s.recordSessionAccessEvent(r, tenantID, tun.ID, method, reason, target)
	writeJSON(w, http.StatusOK, map[string]any{"revoked": true, "scope": scope, "identity": target})
}
