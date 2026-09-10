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
	"github.com/tkodcumpeg4/zorven/server/reqlog"
	"github.com/tkodcumpeg4/zorven/server/store"
	"github.com/tkodcumpeg4/zorven/server/store/pgstore"
	"github.com/tkodcumpeg4/zorven/server/tunnel"
)

func TestTeam_API_Lifecycle(t *testing.T) {
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

	// 1. Temizlik
	tenantID := "org_team_test_org"
	ownerUserID := "usr_team_test_owner"
	sessionToken := "tok_team_test_session"

	_, _ = pool.Exec(ctx, `DELETE FROM subscriptions WHERE tenant_id = $1`, tenantID)
	_, _ = pool.Exec(ctx, `DELETE FROM clients WHERE tenant_id = $1`, tenantID)
	_, _ = pool.Exec(ctx, `DELETE FROM "session" WHERE "token" = $1`, sessionToken)
	_, _ = pool.Exec(ctx, `DELETE FROM "member" WHERE "organizationId" = $1`, tenantID)
	_, _ = pool.Exec(ctx, `DELETE FROM "organization" WHERE "id" = $1`, tenantID)
	_, _ = pool.Exec(ctx, `DELETE FROM "user" WHERE "id" LIKE 'usr_team_test%'`)
	_, _ = pool.Exec(ctx, `DELETE FROM "tenants" WHERE "id" = $1`, tenantID)

	// 2. Org, User, Subscription ve Owner Member olustur
	_, err = pool.Exec(ctx, `
		INSERT INTO "user" ("id", "name", "email", "createdAt", "updatedAt")
		VALUES ($1, 'Team Leader', 'owner@team.corp', now(), now())
	`, ownerUserID)
	if err != nil {
		t.Fatalf("insert owner: %v", err)
	}

	_, err = pool.Exec(ctx, `
		INSERT INTO "organization" ("id", "name", "slug", "createdAt")
		VALUES ($1, 'TeamCorp', 'teamcorp', now())
	`, tenantID)
	if err != nil {
		t.Fatalf("insert org: %v", err)
	}

	_, err = pool.Exec(ctx, `
		INSERT INTO subscriptions (tenant_id, plan, status, created_at, updated_at)
		VALUES ($1, 'team', 'active', now(), now())
		ON CONFLICT (tenant_id) DO UPDATE SET plan = 'team', status = 'active'
	`, tenantID)
	if err != nil {
		t.Fatalf("insert subscription: %v", err)
	}

	ownerMemberID := "mem_team_test_owner"
	_, err = pool.Exec(ctx, `
		INSERT INTO "member" ("id", "organizationId", "userId", "role", "createdAt")
		VALUES ($1, $2, $3, 'owner', now())
	`, ownerMemberID, tenantID, ownerUserID)
	if err != nil {
		t.Fatalf("insert member: %v", err)
	}

	_, err = pool.Exec(ctx, `
		INSERT INTO "session" ("id", "token", "userId", "activeOrganizationId", "expiresAt", "createdAt", "updatedAt")
		VALUES ('sess_team_test', $1, $2, $3, now() + interval '1 day', now(), now())
	`, sessionToken, ownerUserID, tenantID)
	if err != nil {
		t.Fatalf("insert session: %v", err)
	}

	// 3. Server kurulumu
	baVerifier := auth.NewBetterAuthVerifier(pool, 5*time.Second)
	entService := entitlements.NewService(st)
	srv := &Server{
		Store:        st,
		Hub:          tunnel.NewHub(),
		Events:       events.New(),
		Log:          reqlog.New(100),
		BetterAuth:   baVerifier,
		Entitlements: entService,
		Version:      "test",
	}

	mux := http.NewServeMux()
	mux.Handle("/api/v1/", srv.Routes())

	mw := &Middleware{
		Key:        AdminKey{TokenID: "dummy", Hash: "dummy"},
		Next:       mux,
		BetterAuth: baVerifier,
	}

	ts := httptest.NewServer(mw)
	defer ts.Close()

	authReq := func(method, path string, body any) *http.Request {
		var buf bytes.Buffer
		if body != nil {
			_ = json.NewEncoder(&buf).Encode(body)
		}
		r, _ := http.NewRequest(method, ts.URL+path, &buf)
		r.Header.Set("Authorization", "Bearer "+sessionToken)
		r.Header.Set("Content-Type", "application/json")
		return r
	}
	client := ts.Client()

	// Test 1: List members (initially only owner)
	t.Run("List initial members", func(t *testing.T) {
		resp, err := client.Do(authReq("GET", "/api/v1/team/members", nil))
		if err != nil {
			t.Fatalf("Do: %v", err)
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("GET /team/members status=%d", resp.StatusCode)
		}
		var res struct {
			Members []store.TeamMember `json:"members"`
			Count   int                `json:"count"`
			Plan    string             `json:"plan"`
		}
		if err := json.NewDecoder(resp.Body).Decode(&res); err != nil {
			t.Fatalf("decode: %v", err)
		}
		if len(res.Members) != 1 || res.Members[0].Email != "owner@team.corp" {
			t.Fatalf("unexpected members: %+v", res.Members)
		}
	})

	var invitedMemberID string

	// Test 2: Invite new member
	t.Run("Invite member", func(t *testing.T) {
		reqBody := map[string]string{
			"email": "developer@team.corp",
			"name":  "Alice Dev",
			"role":  "member",
		}
		resp, err := client.Do(authReq("POST", "/api/v1/team/members/invite", reqBody))
		if err != nil {
			t.Fatalf("Do: %v", err)
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusCreated {
			t.Fatalf("POST /team/members/invite status=%d", resp.StatusCode)
		}
		var created store.TeamMember
		if err := json.NewDecoder(resp.Body).Decode(&created); err != nil {
			t.Fatalf("decode: %v", err)
		}
		if created.Email != "developer@team.corp" || created.Role != "member" {
			t.Fatalf("unexpected created member: %+v", created)
		}
		invitedMemberID = created.ID
	})

	// Test 3: Update role to admin
	t.Run("Update member role", func(t *testing.T) {
		reqBody := map[string]string{"role": "admin"}
		resp, err := client.Do(authReq("PATCH", "/api/v1/team/members/"+invitedMemberID+"/role", reqBody))
		if err != nil {
			t.Fatalf("Do: %v", err)
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("PATCH role status=%d", resp.StatusCode)
		}
	})

	var createdClientID string

	// Test 4: Create member client token
	t.Run("Create member token", func(t *testing.T) {
		reqBody := map[string]string{"name": "Alice MacBook"}
		resp, err := client.Do(authReq("POST", "/api/v1/team/members/"+invitedMemberID+"/tokens", reqBody))
		if err != nil {
			t.Fatalf("Do: %v", err)
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusCreated {
			t.Fatalf("POST tokens status=%d", resp.StatusCode)
		}
		var res struct {
			Client store.Client `json:"client"`
			Token  string       `json:"token"`
		}
		if err := json.NewDecoder(resp.Body).Decode(&res); err != nil {
			t.Fatalf("decode: %v", err)
		}
		if !strings.HasPrefix(res.Token, "zrv_live_") {
			t.Fatalf("token must start with zrv_live_, got %s", res.Token)
		}
		if res.Client.Name != "Alice MacBook" {
			t.Fatalf("unexpected client: %+v", res.Client)
		}
		createdClientID = res.Client.ID
	})

	// Test 5: List member tokens
	t.Run("List member tokens", func(t *testing.T) {
		resp, err := client.Do(authReq("GET", "/api/v1/team/members/"+invitedMemberID+"/tokens", nil))
		if err != nil {
			t.Fatalf("Do: %v", err)
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("GET tokens status=%d", resp.StatusCode)
		}
		var tokens []store.Client
		if err := json.NewDecoder(resp.Body).Decode(&tokens); err != nil {
			t.Fatalf("decode: %v", err)
		}
		if len(tokens) != 1 || tokens[0].ID != createdClientID {
			t.Fatalf("unexpected tokens: %+v", tokens)
		}
	})

	// Test 6: Revoke member token
	t.Run("Revoke member token", func(t *testing.T) {
		resp, err := client.Do(authReq("DELETE", "/api/v1/team/members/"+invitedMemberID+"/tokens/"+createdClientID, nil))
		if err != nil {
			t.Fatalf("Do: %v", err)
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("DELETE token status=%d", resp.StatusCode)
		}

		// List again -> should be 0
		resp2, err := client.Do(authReq("GET", "/api/v1/team/members/"+invitedMemberID+"/tokens", nil))
		if err != nil {
			t.Fatalf("Do: %v", err)
		}
		defer resp2.Body.Close()
		var tokens []store.Client
		_ = json.NewDecoder(resp2.Body).Decode(&tokens)
		if len(tokens) != 0 {
			t.Fatalf("expected 0 tokens after revoke, got %d", len(tokens))
		}
	})

	// Test 7: Remove member
	t.Run("Remove member", func(t *testing.T) {
		resp, err := client.Do(authReq("DELETE", "/api/v1/team/members/"+invitedMemberID, nil))
		if err != nil {
			t.Fatalf("Do: %v", err)
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("DELETE member status=%d", resp.StatusCode)
		}

		// List members -> should be back to 1
		resp2, err := client.Do(authReq("GET", "/api/v1/team/members", nil))
		if err != nil {
			t.Fatalf("Do: %v", err)
		}
		defer resp2.Body.Close()
		var res struct {
			Members []store.TeamMember `json:"members"`
		}
		_ = json.NewDecoder(resp2.Body).Decode(&res)
		if len(res.Members) != 1 {
			t.Fatalf("expected 1 member after delete, got %d", len(res.Members))
		}
	})
}
