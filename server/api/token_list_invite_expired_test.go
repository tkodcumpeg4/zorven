package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"

	"github.com/tkodcumpeg4/zorven/server/store"
)

func (m *orgAuthzStore) ListAPITokens(_ context.Context, tenantID string) ([]store.APIToken, error) {
	out := []store.APIToken{}
	for _, id := range []string{"tok_owner", "tok_mem", "tok_b"} {
		if t := m.tokens[id]; t.TenantID == tenantID {
			out = append(out, t)
		}
	}
	return out, nil
}

func (m *orgAuthzStore) ListAPITokensPage(ctx context.Context, tenantID string, _ store.Page) ([]store.APIToken, int, error) {
	l, _ := m.ListAPITokens(ctx, tenantID)
	return l, len(l), nil
}

func TestListAPITokensOwnerFilter(t *testing.T) {
	s := &Server{Store: newOrgAuthzStore()}
	cases := []struct {
		user string
		want []string
	}{
		{"u_mem", []string{"tok_mem"}}, // yalniz kendi kisisel token'i
		{"u_mem2", []string{}},         // kimsenin token'i yok
		{"u_admin", []string{"tok_owner", "tok_mem"}},
		{"u_owner", []string{"tok_owner", "tok_mem"}},
	}
	for _, c := range cases {
		w := httptest.NewRecorder()
		s.listAPITokens(w, orgReq("GET", "/api/v1/api-tokens", "", c.user))
		if w.Code != http.StatusOK {
			t.Fatalf("%s: kod %d", c.user, w.Code)
		}
		var res struct {
			Tokens []store.APIToken `json:"tokens"`
			Count  int              `json:"count"`
		}
		_ = json.Unmarshal(w.Body.Bytes(), &res)
		if len(res.Tokens) != len(c.want) || res.Count != len(c.want) {
			t.Fatalf("%s: %d token, beklenen %v", c.user, len(res.Tokens), c.want)
		}
		for i, id := range c.want {
			if res.Tokens[i].ID != id {
				t.Errorf("%s: %d. token %s, beklenen %s", c.user, i, res.Tokens[i].ID, id)
			}
		}
		if got := w.Header().Get("X-Total-Count"); got != strconv.Itoa(len(c.want)) {
			t.Errorf("%s: X-Total-Count %s, beklenen %d", c.user, got, len(c.want))
		}
	}
}

func TestGetInvitationExpiredFlag(t *testing.T) {
	st := newOrgAuthzStore()
	st.invites["inv_old"] = store.TeamInvitation{ID: "inv_old", OrganizationName: "Org", Email: "new@x.com", Status: "pending", ExpiresAt: time.Now().Add(-time.Hour)}
	st.invites["inv_ok"] = store.TeamInvitation{ID: "inv_ok", OrganizationName: "Org", Email: "new@x.com", Status: "pending", ExpiresAt: time.Now().Add(time.Hour)}
	s := &Server{Store: st}
	for id, want := range map[string]bool{"inv_old": true, "inv_ok": false} {
		r := orgReq("GET", "/api/v1/invitations/"+id, "", "u_new", "new@x.com")
		r.SetPathValue("id", id)
		w := httptest.NewRecorder()
		s.getInvitation(w, r)
		var res map[string]any
		_ = json.Unmarshal(w.Body.Bytes(), &res)
		if w.Code != http.StatusOK || res["expired"] != want || res["status"] != "pending" || res["organization_name"] != "Org" {
			t.Errorf("%s: kod %d govde %s", id, w.Code, w.Body.String())
		}
	}
}
