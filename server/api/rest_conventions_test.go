package api

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestOpenAPISpecServed(t *testing.T) {
	s := &Server{}
	w := httptest.NewRecorder()
	s.openapiSpec(w, httptest.NewRequest("GET", "/api/v1/openapi.yaml", nil))
	if w.Code != http.StatusOK {
		t.Fatalf("beklenen 200, alinan %d", w.Code)
	}
	if ct := w.Header().Get("Content-Type"); !strings.Contains(ct, "yaml") {
		t.Errorf("Content-Type yaml olmali, alinan %q", ct)
	}
	body := w.Body.String()
	if !strings.Contains(body, "openapi: 3.1") {
		t.Error("gomulu spec OpenAPI 3.1 basligini icermeli")
	}
	if !strings.Contains(body, "Zorven REST API") {
		t.Error("spec basligi eksik")
	}
}

func TestEffectiveScopes(t *testing.T) {
	cases := []struct {
		name    string
		granted []string
		want    map[string]bool // scope -> beklenen uyeklik
	}{
		{
			name:    "eski read+write tam public erisim",
			granted: []string{"read", "write"},
			want: map[string]bool{
				ScopeClientsRead: true, ScopeClientsWrite: true,
				ScopeTunnelsRead: true, ScopeTunnelsWrite: true,
				ScopeAnalyticsRead: true,
			},
		},
		{
			name:    "kaynak read yazamaz",
			granted: []string{ScopeTunnelsRead},
			want: map[string]bool{
				ScopeTunnelsRead:  true,
				ScopeTunnelsWrite: false,
				ScopeClientsRead:  false,
			},
		},
		{
			name:    "kaynak write okumayi da kapsar",
			granted: []string{ScopeTunnelsWrite},
			want: map[string]bool{
				ScopeTunnelsWrite: true,
				ScopeTunnelsRead:  true,
				ScopeClientsWrite: false,
			},
		},
		{
			name:    "wildcard her seyi verir",
			granted: []string{"*"},
			want: map[string]bool{
				ScopeClientsWrite: true, ScopeIPAllowWrite: true, ScopeAnalyticsRead: true,
			},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			eff := effectiveScopes(c.granted)
			for scope, exp := range c.want {
				if eff[scope] != exp {
					t.Errorf("scope %q: beklenen %v, alinan %v", scope, exp, eff[scope])
				}
			}
		})
	}
}

func TestRequireScope(t *testing.T) {
	// 1. Cerez/admin auth (context'te apiScopes yok) -> kisit yok, izin verilir.
	t.Run("cerez auth kisitsiz", func(t *testing.T) {
		w := httptest.NewRecorder()
		r := httptest.NewRequest("DELETE", "/api/v1/tunnels/tun_1", nil)
		if !requireScope(w, r, ScopeTunnelsWrite) {
			t.Fatal("cerez auth'ta requireScope true donmeli")
		}
	})

	// 2. read token'i write ucta reddedilir (403 insufficient_scope).
	t.Run("read token write reddedilir", func(t *testing.T) {
		w := httptest.NewRecorder()
		r := httptest.NewRequest("DELETE", "/api/v1/tunnels/tun_1", nil)
		r = r.WithContext(withAPIScopes(r.Context(), []string{ScopeTunnelsRead}))
		if requireScope(w, r, ScopeTunnelsWrite) {
			t.Fatal("read token write ucta false donmeliydi")
		}
		if w.Code != http.StatusForbidden {
			t.Errorf("beklenen 403, alinan %d", w.Code)
		}
		if got := w.Header().Get("WWW-Authenticate"); got == "" {
			t.Error("WWW-Authenticate basligi set edilmeliydi")
		}
	})

	// 3. Yeterli scope'la izin verilir.
	t.Run("yeterli scope izinli", func(t *testing.T) {
		w := httptest.NewRecorder()
		r := httptest.NewRequest("DELETE", "/api/v1/tunnels/tun_1", nil)
		r = r.WithContext(withAPIScopes(r.Context(), []string{ScopeTunnelsWrite}))
		if !requireScope(w, r, ScopeTunnelsWrite) {
			t.Fatal("write token write ucta true donmeliydi")
		}
	})
}

func TestTokenMayAccess(t *testing.T) {
	allow := []struct{ m, p string }{
		{"GET", "/api/v1/tunnels"},
		{"POST", "/api/v1/tunnels"},
		{"DELETE", "/api/v1/tunnels/tun_abc"},
		{"POST", "/api/v1/clients/cli_1/token"},
		{"GET", "/api/v1/requests"},
		{"POST", "/api/v1/hostnames/custom"},
		{"POST", "/api/v1/hostnames/hn_1/verify"},
	}
	for _, c := range allow {
		if !tokenMayAccess(c.m, c.p) {
			t.Errorf("acik olmali: %s %s", c.m, c.p)
		}
	}

	deny := []struct{ m, p string }{
		{"GET", "/api/v1/mail/messages"},
		{"POST", "/api/v1/team/members/invite"},
		{"GET", "/api/v1/admin/stats"},
		{"POST", "/api/v1/billing/start-trial"},
		{"GET", "/api/v1/api-tokens"},
		{"POST", "/api/v1/tunnels/tun_1/extra"}, // desende yok
		{"PUT", "/api/v1/tunnels/tun_1"},        // metot uyusmuyor (PATCH acik)
	}
	for _, c := range deny {
		if tokenMayAccess(c.m, c.p) {
			t.Errorf("KAPALI olmali ama acik: %s %s", c.m, c.p)
		}
	}
}

func TestParsePage(t *testing.T) {
	t.Run("parametresiz -> sayfalama yok", func(t *testing.T) {
		r := httptest.NewRequest("GET", "/api/v1/clients", nil)
		if _, _, ok := parsePage(r); ok {
			t.Error("parametresiz istekte paginated=false olmali")
		}
	})
	t.Run("limit verilince sayfalanir", func(t *testing.T) {
		r := httptest.NewRequest("GET", "/api/v1/clients?limit=10", nil)
		limit, _, ok := parsePage(r)
		if !ok || limit != 10 {
			t.Errorf("beklenen (10,true), alinan (%d,%v)", limit, ok)
		}
	})
	t.Run("limit maks'a kelepcelenir", func(t *testing.T) {
		r := httptest.NewRequest("GET", "/api/v1/clients?limit=99999", nil)
		limit, _, _ := parsePage(r)
		if limit != maxPageLimit {
			t.Errorf("beklenen %d, alinan %d", maxPageLimit, limit)
		}
	})
}

func TestPaginate(t *testing.T) {
	type item struct{ id string }
	getID := func(x item) string { return x.id }
	items := []item{{"c"}, {"a"}, {"e"}, {"b"}, {"d"}} // sirasiz

	// Ilk sayfa: limit 2 -> a,b; next=b; more=true
	page, next, more := paginate(items, getID, 2, "")
	if len(page) != 2 || page[0].id != "a" || page[1].id != "b" {
		t.Fatalf("ilk sayfa beklenmedik: %+v", page)
	}
	if next != "b" || !more {
		t.Fatalf("next/more beklenmedik: next=%q more=%v", next, more)
	}

	// Sonraki sayfa (cursor=b): c,d; next=d; more=true
	page, next, more = paginate(items, getID, 2, "b")
	if len(page) != 2 || page[0].id != "c" || page[1].id != "d" || next != "d" || !more {
		t.Fatalf("ikinci sayfa beklenmedik: %+v next=%q more=%v", page, next, more)
	}

	// Son sayfa (cursor=d): e; more=false
	page, next, more = paginate(items, getID, 2, "d")
	if len(page) != 1 || page[0].id != "e" || more {
		t.Fatalf("son sayfa beklenmedik: %+v next=%q more=%v", page, next, more)
	}
}
