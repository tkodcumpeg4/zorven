package api

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/tkodcumpeg4/zorven/server/auth"
	"github.com/tkodcumpeg4/zorven/server/entitlements"
	"github.com/tkodcumpeg4/zorven/server/events"
	"github.com/tkodcumpeg4/zorven/server/ipfilter"
	"github.com/tkodcumpeg4/zorven/server/reqlog"
	"github.com/tkodcumpeg4/zorven/server/store"
	"github.com/tkodcumpeg4/zorven/server/store/pgstore"
	"github.com/tkodcumpeg4/zorven/server/tunnel"
)

func TestAPITokensAndIPAllowlist_Lifecycle(t *testing.T) {
	dsn := testE2EDSNDatabase(t)
	ctx := context.Background()

	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatalf("pgxpool.New: %v", err)
	}
	defer pool.Close()

	st, err := pgstore.Open(ctx, dsn)
	if err != nil {
		t.Fatalf("pgstore.Open: %v", err)
	}
	defer st.Close()

	tenantID := "org_token_test_org"
	ownerUserID := "usr_token_test_owner"
	sessionToken := "tok_token_test_session"

	// 1. Temizlik
	_, _ = pool.Exec(ctx, `DELETE FROM subscriptions WHERE tenant_id = $1`, tenantID)
	_, _ = pool.Exec(ctx, `DELETE FROM ip_allowlist_rules WHERE tenant_id = $1`, tenantID)
	_, _ = pool.Exec(ctx, `DELETE FROM api_tokens WHERE tenant_id = $1`, tenantID)
	_, _ = pool.Exec(ctx, `DELETE FROM clients WHERE tenant_id = $1`, tenantID)
	_, _ = pool.Exec(ctx, `DELETE FROM "session" WHERE "token" = $1`, sessionToken)
	_, _ = pool.Exec(ctx, `DELETE FROM "member" WHERE "organizationId" = $1`, tenantID)
	_, _ = pool.Exec(ctx, `DELETE FROM "organization" WHERE "id" = $1`, tenantID)
	_, _ = pool.Exec(ctx, `DELETE FROM "user" WHERE "id" = $1`, ownerUserID)
	_, _ = pool.Exec(ctx, `DELETE FROM "tenants" WHERE "id" = $1`, tenantID)

	// 2. Org & User ekle
	_, err = pool.Exec(ctx, `
		INSERT INTO "user" ("id", "name", "email", "createdAt", "updatedAt")
		VALUES ($1, 'Token Tester', 'tester@tokens.corp', now(), now())
	`, ownerUserID)
	if err != nil {
		t.Fatalf("insert user: %v", err)
	}

	_, err = pool.Exec(ctx, `
		INSERT INTO "organization" ("id", "name", "slug", "createdAt")
		VALUES ($1, 'Token Corp', 'token-corp', now())
	`, tenantID)
	if err != nil {
		t.Fatalf("insert org: %v", err)
	}

	_, err = pool.Exec(ctx, `
		INSERT INTO "member" ("id", "organizationId", "userId", "role", "createdAt")
		VALUES ('mem_token_tester', $1, $2, 'owner', now())
	`, tenantID, ownerUserID)
	if err != nil {
		t.Fatalf("insert member: %v", err)
	}

	// 3. Pro plan tanimla (has_api_access = true, has_ip_allowlist = true)
	_, err = pool.Exec(ctx, `
		INSERT INTO subscriptions (tenant_id, plan, status, max_clients, max_custom_domains, max_tunnels, bandwidth_limit_bytes, created_at, updated_at)
		VALUES ($1, 'pro', 'active', 15, 5, 20, 107374182400, now(), now())
	`, tenantID)
	if err != nil {
		t.Fatalf("insert subscription: %v", err)
	}

	// 4. Better Auth oturumu
	expiresAt := time.Now().Add(24 * time.Hour)
	_, err = pool.Exec(ctx, `
		INSERT INTO "session" ("id", "token", "userId", "activeOrganizationId", "expiresAt", "createdAt", "updatedAt")
		VALUES ('sess_token_test', $1, $2, $3, $4, now(), now())
	`, sessionToken, ownerUserID, tenantID, expiresAt)
	if err != nil {
		t.Fatalf("insert session: %v", err)
	}

	entSvc := entitlements.NewService(st)
	ipEngine := ipfilter.NewEngine(st, entSvc)
	_ = ipEngine.Reload(ctx)

	srv := &Server{
		Store:        st,
		Hub:          tunnel.NewHub(),
		Log:          reqlog.New(100),
		Events:       events.New(),
		Tickets:      NewTicketStore(),
		Entitlements: entSvc,
		IPFilter:     ipEngine,
	}

	mux := http.NewServeMux()
	mux.Handle("/api/v1/", srv.Routes())

	betterAuthVerifier := auth.NewBetterAuthVerifier(pool, 1*time.Minute)
	guarded := &Middleware{
		Next:         mux,
		Store:        st,
		Entitlements: entSvc,
		BetterAuth:   betterAuthVerifier,
		PublicPaths:  map[string]bool{"/api/v1/health": true},
	}

	// Helper for session requests
	doReq := func(method, path string, body any, bearerToken string) *httptest.ResponseRecorder {
		var buf bytes.Buffer
		if body != nil {
			_ = json.NewEncoder(&buf).Encode(body)
		}
		req := httptest.NewRequest(method, path, &buf)
		req.Header.Set("Content-Type", "application/json")
		if bearerToken != "" {
			req.Header.Set("Authorization", "Bearer "+bearerToken)
		} else {
			req.AddCookie(&http.Cookie{Name: "better-auth.session_token", Value: sessionToken})
		}
		w := httptest.NewRecorder()
		guarded.ServeHTTP(w, req)
		return w
	}

	var issuedToken string
	var tokenID string

	// --- 1. REST API Token Testleri ---
	t.Run("Create API Token", func(t *testing.T) {
		w := doReq("POST", "/api/v1/api-tokens", map[string]any{
			"name":            "CI Deployment Key",
			"scopes":          []string{"read", "write"},
			"expires_in_days": 30,
		}, "")
		if w.Code != http.StatusCreated {
			t.Fatalf("status %d, body: %s", w.Code, w.Body.String())
		}
		var res struct {
			Token    string         `json:"token"`
			APIToken store.APIToken `json:"api_token"`
		}
		if err := json.Unmarshal(w.Body.Bytes(), &res); err != nil {
			t.Fatalf("unmarshal: %v", err)
		}
		if !strings.HasPrefix(res.Token, "zrv_api_") {
			t.Errorf("token zrv_api_ ile baslamali: %s", res.Token)
		}
		if res.APIToken.Name != "CI Deployment Key" {
			t.Errorf("name mismatch: %s", res.APIToken.Name)
		}
		issuedToken = res.Token
		tokenID = res.APIToken.ID
	})

	t.Run("List API Tokens", func(t *testing.T) {
		w := doReq("GET", "/api/v1/api-tokens", nil, "")
		if w.Code != http.StatusOK {
			t.Fatalf("status %d, body: %s", w.Code, w.Body.String())
		}
		var res struct {
			Tokens []store.APIToken `json:"tokens"`
			Count  int              `json:"count"`
		}
		if err := json.Unmarshal(w.Body.Bytes(), &res); err != nil {
			t.Fatalf("unmarshal: %v", err)
		}
		if res.Count < 1 {
			t.Fatalf("en az 1 token bekleniyordu, alinan: %d", res.Count)
		}
	})

	t.Run("Authenticate With API Token", func(t *testing.T) {
		// Programatik token ile /api/v1/clients cagrisi yap (cerez olmadan!)
		w := doReq("GET", "/api/v1/clients", nil, issuedToken)
		if w.Code != http.StatusOK {
			t.Fatalf("API token ile erisim basarisiz: status %d, body: %s", w.Code, w.Body.String())
		}
	})

	t.Run("Revoke API Token", func(t *testing.T) {
		w := doReq("DELETE", "/api/v1/api-tokens/"+tokenID, nil, "")
		if w.Code != http.StatusNoContent {
			t.Fatalf("status %d, body: %s", w.Code, w.Body.String())
		}

		// Iptal edilen token ile cagrildiginda 401 donmeli
		wAuth := doReq("GET", "/api/v1/clients", nil, issuedToken)
		if wAuth.Code != http.StatusUnauthorized {
			t.Fatalf("iptal edilen token reddedilmeliydi, status: %d", wAuth.Code)
		}
	})

	// --- 2. Ingress IP Allowlist Testleri ---
	var ruleID string

	t.Run("Create IP Rule", func(t *testing.T) {
		w := doReq("POST", "/api/v1/ip-allowlist", map[string]any{
			"cidr":        "192.168.1.100",
			"description": "Ofis Sabit IP",
		}, "")
		if w.Code != http.StatusCreated {
			t.Fatalf("status %d, body: %s", w.Code, w.Body.String())
		}
		var rule store.IPAllowlistRule
		if err := json.Unmarshal(w.Body.Bytes(), &rule); err != nil {
			t.Fatalf("unmarshal: %v", err)
		}
		if rule.CIDR != "192.168.1.100/32" {
			t.Errorf("cidr normalize edilmeliydi: %s", rule.CIDR)
		}
		ruleID = rule.ID
	})

	t.Run("List IP Rules", func(t *testing.T) {
		w := doReq("GET", "/api/v1/ip-allowlist", nil, "")
		if w.Code != http.StatusOK {
			t.Fatalf("status %d, body: %s", w.Code, w.Body.String())
		}
		var res struct {
			Rules []store.IPAllowlistRule `json:"rules"`
			Count int                     `json:"count"`
		}
		if err := json.Unmarshal(w.Body.Bytes(), &res); err != nil {
			t.Fatalf("unmarshal: %v", err)
		}
		if res.Count < 1 {
			t.Fatalf("en az 1 kural bekleniyordu, alinan: %d", res.Count)
		}
	})

	t.Run("Update IP Rule (Disable)", func(t *testing.T) {
		disabled := false
		w := doReq("PATCH", "/api/v1/ip-allowlist/"+ruleID, map[string]any{
			"enabled": &disabled,
		}, "")
		if w.Code != http.StatusOK {
			t.Fatalf("status %d, body: %s", w.Code, w.Body.String())
		}
	})

	t.Run("Delete IP Rule", func(t *testing.T) {
		w := doReq("DELETE", "/api/v1/ip-allowlist/"+ruleID, nil, "")
		if w.Code != http.StatusNoContent {
			t.Fatalf("status %d, body: %s", w.Code, w.Body.String())
		}
	})
}
