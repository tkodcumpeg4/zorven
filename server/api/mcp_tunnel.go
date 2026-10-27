package api

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/tkodcumpeg4/zorven/server/ingress"
	"github.com/tkodcumpeg4/zorven/server/mcpconf"
	"github.com/tkodcumpeg4/zorven/server/store"
)

// Tunel "MCP" modu uclari (MCP M3/M4).
//
//	GET    /api/v1/tunnels/{id}/mcp                  ayar + baglanti URL'leri   (tunnels:read)
//	PUT    /api/v1/tunnels/{id}/mcp                  ayari kaydet               (tunnels:write, owner/admin)
//	GET    /api/v1/tunnels/{id}/mcp/calls            cagri logu (govde YOK)     (analytics:read)
//	GET    /api/v1/tunnels/{id}/mcp/stats            arac basina analitik       (analytics:read)
//	POST   /api/v1/tunnels/{id}/mcp/test             baglantiyi sina            (tunnels:write, owner/admin)
//	POST   /api/v1/tunnels/{id}/mcp/revoke-sessions  OAuth oturumlarini sonlandir (PANEL; token'a kapali)
//	GET/POST/DELETE .../mcp/keys[/{keyId}]           API anahtarlari            (PANEL; token'a KAPALI)
//
// Anahtar uclari ve oturum sonlandirma token yuzeyine bilerek EKLENMEZ (tokenPublicRoutes).

const mcpKeyPrefix = "zmk_"

// mcpTunnel, tuneli (kiraci kapsaminda) ve HTTP oldugunu dogrular.
func (s *Server) mcpTunnel(w http.ResponseWriter, r *http.Request) (store.Tunnel, string, bool) {
	tenantID, ok := s.tenantFor(r)
	if !ok {
		writeJSONError(w, http.StatusInternalServerError, "no_tenant", "istek kiraci kapsami olmadan ulasti")
		return store.Tunnel{}, "", false
	}
	tun, err := s.Store.GetTunnel(r.Context(), tenantID, r.PathValue("id"))
	if err != nil {
		writeJSONError(w, http.StatusNotFound, "not_found", "tunel bulunamadi")
		return store.Tunnel{}, "", false
	}
	return tun, tenantID, true
}

// mcpURLs, tunelin dogrulanmis/etkin adlari icin MCP baglanti URL'leri.
func (s *Server) mcpURLs(r *http.Request, tenantID, tunnelID, path string) []string {
	names, err := s.Store.ListHostnames(r.Context(), tenantID)
	if err != nil {
		return []string{}
	}
	out := []string{}
	for _, n := range names {
		if n.TunnelID != tunnelID || (n.Type == store.HostTypeCustom && !n.Verified) {
			continue
		}
		out = append(out, "https://"+n.FQDN+path)
	}
	return out
}

func (s *Server) getTunnelMCP(w http.ResponseWriter, r *http.Request) {
	if !requireScope(w, r, ScopeTunnelsRead) {
		return
	}
	tun, tenantID, ok := s.mcpTunnel(w, r)
	if !ok {
		return
	}
	m, err := s.Store.GetTunnelMCP(r.Context(), tenantID, tun.ID)
	if err != nil {
		s.fail(w, err)
		return
	}
	cfg := mcpconf.ParseLenient(m.Config)
	keys, _ := s.Store.ListTunnelMCPKeys(r.Context(), tenantID, tun.ID)
	writeJSON(w, http.StatusOK, map[string]any{
		"tunnel_id":       tun.ID,
		"enabled":         m.Enabled,
		"config":          cfg,
		"urls":            s.mcpURLs(r, tenantID, tun.ID, cfg.Path),
		"key_count":       len(keys),
		"oauth_available": s.MCPOAuthReady,
		"protocol":        tun.Proto,
	})
}

func (s *Server) setTunnelMCP(w http.ResponseWriter, r *http.Request) {
	if !requireScope(w, r, ScopeTunnelsWrite) {
		return
	}
	if !s.requirePrivilegedCaller(w, r) {
		return
	}
	tun, tenantID, ok := s.mcpTunnel(w, r)
	if !ok {
		return
	}
	if tun.Proto != "" && tun.Proto != store.ProtoHTTP {
		writeJSONError(w, http.StatusUnprocessableEntity, "not_http", "MCP modu yalnizca HTTP tunellerinde kullanilabilir")
		return
	}
	var body struct {
		Enabled bool            `json:"enabled"`
		Config  json.RawMessage `json:"config"`
	}
	if !decode(w, r, &body) {
		return
	}
	cfg, err := mcpconf.Parse(body.Config)
	if err != nil {
		writeJSONError(w, http.StatusUnprocessableEntity, "invalid_config", err.Error())
		return
	}
	if body.Enabled && cfg.Auth == mcpconf.AuthOAuth && !s.MCPOAuthReady {
		writeJSONError(w, http.StatusUnprocessableEntity, "oauth_unavailable",
			"Bu sunucuda OAuth yetkilendirme sunucusu etkin degil; auth=apikey kullanin")
		return
	}
	raw, _ := json.Marshal(cfg)
	if err := s.Store.SetTunnelMCP(r.Context(), tenantID, store.TunnelMCP{TunnelID: tun.ID, Enabled: body.Enabled, Config: raw}); err != nil {
		s.fail(w, err)
		return
	}
	s.tunnelsChanged()
	s.audit(r, "tunnel.mcp.update", tun.ID, "enabled="+strconv.FormatBool(body.Enabled)+" auth="+cfg.Auth+
		" allow="+strconv.Itoa(len(cfg.AllowTools))+" deny="+strconv.Itoa(len(cfg.DenyTools))+" rate="+strconv.Itoa(cfg.RateLimitPerMin))
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "enabled": body.Enabled, "config": cfg,
		"urls": s.mcpURLs(r, tenantID, tun.ID, cfg.Path)})
}

// --- anahtarlar -----------------------------------------------------------------

func (s *Server) listMCPKeys(w http.ResponseWriter, r *http.Request) {
	if !requireScope(w, r, ScopeTunnelsRead) {
		return
	}
	tun, tenantID, ok := s.mcpTunnel(w, r)
	if !ok {
		return
	}
	keys, err := s.Store.ListTunnelMCPKeys(r.Context(), tenantID, tun.ID)
	if err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, keys)
}

func (s *Server) createMCPKey(w http.ResponseWriter, r *http.Request) {
	if !requireScope(w, r, ScopeTunnelsWrite) {
		return
	}
	if !s.requirePrivilegedCaller(w, r) {
		return
	}
	tun, tenantID, ok := s.mcpTunnel(w, r)
	if !ok {
		return
	}
	var body struct {
		Label     string `json:"label"`
		ExpiresIn int    `json:"expires_in_days"`
	}
	if !decode(w, r, &body) {
		return
	}
	label := strings.TrimSpace(body.Label)
	if label == "" || len(label) > 64 || strings.ContainsAny(label, "\r\n\x00") {
		writeJSONError(w, http.StatusUnprocessableEntity, "invalid_label", "label zorunlu (en fazla 64 karakter, satir sonu yok)")
		return
	}
	if body.ExpiresIn < 0 || body.ExpiresIn > 3650 {
		writeJSONError(w, http.StatusUnprocessableEntity, "invalid_expiry", "expires_in_days 0..3650 olmali")
		return
	}
	buf := make([]byte, 24)
	if _, err := rand.Read(buf); err != nil {
		s.fail(w, err)
		return
	}
	key := mcpKeyPrefix + base64.RawURLEncoding.EncodeToString(buf)
	sum := sha256.Sum256([]byte(key))
	k := store.MCPKey{ID: "mk_" + hex.EncodeToString(buf[:8]), TunnelID: tun.ID, Label: label,
		Prefix: key[:8], KeyHash: hex.EncodeToString(sum[:])}
	if body.ExpiresIn > 0 {
		t := time.Now().UTC().Add(time.Duration(body.ExpiresIn) * 24 * time.Hour)
		k.ExpiresAt = &t
	}
	if err := s.Store.CreateTunnelMCPKey(r.Context(), tenantID, k); err != nil {
		if errors.Is(err, store.ErrMCPKeyLimit) {
			writeJSONError(w, http.StatusConflict, "key_limit", "tunel basina en fazla 20 anahtar")
			return
		}
		s.fail(w, err)
		return
	}
	s.tunnelsChanged()
	s.audit(r, "tunnel.mcp.key.create", tun.ID, "key="+k.ID+" label="+label)
	// Ham anahtar YALNIZCA bu yanitta doner.
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusCreated, map[string]any{"id": k.ID, "label": label, "prefix": k.Prefix, "key": key, "expires_at": k.ExpiresAt})
}

func (s *Server) deleteMCPKey(w http.ResponseWriter, r *http.Request) {
	if !requireScope(w, r, ScopeTunnelsWrite) {
		return
	}
	if !s.requirePrivilegedCaller(w, r) {
		return
	}
	tun, tenantID, ok := s.mcpTunnel(w, r)
	if !ok {
		return
	}
	if err := s.Store.DeleteTunnelMCPKey(r.Context(), tenantID, tun.ID, r.PathValue("keyId")); err != nil {
		s.fail(w, err)
		return
	}
	s.tunnelsChanged()
	s.audit(r, "tunnel.mcp.key.delete", tun.ID, "key="+r.PathValue("keyId"))
	w.WriteHeader(http.StatusNoContent)
}

// --- log / analitik -------------------------------------------------------------

var mcpWindows = map[string]time.Duration{
	"1h": time.Hour, "24h": 24 * time.Hour, "7d": 7 * 24 * time.Hour, "30d": 30 * 24 * time.Hour,
}

func (s *Server) listMCPCalls(w http.ResponseWriter, r *http.Request) {
	if !requireScope(w, r, ScopeAnalyticsRead) {
		return
	}
	tun, tenantID, ok := s.mcpTunnel(w, r)
	if !ok {
		return
	}
	q := r.URL.Query()
	f := store.MCPCallFilter{Tool: strings.TrimSpace(q.Get("tool")), Status: strings.TrimSpace(q.Get("status"))}
	switch f.Status {
	case "", "ok", "error", "denied", "rate_limited":
	default:
		writeJSONError(w, http.StatusUnprocessableEntity, "invalid_status", "status ok|error|denied|rate_limited olmali")
		return
	}
	f.Limit, _ = strconv.Atoi(q.Get("limit"))
	f.Offset, _ = strconv.Atoi(q.Get("offset"))
	if d, ok := mcpWindows[q.Get("window")]; ok {
		f.Since = time.Now().Add(-d)
	}
	calls, err := s.Store.ListMCPCalls(r.Context(), tenantID, tun.ID, f)
	if err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, calls)
}

func (s *Server) getMCPStats(w http.ResponseWriter, r *http.Request) {
	if !requireScope(w, r, ScopeAnalyticsRead) {
		return
	}
	tun, tenantID, ok := s.mcpTunnel(w, r)
	if !ok {
		return
	}
	win := r.URL.Query().Get("window")
	d, ok := mcpWindows[win]
	if !ok {
		win, d = "24h", mcpWindows["24h"]
	}
	tools, err := s.Store.MCPToolStats(r.Context(), tenantID, tun.ID, time.Now().Add(-d))
	if err != nil {
		s.fail(w, err)
		return
	}
	var calls, errs, denied int64
	for _, t := range tools {
		calls += t.Calls
		errs += t.Errors
		denied += t.Denied
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"window": win, "tools": tools,
		"totals": map[string]int64{"calls": calls, "errors": errs, "denied": denied},
	})
}

// --- baglantiyi sina / oturumlari sonlandir ---------------------------------------

func (s *Server) testTunnelMCP(w http.ResponseWriter, r *http.Request) {
	if !requireScope(w, r, ScopeTunnelsWrite) {
		return
	}
	if !s.requirePrivilegedCaller(w, r) {
		return
	}
	tun, tenantID, ok := s.mcpTunnel(w, r)
	if !ok {
		return
	}
	m, err := s.Store.GetTunnelMCP(r.Context(), tenantID, tun.ID)
	if err != nil {
		s.fail(w, err)
		return
	}
	cfg := mcpconf.ParseLenient(m.Config)
	var res ingress.MCPProbeResult
	if s.MCPProbe != nil && m.Enabled {
		res = s.MCPProbe(r.Context(), tun.ID)
	}
	keys, _ := s.Store.ListTunnelMCPKeys(r.Context(), tenantID, tun.ID)
	checks := map[string]any{
		"mcp_enabled":     m.Enabled,
		"agent_online":    res.AgentOnline,
		"oauth_available": s.MCPOAuthReady,
		"auth_ready":      cfg.Auth == mcpconf.AuthNone || (cfg.Auth == mcpconf.AuthOAuth && s.MCPOAuthReady) || (cfg.Auth == mcpconf.AuthAPIKey && len(keys) > 0),
		"has_url":         len(s.mcpURLs(r, tenantID, tun.ID, cfg.Path)) > 0,
	}
	writeJSON(w, http.StatusOK, map[string]any{"auth": cfg.Auth, "checks": checks, "probe": res})
}

func (s *Server) revokeMCPSessions(w http.ResponseWriter, r *http.Request) {
	if !requireScope(w, r, ScopeTunnelsWrite) {
		return
	}
	if !s.requirePrivilegedCaller(w, r) {
		return
	}
	tun, tenantID, ok := s.mcpTunnel(w, r)
	if !ok {
		return
	}
	m, err := s.Store.GetTunnelMCP(r.Context(), tenantID, tun.ID)
	if err != nil {
		s.fail(w, err)
		return
	}
	cfg := mcpconf.ParseLenient(m.Config)
	n := 0
	if s.MCPRevokeGrants != nil {
		var keysToRevoke []string
		for _, u := range s.mcpURLs(r, tenantID, tun.ID, cfg.Path) {
			if k, ok := mcpconf.ResourceKey(u); ok {
				keysToRevoke = append(keysToRevoke, k)
			}
		}
		n, err = s.MCPRevokeGrants(r.Context(), tenantID, keysToRevoke)
		if err != nil {
			s.fail(w, err)
			return
		}
	}
	s.audit(r, "tunnel.mcp.revoke_sessions", tun.ID, "revoked="+strconv.Itoa(n))
	writeJSON(w, http.StatusOK, map[string]any{"revoked": n})
}
