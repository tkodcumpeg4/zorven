package api

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/tkodcumpeg4/zorven/server/store"
	"github.com/tkodcumpeg4/zorven/server/tunnel"
)

type remoteTicketStore struct {
	store.Store
}

func (m *remoteTicketStore) GetClient(_ context.Context, tenantID, id string) (store.Client, error) {
	if id != "cli_a" || tenantID != "ten_a" {
		return store.Client{}, store.ErrNotFound
	}
	return store.Client{ID: id, TenantID: tenantID}, nil
}

// F-06: terminal ve ekran biletleri birbirinin yerine kullanilamaz.
func TestTicketKindsAreNotInterchangeable(t *testing.T) {
	ts := NewTicketStore()
	tt := ts.issue(ticketKindTerminal, "ten_a", "cli_a")
	if _, ok := ts.redeem(ticketKindScreen, tt, "ten_a", "cli_a"); ok {
		t.Fatal("terminal bileti ekran ucunda kabul edildi")
	}
	st := ts.issueWithRelease(ticketKindScreen, "ten_a", "cli_a", nil)
	if _, ok := ts.redeem(ticketKindTerminal, st, "ten_a", "cli_a"); ok {
		t.Fatal("ekran bileti terminal ucunda kabul edildi")
	}
	same := ts.issue(ticketKindTerminal, "ten_a", "cli_a")
	if _, ok := ts.redeem(ticketKindTerminal, same, "ten_a", "cli_a"); !ok {
		t.Fatal("ayni tur bilet reddedildi")
	}
}

// F-07: muafiyet yalniz gercek WS desenine uyar; digerleri kimlik ister.
func TestMiddlewareTicketExemptionNarrow(t *testing.T) {
	m := &Middleware{Next: http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	})}
	do := func(method, path string) int {
		r := httptest.NewRequest(method, path, nil)
		w := httptest.NewRecorder()
		m.ServeHTTP(w, r)
		return w.Code
	}
	if c := do(http.MethodDelete, "/api/v1/clients/terminal"); c != http.StatusUnauthorized {
		t.Fatalf("DELETE /api/v1/clients/terminal: %d, 401 bekleniyordu", c)
	}
	if c := do(http.MethodPost, "/api/v1/clients/c1/terminal"); c != http.StatusUnauthorized {
		t.Fatalf("POST .../terminal: %d, 401 bekleniyordu", c)
	}
	if c := do(http.MethodGet, "/api/v1/foo/screen"); c != http.StatusUnauthorized {
		t.Fatalf("GET /api/v1/foo/screen: %d, 401 bekleniyordu", c)
	}
	if c := do(http.MethodGet, "/api/v1/clients/c1/terminal"); c != http.StatusNoContent {
		t.Fatalf("gercek WS ucu muaf olmali: %d", c)
	}
	if c := do(http.MethodGet, "/api/v1/clients/c1/screen"); c != http.StatusNoContent {
		t.Fatalf("gercek WS ucu muaf olmali: %d", c)
	}
}

// F-05: uzak terminal/ekran bileti yalniz owner/admin; member client_id ile 403.
func TestRemoteTicketRoleGate(t *testing.T) {
	srv := &Server{Store: &remoteTicketStore{}, Hub: tunnel.NewHub(), Tickets: NewTicketStore()}
	for name, h := range map[string]http.HandlerFunc{
		"terminal": srv.issueTerminalTicket,
		"screen":   srv.issueScreenTicket,
	} {
		call := func(role string) int {
			r := httptest.NewRequest("POST", "/x", strings.NewReader(`{"client_id":"cli_a"}`))
			ctx := withTenant(r.Context(), "ten_a")
			ctx = withUser(ctx, &AuthUser{ID: "u1", Role: role})
			w := httptest.NewRecorder()
			h(w, r.WithContext(ctx))
			return w.Code
		}
		if c := call("member"); c != http.StatusForbidden {
			t.Errorf("%s: member client_id ile %d, 403 bekleniyordu", name, c)
		}
		if c := call("owner"); c != http.StatusOK {
			t.Errorf("%s: owner %d, 200 bekleniyordu", name, c)
		}
		if c := call("admin"); c != http.StatusOK {
			t.Errorf("%s: admin %d, 200 bekleniyordu", name, c)
		}
	}
}
