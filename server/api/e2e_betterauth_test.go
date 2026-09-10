package api

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/tkodcumpeg4/zorven/server/auth"
	"github.com/tkodcumpeg4/zorven/server/events"
	"github.com/tkodcumpeg4/zorven/server/reqlog"
	"github.com/tkodcumpeg4/zorven/server/store/pgstore"
	"github.com/tkodcumpeg4/zorven/server/tunnel"
)

func testE2EDSNDatabase(t *testing.T) string {
	t.Helper()
	dsn := os.Getenv("ZORVEN_TEST_PG_DSN")
	if dsn == "" {
		t.Skip("ZORVEN_TEST_PG_DSN ayarli degil; E2E Better Auth testleri atlaniyor")
	}
	u, err := url.Parse(dsn)
	if err != nil || !strings.Contains(strings.TrimPrefix(u.Path, "/"), "test") {
		t.Fatalf("ZORVEN_TEST_PG_DSN test veritabani olmali: %s", dsn)
	}
	return dsn
}

func TestBetterAuth_E2E_Flow(t *testing.T) {
	dsn := testE2EDSNDatabase(t)
	ctx := context.Background()

	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatalf("pgxpool.New: %v", err)
	}
	defer pool.Close()

	// Temizlik
	_, _ = pool.Exec(ctx, `DELETE FROM "session" WHERE "token" IN ('tok_e2e_user1', 'tok_e2e_user2')`)
	_, _ = pool.Exec(ctx, `DELETE FROM "member" WHERE "id" IN ('mem_e2e_1', 'mem_e2e_2')`)
	_, _ = pool.Exec(ctx, `DELETE FROM "organization" WHERE "id" IN ('org_e2e_team_a', 'org_e2e_team_b')`)
	_, _ = pool.Exec(ctx, `DELETE FROM "user" WHERE "id" IN ('usr_e2e_1', 'usr_e2e_2')`)
	_, _ = pool.Exec(ctx, `DELETE FROM "tenants" WHERE "id" IN ('org_e2e_team_a', 'org_e2e_team_b')`)

	// 1. Better Auth tablolarina kullanici ve organizasyon yerlestir
	// (Trigger automatically syncs organization -> tenants!)
	_, err = pool.Exec(ctx, `
		INSERT INTO "user" ("id", "name", "email", "createdAt", "updatedAt")
		VALUES ('usr_e2e_1', 'E2E Developer', 'e2e@dev.corp', now(), now())
	`)
	if err != nil {
		t.Fatalf("user insert: %v", err)
	}

	_, err = pool.Exec(ctx, `
		INSERT INTO "organization" ("id", "name", "slug", "createdAt")
		VALUES ('org_e2e_team_a', 'E2E Team A', 'e2e-team-a', now())
	`)
	if err != nil {
		t.Fatalf("org insert: %v", err)
	}

	_, err = pool.Exec(ctx, `
		INSERT INTO "member" ("id", "organizationId", "userId", "role", "createdAt")
		VALUES ('mem_e2e_1', 'org_e2e_team_a', 'usr_e2e_1', 'owner', now())
	`)
	if err != nil {
		t.Fatalf("member insert: %v", err)
	}

	_, err = pool.Exec(ctx, `
		INSERT INTO "session" ("id", "userId", "token", "expiresAt", "activeOrganizationId", "createdAt", "updatedAt")
		VALUES ('sess_e2e_1', 'usr_e2e_1', 'tok_e2e_user1', now() + interval '2 hours', 'org_e2e_team_a', now(), now())
	`)
	if err != nil {
		t.Fatalf("session insert: %v", err)
	}

	// 2. Go Store ve Server ayarla
	st, err := pgstore.Open(ctx, dsn)
	if err != nil {
		t.Fatalf("pgstore.Open: %v", err)
	}
	defer st.Close()

	verifier := auth.NewBetterAuthVerifier(st.Pool(), 1*time.Minute)
	hub := tunnel.NewHub()
	requests := reqlog.New(100)
	broker := events.New()

	apiSrv := &Server{
		Store:   st,
		Hub:     hub,
		Log:     requests,
		Events:  broker,
		Version: "test",
	}

	mux := http.NewServeMux()
	mux.Handle("/api/v1/", apiSrv.Routes())

	mw := &Middleware{
		Key:        AdminKey{TokenID: "dummy", Hash: "dummy"},
		Next:       mux,
		BetterAuth: verifier,
		PublicPaths: map[string]bool{
			"/api/v1/health": true,
		},
	}

	ts := httptest.NewServer(mw)
	defer ts.Close()

	client := ts.Client()

	t.Run("Unauthorized request without token or cookie", func(t *testing.T) {
		req, err := http.NewRequestWithContext(ctx, "GET", ts.URL+"/api/v1/me", nil)
		if err != nil {
			t.Fatalf("NewRequest: %v", err)
		}
		resp, err := client.Do(req)
		if err != nil {
			t.Fatalf("Do: %v", err)
		}
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusUnauthorized {
			t.Errorf("beklenen 401 Unauthorized, alinan: %d", resp.StatusCode)
		}
	})

	t.Run("Authorized via Better Auth session cookie", func(t *testing.T) {
		req, err := http.NewRequestWithContext(ctx, "GET", ts.URL+"/api/v1/me", nil)
		if err != nil {
			t.Fatalf("NewRequest: %v", err)
		}
		req.AddCookie(&http.Cookie{
			Name:  "better-auth.session_token",
			Value: "tok_e2e_user1",
		})

		resp, err := client.Do(req)
		if err != nil {
			t.Fatalf("Do: %v", err)
		}
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusOK {
			t.Fatalf("beklenen 200 OK, alinan: %d", resp.StatusCode)
		}

		var body struct {
			Authenticated bool   `json:"authenticated"`
			Method        string `json:"method"`
			User          struct {
				ID    string `json:"id"`
				Email string `json:"email"`
				Name  string `json:"name"`
				Role  string `json:"role"`
			} `json:"user"`
			Tenant struct {
				ID   string `json:"id"`
				Slug string `json:"slug"`
			} `json:"tenant"`
		}
		if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
			t.Fatalf("Decode: %v", err)
		}

		if !body.Authenticated {
			t.Error("authenticated true olmaliydi")
		}
		if body.Method != "better-auth" {
			t.Errorf("beklenen method better-auth, alinan: %s", body.Method)
		}
		if body.User.Email != "e2e@dev.corp" {
			t.Errorf("beklenen email e2e@dev.corp, alinan: %s", body.User.Email)
		}
		if body.User.Role != "owner" {
			t.Errorf("beklenen role owner, alinan: %s", body.User.Role)
		}
		if body.Tenant.ID != "org_e2e_team_a" {
			t.Errorf("beklenen tenant ID org_e2e_team_a, alinan: %s", body.Tenant.ID)
		}
		if body.Tenant.Slug != "e2e-team-a" {
			t.Errorf("beklenen tenant slug e2e-team-a, alinan: %s", body.Tenant.Slug)
		}
	})

	t.Run("Authorized via Better Auth Bearer token header", func(t *testing.T) {
		req, err := http.NewRequestWithContext(ctx, "GET", ts.URL+"/api/v1/me", nil)
		if err != nil {
			t.Fatalf("NewRequest: %v", err)
		}
		req.Header.Set("Authorization", "Bearer tok_e2e_user1")

		resp, err := client.Do(req)
		if err != nil {
			t.Fatalf("Do: %v", err)
		}
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusOK {
			t.Fatalf("beklenen 200 OK, alinan: %d", resp.StatusCode)
		}
	})

	t.Run("Create client within authenticated tenant scope", func(t *testing.T) {
		payload := `{"name":"agent-e2e-laptop"}`
		req, err := http.NewRequestWithContext(ctx, "POST", ts.URL+"/api/v1/clients", bytes.NewBufferString(payload))
		if err != nil {
			t.Fatalf("NewRequest: %v", err)
		}
		req.Header.Set("Content-Type", "application/json")
		req.AddCookie(&http.Cookie{
			Name:  "better-auth.session_token",
			Value: "tok_e2e_user1",
		})

		resp, err := client.Do(req)
		if err != nil {
			t.Fatalf("Do: %v", err)
		}
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusCreated {
			t.Fatalf("beklenen 201 Created, alinan: %d", resp.StatusCode)
		}

		var created struct {
			Client struct {
				ID       string `json:"id"`
				Name     string `json:"name"`
				TenantID string `json:"tenant_id"`
			} `json:"client"`
			Token string `json:"token"`
		}
		if err := json.NewDecoder(resp.Body).Decode(&created); err != nil {
			t.Fatalf("Decode: %v", err)
		}

		if created.Client.Name != "agent-e2e-laptop" {
			t.Errorf("beklenen isim agent-e2e-laptop, alinan: %s", created.Client.Name)
		}

		// Dogrudan store'dan kiraci izolasyonunu dogrula
		dbClient, err := st.GetClient(ctx, "org_e2e_team_a", created.Client.ID)
		if err != nil {
			t.Fatalf("st.GetClient kendi kiracisinda bulamadi: %v", err)
		}
		if dbClient.TenantID != "org_e2e_team_a" {
			t.Errorf("beklenen TenantID org_e2e_team_a, alinan: %s", dbClient.TenantID)
		}

		// Baska kiraci bu istemciyi ASLA gorememeli (izolasyon)
		_, err = st.GetClient(ctx, "ten_default", created.Client.ID)
		if err == nil {
			t.Errorf("farkli bir kiraci bu istemciyi gormemeliydi (izolasyon ihlali)")
		}

		// List clients for this tenant
		listReq, _ := http.NewRequestWithContext(ctx, "GET", ts.URL+"/api/v1/clients", nil)
		listReq.AddCookie(&http.Cookie{Name: "better-auth.session_token", Value: "tok_e2e_user1"})
		listResp, err := client.Do(listReq)
		if err != nil {
			t.Fatalf("list Do: %v", err)
		}
		defer listResp.Body.Close()

		var clients []struct {
			ID   string `json:"id"`
			Name string `json:"name"`
		}
		if err := json.NewDecoder(listResp.Body).Decode(&clients); err != nil {
			t.Fatalf("Decode list: %v", err)
		}
		found := false
		for _, c := range clients {
			if c.ID == created.Client.ID {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("olusturulan istemci listede bulunamadi")
		}
	})

	t.Run("Switch organization and verify strict tenant isolation", func(t *testing.T) {
		// Ikinci organizasyon ve uyelik olustur
		_, err := pool.Exec(ctx, `
			INSERT INTO "organization" ("id", "name", "slug", "createdAt")
			VALUES ('org_e2e_team_b', 'E2E Team B', 'e2e-team-b', now())
		`)
		if err != nil {
			t.Fatalf("org_b insert: %v", err)
		}

		_, err = pool.Exec(ctx, `
			INSERT INTO "member" ("id", "organizationId", "userId", "role", "createdAt")
			VALUES ('mem_e2e_2', 'org_e2e_team_b', 'usr_e2e_1', 'member', now())
		`)
		if err != nil {
			t.Fatalf("member_b insert: %v", err)
		}

		// Kullanici ikinci bir oturum acti veya aktif organizasyonu degistirdi
		_, err = pool.Exec(ctx, `
			INSERT INTO "session" ("id", "userId", "token", "expiresAt", "activeOrganizationId", "createdAt", "updatedAt")
			VALUES ('sess_e2e_2', 'usr_e2e_1', 'tok_e2e_user1_org_b', now() + interval '2 hours', 'org_e2e_team_b', now(), now())
		`)
		if err != nil {
			t.Fatalf("session_b insert: %v", err)
		}

		// /api/v1/me ile yeni organizasyonun dogrulandigini kontrol et
		req, _ := http.NewRequestWithContext(ctx, "GET", ts.URL+"/api/v1/me", nil)
		req.AddCookie(&http.Cookie{Name: "better-auth.session_token", Value: "tok_e2e_user1_org_b"})
		resp, err := client.Do(req)
		if err != nil {
			t.Fatalf("me Do: %v", err)
		}
		defer resp.Body.Close()

		var me struct {
			User struct {
				Role string `json:"role"`
			} `json:"user"`
			Tenant struct {
				ID   string `json:"id"`
				Slug string `json:"slug"`
			} `json:"tenant"`
		}
		if err := json.NewDecoder(resp.Body).Decode(&me); err != nil {
			t.Fatalf("me decode: %v", err)
		}

		if me.Tenant.ID != "org_e2e_team_b" || me.Tenant.Slug != "e2e-team-b" {
			t.Errorf("organizasyon degisimi basarisiz: got %+v", me.Tenant)
		}
		if me.User.Role != "member" {
			t.Errorf("beklenen rol 'member', alinan: %s", me.User.Role)
		}

		// Team B'de istemcileri listele -> Team A'nin istemcileri ASLA gorunmemeli (0 olmali)
		listReq, _ := http.NewRequestWithContext(ctx, "GET", ts.URL+"/api/v1/clients", nil)
		listReq.AddCookie(&http.Cookie{Name: "better-auth.session_token", Value: "tok_e2e_user1_org_b"})
		listResp, err := client.Do(listReq)
		if err != nil {
			t.Fatalf("list Do: %v", err)
		}
		defer listResp.Body.Close()

		var clientsInB []struct {
			ID string `json:"id"`
		}
		if err := json.NewDecoder(listResp.Body).Decode(&clientsInB); err != nil {
			t.Fatalf("decode: %v", err)
		}
		if len(clientsInB) != 0 {
			t.Errorf("Team B'de 0 istemci olmaliydi (izolasyon saglanamadi), alinan: %d", len(clientsInB))
		}
	})
}
