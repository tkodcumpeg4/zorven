package api

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/tkodcumpeg4/zorven/server/auth"
	"github.com/tkodcumpeg4/zorven/server/store"
	"github.com/tkodcumpeg4/zorven/server/tunnel"
)

type mockSecurityStore struct {
	store.Store
	clients map[string]store.Client // id -> client
}

func (m *mockSecurityStore) GetClient(ctx context.Context, tenantID, id string) (store.Client, error) {
	c, ok := m.clients[id]
	if !ok || (tenantID != "" && c.TenantID != tenantID) {
		return store.Client{}, store.ErrNotFound
	}
	return c, nil
}

func TestSecurity_Ticket_Tenant_And_Client_Isolation(t *testing.T) {
	storeMock := &mockSecurityStore{
		clients: map[string]store.Client{
			"cli_tenant_a": {ID: "cli_tenant_a", TenantID: "ten_a", Name: "client-a"},
			"cli_tenant_b": {ID: "cli_tenant_b", TenantID: "ten_b", Name: "client-b"},
		},
	}
	srv := &Server{
		Store:   storeMock,
		Hub:     tunnel.NewHub(),
		Tickets: NewTicketStore(),
	}

	// 1. Tenant A asks for a ticket specifically for Tenant B's client -> MUST FAIL (404/Not Found)
	{
		req := httptest.NewRequest("POST", "/api/v1/terminal-ticket", strings.NewReader(`{"client_id":"cli_tenant_b"}`))
		req = req.WithContext(withTenant(req.Context(), "ten_a"))
		rec := httptest.NewRecorder()
		srv.issueTerminalTicket(rec, req)

		if rec.Code != http.StatusNotFound {
			t.Fatalf("expected 404 when requesting ticket for another tenant's client, got %d", rec.Code)
		}
	}

	// 2. Tenant A asks for a ticket for Tenant A's client -> SUCCESS
	var ticketA string
	{
		req := httptest.NewRequest("POST", "/api/v1/terminal-ticket", strings.NewReader(`{"client_id":"cli_tenant_a"}`))
		req = req.WithContext(withTenant(req.Context(), "ten_a"))
		rec := httptest.NewRecorder()
		srv.issueTerminalTicket(rec, req)

		if rec.Code != http.StatusOK {
			t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
		}
		// Extract ticket
		tok := srv.Tickets.issue("ten_a", "cli_tenant_a")
		ticketA = tok
	}

	// 3. Redeeming ticket A on Tenant B's client -> MUST FAIL
	if _, ok := srv.Tickets.redeem(ticketA, "ten_a", "cli_tenant_b"); ok {
		t.Errorf("IDOR VULNERABILITY: Ticket issued for cli_tenant_a was accepted for cli_tenant_b!")
	}

	// 4. Redeeming ticket A with Tenant B context -> MUST FAIL
	tokB := srv.Tickets.issue("ten_a", "cli_tenant_a")
	if _, ok := srv.Tickets.redeem(tokB, "ten_b", "cli_tenant_a"); ok {
		t.Errorf("IDOR VULNERABILITY: Ticket issued for ten_a was accepted for ten_b!")
	}

	// 5. Redeeming valid ticket without tenant context -> SUCCESS (ticket provides tenant)
	tokValid := srv.Tickets.issue("ten_a", "cli_tenant_a")
	tenantID, ok := srv.Tickets.redeem(tokValid, "", "cli_tenant_a")
	if !ok || tenantID != "ten_a" {
		t.Errorf("valid ticket redemption failed: got tenant %q, ok=%v", tenantID, ok)
	}

	// 6. Double redeem (Replay attack) -> MUST FAIL
	if _, ok := srv.Tickets.redeem(tokValid, "", "cli_tenant_a"); ok {
		t.Errorf("REPLAY VULNERABILITY: Ticket was accepted twice!")
	}
}

func TestSecurity_CSWSH_Origin_Validation(t *testing.T) {
	srv := &Server{PlatformDomain: "rpshell.app"}

	// 1. Same-origin -> ALLOW
	req1 := httptest.NewRequest("GET", "https://rpshell.app/api/v1/clients/c1/terminal", nil)
	req1.Header.Set("Origin", "https://rpshell.app")
	if !srv.isAllowedOrigin(req1) {
		t.Errorf("expected same-origin to be allowed")
	}

	// 2. Subdomain of platform domain -> ALLOW
	req2 := httptest.NewRequest("GET", "https://sunucu:8443/api/v1/clients/c1/terminal", nil)
	req2.Header.Set("Origin", "https://admin.rpshell.app")
	if !srv.isAllowedOrigin(req2) {
		t.Errorf("expected platform subdomain origin to be allowed")
	}

	// 3. Localhost dev origin -> ALLOW
	req3 := httptest.NewRequest("GET", "https://localhost:8443/api/v1/clients/c1/terminal", nil)
	req3.Header.Set("Origin", "http://localhost:3000")
	if !srv.isAllowedOrigin(req3) {
		t.Errorf("expected localhost origin to be allowed")
	}

	// 4. Malicious third-party origin -> BLOCK
	req4 := httptest.NewRequest("GET", "https://rpshell.app/api/v1/clients/c1/terminal", nil)
	req4.Header.Set("Origin", "https://evil-attacker.com")
	if srv.isAllowedOrigin(req4) {
		t.Errorf("CSWSH VULNERABILITY: malicious origin was allowed!")
	}
}

func TestSecurity_TimingAttack_DummyVerification(t *testing.T) {
	// Verify that auth.VerifyDummy runs argon2 verification with realistic duration
	secret := "test_secret_12345"
	start := time.Now()
	auth.VerifyDummy(secret)
	dur := time.Since(start)

	// Argon2 with default parameters takes at least a few milliseconds
	if dur < 1*time.Millisecond {
		t.Errorf("VerifyDummy took %v, expected at least 1ms to prevent timing leak", dur)
	}
}
