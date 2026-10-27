package oauthas

import (
	"context"
	"errors"
	"net/http"
	"strings"

	"github.com/tkodcumpeg4/zorven/server/mcpconf"
)

// ResourceInfo, bir MCP tunel kaynaginin (kullanicinin kendi MCP sunucusu)
// yetkilendirme icin gereken ozeti. Resolver tarafindan doldurulur.
type ResourceInfo struct {
	TenantID string
	TunnelID string
	Host     string
	// Auth, tunelin MCP kimlik dogrulama modu; yalniz "oauth" bu AS ile kullanilabilir.
	Auth            string
	AllowOrgMembers bool
	AllowedEmails   []string
}

// canonResourceURL, kaynak URL'sini kanonik hale getirir (kucuk harf host, sondaki
// '/' yok). Ayristirilamazsa girdiyi kirpilmis haliyle doner.
func canonResourceURL(raw string) string {
	raw = strings.TrimSpace(raw)
	key, ok := mcpconf.ResourceKey(raw)
	if !ok {
		return strings.TrimRight(raw, "/")
	}
	scheme := "https"
	if strings.HasPrefix(raw, "http://") {
		scheme = "http"
	}
	return scheme + "://" + key
}

// resolveTunnel, bir resource URL'sini MCP tunel kaynagina cozer.
func (s *Server) resolveTunnel(ctx context.Context, res string) (ResourceInfo, string, bool) {
	if s.cfg.ResolveResource == nil {
		return ResourceInfo{}, "", false
	}
	key, ok := mcpconf.ResourceKey(res)
	if !ok {
		return ResourceInfo{}, "", false
	}
	// Uretimde yalnizca https; http yalnizca issuer da http ise (gelistirme).
	if strings.HasPrefix(res, "http://") && !strings.HasPrefix(s.cfg.Issuer, "http://") {
		return ResourceInfo{}, "", false
	}
	info, ok := s.cfg.ResolveResource(ctx, key)
	if !ok || info.Auth != mcpconf.AuthOAuth || info.TenantID == "" {
		return ResourceInfo{}, "", false
	}
	return info, canonResourceURL(res), true
}

// userAllowed, kullanicinin tunel kaynagina izinli olup olmadigi (uye veya izinli e-posta).
func (s *Server) userAllowed(ctx context.Context, info ResourceInfo, userID string) (bool, string, error) {
	if s.cfg.UserInfo == nil {
		return false, "", errors.New("oauthas: UserInfo yapilandirilmamis")
	}
	email, member, err := s.cfg.UserInfo(ctx, info.TenantID, userID)
	if err != nil {
		return false, "", err
	}
	if info.AllowOrgMembers && member {
		return true, email, nil
	}
	return mcpconf.EmailListed(info.AllowedEmails, email), email, nil
}

// ResourceClaims, bir kaynak (tunel) token'inin dogrulanmis talepleri.
type ResourceClaims struct {
	Subject    string
	TenantID   string
	GrantID    string
	ClientID   string
	ClientName string
	Scope      string
}

// VerifyResource, bir TUNEL kaynak token'ini dogrular: imza, iss (ayarliysa), exp,
// aud == resourceKey (sema-duyarsiz "host/yol"), grant'in iptal edilmemis ve
// kapsaminin "mcp"yi icerdigi. Kaynakla eslesmeyen (baska tunel/yonetim) token
// reddedilir.
func (s *Server) VerifyResource(_ *http.Request, raw, resourceKey string) (ResourceClaims, error) {
	c, err := parseJWT(s.pub, raw)
	if err != nil {
		return ResourceClaims{}, err
	}
	now := s.cfg.Now()
	k, ok := mcpconf.ResourceKey(c.Aud)
	if !ok || k != resourceKey || c.Exp <= now.Unix()-5 || c.Gid == "" || c.Sub == "" || c.Tid == "" {
		return ResourceClaims{}, ErrInvalidToken
	}
	if s.cfg.Issuer != "" && c.Iss != strings.TrimRight(s.cfg.Issuer, "/") {
		return ResourceClaims{}, ErrInvalidToken
	}
	g, err := s.grantCached(context.Background(), c.Gid, now)
	if err != nil || !g.Active() || g.UserID != c.Sub || g.TenantID != c.Tid || g.Resource != c.Aud {
		return ResourceClaims{}, ErrInvalidToken
	}
	scopes, unknown := ParseScopes(c.Scope)
	granted, _ := ParseScopes(g.Scope)
	if unknown != "" || !subsetOf(scopes, granted) || !hasScope(scopes, ScopeTunnelAccess) {
		return ResourceClaims{}, ErrInvalidToken
	}
	return ResourceClaims{Subject: c.Sub, TenantID: c.Tid, GrantID: c.Gid, ClientID: c.Cid,
		ClientName: g.ClientName, Scope: c.Scope}, nil
}

func hasScope(list []string, want string) bool {
	for _, s := range list {
		if s == want {
			return true
		}
	}
	return false
}

// SignUpstream, upstream sunucusuna iletilecek kisa omurlu imzali kimligi uretir.
func (s *Server) SignUpstream(id UpstreamIdentity) (string, error) {
	iss := strings.TrimRight(s.cfg.Issuer, "/")
	return signUpstream(s.priv, iss, id, s.cfg.Now(), randToken(12))
}

// RevokeByResource, bir kiracinin verilen kaynak anahtarlarina (ornegin
// "acme.zorven.app/mcp") bagli TUM aktif yetkilerini iptal eder; iptal sayisini doner.
func (s *Server) RevokeByResource(ctx context.Context, tenantID string, resourceKeys []string) (int, error) {
	want := map[string]bool{}
	for _, k := range resourceKeys {
		want[k] = true
	}
	if len(want) == 0 {
		return 0, nil
	}
	list, err := s.cfg.Store.ListGrants(ctx, "", tenantID)
	if err != nil {
		return 0, err
	}
	now := s.cfg.Now()
	n := 0
	for _, g := range list {
		if k, ok := mcpconf.ResourceKey(g.Resource); !ok || !want[k] {
			continue
		}
		if err := s.cfg.Store.RevokeGrant(ctx, g.ID, now); err != nil {
			return n, err
		}
		s.forget(g.ID)
		s.audit(g.TenantID, "tunnel-owner", "", "oauth.revoke", g.ID, "client="+g.ClientID+" via=tunnel_mcp")
		n++
	}
	return n, nil
}
