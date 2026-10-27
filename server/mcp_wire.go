package main

import (
	"context"
	"net/http"
	"strings"

	"github.com/tkodcumpeg4/zorven/server/ingress"
	"github.com/tkodcumpeg4/zorven/server/mcpconf"
	"github.com/tkodcumpeg4/zorven/server/oauthas"
)

// mcpTokenAdapter, oauthas.Server'i ingress.MCPVerifier arayuzune uyarlar.
type mcpTokenAdapter struct{ srv *oauthas.Server }

func (a mcpTokenAdapter) VerifyResource(r *http.Request, token, key string) (ingress.MCPClaims, error) {
	c, err := a.srv.VerifyResource(r, token, key)
	if err != nil {
		return ingress.MCPClaims{}, err
	}
	return ingress.MCPClaims{Subject: c.Subject, TenantID: c.TenantID, GrantID: c.GrantID,
		ClientID: c.ClientID, ClientName: c.ClientName, Scope: c.Scope}, nil
}

func (a mcpTokenAdapter) SignUpstream(id ingress.MCPUpstreamIdentity) (string, error) {
	return a.srv.SignUpstream(oauthas.UpstreamIdentity{Subject: id.Subject, Email: id.Email, TenantID: id.TenantID,
		TunnelID: id.TunnelID, ClientID: id.ClientID, Scope: id.Scope, AuthMethod: id.AuthMethod})
}

// mcpResolveResource, "host/yol" kaynak anahtarini MCP tunel ayarina cozer.
func mcpResolveResource(router *ingress.Router) func(ctx context.Context, key string) (oauthas.ResourceInfo, bool) {
	return func(_ context.Context, key string) (oauthas.ResourceInfo, bool) {
		host, path, _ := strings.Cut(key, "/")
		path = "/" + path
		tenantID, tunnelID, cfg, ok := router.MCPResource(host, path)
		if !ok || cfg.Path != path && !(cfg.Path == "/" && path == "/") {
			return oauthas.ResourceInfo{}, false
		}
		return oauthas.ResourceInfo{TenantID: tenantID, TunnelID: tunnelID, Host: host, Auth: cfg.Auth,
			AllowOrgMembers: cfg.AllowOrgMembers, AllowedEmails: cfg.AllowedEmails}, true
	}
}

var _ = mcpconf.AuthOAuth
