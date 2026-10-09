package api

import (
	"context"
	"net/http"

	"github.com/tkodcumpeg4/zorven/server/session"
)

// sessionSubject, session_epochs anahtari: kiraci + kullanici.
func sessionSubject(tenantID, userID string) string { return tenantID + ":" + userID }

// resolveLegacySession, dogrulanmis imzali cerezi HER ISTEKTE DB'ye karsi
// yeniden degerlendirir ve gercek kullanici baglamini uretir. Ok=false ise
// cerez gecersizdir (istek 401 almali):
//   - iptal sayaci cerezdekinden buyukse (logout)
//   - github: kullanici silinmis / kiraciya uye degilse; rol DB'deki GERCEK roldur
func (m *Middleware) resolveLegacySession(ctx context.Context, sess session.Session) (*AuthUser, bool) {
	if m.Store == nil {
		return nil, false
	}
	ep, err := m.Store.GetSessionEpoch(ctx, sessionSubject(sess.TenantID, sess.UserID))
	if err != nil || ep != sess.Epoch {
		return nil, false
	}
	switch sess.Method {
	case "github":
		role, err := m.Store.GetLegacyMemberRole(ctx, sess.TenantID, sess.UserID)
		if err != nil || role == "" {
			return nil, false
		}
		return &AuthUser{ID: sess.UserID, Email: sess.Login, Name: sess.Login,
			TenantID: sess.TenantID, Role: role}, true
	}
	return nil, false
}

// revokeLegacySession, istekteki imzali cerezi sunucu tarafinda iptal eder
// (logout). Cerez gecersizse veya Store yoksa sessizce gecer.
func (s *Server) revokeLegacySession(r *http.Request) {
	if s.Sessions == nil || s.Store == nil {
		return
	}
	c, err := r.Cookie(session.CookieName)
	if err != nil {
		return
	}
	sess, err := s.Sessions.Verify(c.Value)
	if err != nil {
		return
	}
	if err := s.Store.BumpSessionEpoch(r.Context(), sessionSubject(sess.TenantID, sess.UserID)); err != nil && s.Logger != nil {
		s.Logger.Warn("oturum iptal edilemedi", "hata", err)
	}
}
