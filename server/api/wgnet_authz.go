package api

import (
	"context"
	"errors"
	"net/http"
	"strings"

	"github.com/tkodcumpeg4/zorven/server/netctl"
	"github.com/tkodcumpeg4/zorven/server/store"
)

// ACIK SURUM override: resauthz (ticari katman) yok; ayricalik owner/admin rolu.

// netCaller, istegin sahibi: ayricalikli mi (owner/admin/kiraci anahtari),
// kullanici kimligi ve rolu.
type netCallerInfo struct {
	Privileged bool
	UserID     string
	Role       string
}

func (s *Server) netCaller(r *http.Request, tenantID string) (netCallerInfo, error) {
	priv, err := s.callerPrivileged(r, tenantID)
	if err != nil {
		return netCallerInfo{}, err
	}
	info := netCallerInfo{Privileged: priv}
	if u, ok := userFromContext(r.Context()); ok && u != nil {
		info.UserID, info.Role = u.ID, u.Role
		if info.Role == "" || info.Role == "api_token" {
			if role, err := s.Store.GetMemberRole(r.Context(), tenantID, u.ID); err == nil {
				info.Role = role
			}
		}
	}
	return info, nil
}

// netDeviceAllowed: acikta kiracinin her uyesi tum cihazlara erisir.
func (s *Server) netDeviceAllowed(_ *http.Request, _, _ string) (bool, error) {
	return true, nil
}

// storeNetDirectory, netctl.Directory'nin store tabanli uygulamasi.
type storeNetDirectory struct{ st store.Store }

// NewNetDirectory, netctl.Manager icin kimlik dizinini kurar.
func NewNetDirectory(st store.Store) netctl.Directory { return storeNetDirectory{st} }

func (d storeNetDirectory) UserRole(ctx context.Context, tenantID, userID string) (string, error) {
	return d.st.GetMemberRole(ctx, tenantID, userID)
}

func (d storeNetDirectory) DeviceName(ctx context.Context, tenantID, clientID string) (string, bool, error) {
	c, err := d.st.GetClient(ctx, tenantID, clientID)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return "", false, nil
		}
		return "", false, err
	}
	return c.Name, true, nil
}

// ResolveGrantee: kullanici e-postasini / cihaz adini kimlige cevirir (bulunamazsa girdi aynen doner,
// dogrulama asamasi reddeder).
func (d storeNetDirectory) ResolveGrantee(ctx context.Context, tenantID string, g netctl.Grantee) (netctl.Grantee, error) {
	switch g.Type {
	case netctl.GranteeUser:
		if !strings.Contains(g.ID, "@") {
			return g, nil
		}
		ms, err := d.st.ListTeamMembers(ctx, tenantID)
		if err != nil {
			return g, err
		}
		for _, m := range ms {
			if m.UserID != "" && strings.EqualFold(m.Email, g.ID) {
				g.ID = m.UserID
				return g, nil
			}
		}
	case netctl.GranteeDevice:
		if _, err := d.st.GetClient(ctx, tenantID, g.ID); err == nil {
			return g, nil
		}
		cs, err := d.st.ListClients(ctx, tenantID)
		if err != nil {
			return g, err
		}
		for _, c := range cs {
			if strings.EqualFold(c.Name, g.ID) {
				g.ID = c.ID
				return g, nil
			}
		}
	}
	return g, nil
}
