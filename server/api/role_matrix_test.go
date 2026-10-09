package api

// Dalga 2 / F-04, F-31, F-32: rol matrisi. member okuma + olusturma yapar;
// silme, token rotate ve guvenlik/trafik ayarlari owner/admin ister; bilinmeyen
// rol hicbir sey yapamaz. Sahte store ile (Postgres gerektirmez).

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

type roleMatrixStore struct{ *orgAuthzStore }

func (m roleMatrixStore) GetMemberRole(_ context.Context, _, userID string) (string, error) {
	return map[string]string{"u_viewtok": "viewer"}[userID], nil
}

func roleReq(method, role, user string) *http.Request {
	r := httptest.NewRequest(method, "/x", strings.NewReader("{}"))
	r.SetPathValue("id", "x")
	ctx := withTenant(r.Context(), "ten_a")
	return r.WithContext(withUser(ctx, &AuthUser{ID: user, TenantID: "ten_a", Role: role}))
}

// callHandler, handler'i calistirir; store'a ulasip panik olursa kapi gecilmis
// demektir (yetki reddi 403 olmadan sonuc: 0).
func callHandler(h http.HandlerFunc, r *http.Request) (code int) {
	w := httptest.NewRecorder()
	defer func() {
		if recover() != nil {
			code = 0
		}
	}()
	h(w, r)
	return w.Code
}

func TestRoleMatrixPrivilegedRoutes(t *testing.T) {
	s := &Server{Store: roleMatrixStore{newOrgAuthzStore()}}
	routes := map[string]http.HandlerFunc{
		"policies create":    s.createPolicy,
		"policies update":    s.updatePolicy,
		"policies delete":    s.deletePolicy,
		"policies bind":      s.bindPolicy,
		"policies unbind":    s.unbindPolicy,
		"ip-allowlist post":  s.createIPRule,
		"ip-allowlist patch": s.updateIPRule,
		"ip-allowlist del":   s.deleteIPRule,
		"mtls":               s.setTunnelMTLS,
		"access":             s.setTunnelAccess,
		"traffic":            s.setTunnelTraffic,
		"lb":                 s.setTunnelLB,
		"udp":                s.setTunnelUDP,
		"door":               s.setTunnelDoor,
		"door grant revoke":  s.revokeDoorGrant,
		"alert":              s.setTunnelAlert,
		"capture":            s.setCaptureSettings,
		"device config":      s.setDeviceConfig,
		"tunnel delete":      s.deleteTunnel,
		"client delete":      s.deleteClient,
		"client token":       s.rotateToken,
		"hostname delete":    s.deleteHostname,
		"path delete":        s.deletePathRoute,
		"replica delete":     s.removeTunnelReplica,
	}
	for name, h := range routes {
		if c := callHandler(h, roleReq("POST", "member", "u_mem")); c != http.StatusForbidden {
			t.Errorf("%s: member %d, 403 bekleniyordu", name, c)
		}
		if c := callHandler(h, roleReq("POST", "viewer", "u_view")); c != http.StatusForbidden {
			t.Errorf("%s: viewer %d, 403 bekleniyordu", name, c)
		}
		for _, role := range []string{"owner", "admin"} {
			if c := callHandler(h, roleReq("POST", role, "u_"+role)); c == http.StatusForbidden {
				t.Errorf("%s: %s icin 403 (kapi gecilmeliydi)", name, role)
			}
		}
	}
}

// member olusturma uclari kapanmamali.
func TestRoleMatrixMemberCanCreate(t *testing.T) {
	s := &Server{Store: roleMatrixStore{newOrgAuthzStore()}}
	for name, h := range map[string]http.HandlerFunc{
		"project create":  s.createProject,
		"client create":   s.createClient,
		"tunnel create":   s.createTunnel,
		"hostname create": s.createHostname,
	} {
		if c := callHandler(h, roleReq("POST", "member", "u_mem")); c == http.StatusForbidden {
			t.Errorf("%s: member 403 aldi, olusturma acik kalmali", name)
		}
	}
}

// API token'in sahibinin gercek rolu bilinmiyorsa da reddedilir (F-32).
func TestUnknownRoleViaAPIToken(t *testing.T) {
	s := &Server{Store: roleMatrixStore{newOrgAuthzStore()}}
	r := roleReq("POST", "api_token", "u_viewtok")
	if c := callHandler(s.deleteClient, r); c != http.StatusForbidden {
		t.Fatalf("bilinmeyen rollu token %d, 403 bekleniyordu", c)
	}
}
