package main

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/tkodcumpeg4/zorven/server/entitlements"
)

type fakeQuota struct {
	deny   bool
	called string
}

func (f *fakeQuota) CanAddMember(_ context.Context, tenantID string) error {
	f.called = tenantID
	if f.deny {
		return &entitlements.EntitlementError{Code: "member_limit_reached", Message: "uye limiti", Resource: "members", Limit: 1, Current: 1}
	}
	return nil
}

func quotaProxy(t *testing.T, q *fakeQuota, hits *int) *httptest.Server {
	t.Helper()
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		*hits++
		b, _ := io.ReadAll(r.Body)
		_, _ = w.Write(b)
	}))
	t.Cleanup(up.Close)
	u, _ := url.Parse(up.URL)
	gate := &memberQuotaGate{Quota: q, ActiveOrg: func(context.Context, string) string { return "ten_active" }}
	front := httptest.NewServer(newAuthProxy(u, nil, gate))
	t.Cleanup(front.Close)
	return front
}

func TestAuthProxyInviteMemberQuota(t *testing.T) {
	hits := 0
	q := &fakeQuota{deny: true}
	front := quotaProxy(t, q, &hits)

	resp, err := http.Post(front.URL+"/api/auth/organization/invite-member", "application/json",
		strings.NewReader(`{"email":"a@b.c","organizationId":"ten_x"}`))
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden || hits != 0 || q.called != "ten_x" {
		t.Fatalf("kota asiminda 403 ve proxy'siz olmali: %d hits=%d org=%q", resp.StatusCode, hits, q.called)
	}
	if !strings.Contains(string(body), "member_limit_reached") {
		t.Fatalf("entitlement hata bicimi bekleniyordu: %s", body)
	}

	// organizationId yoksa aktif organizasyon kullanilir.
	resp, _ = http.Post(front.URL+"/api/auth/organization/invite-member", "application/json", strings.NewReader(`{"email":"a@b.c"}`))
	resp.Body.Close()
	if q.called != "ten_active" || resp.StatusCode != http.StatusForbidden {
		t.Fatalf("aktif org beklenirdi: %q %d", q.called, resp.StatusCode)
	}

	// Kota uygunsa istek govdesi bozulmadan Better Auth'a gider; diger uclar etkilenmez.
	q.deny = false
	resp, _ = http.Post(front.URL+"/api/auth/organization/invite-member", "application/json", strings.NewReader(`{"email":"a@b.c"}`))
	got, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if hits != 1 || string(got) != `{"email":"a@b.c"}` {
		t.Fatalf("govde korunmali: hits=%d %q", hits, got)
	}
	q.deny, q.called = true, ""
	resp, _ = http.Post(front.URL+"/api/auth/sign-in/email", "application/json", strings.NewReader(`{}`))
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK || q.called != "" {
		t.Fatalf("baska uc kota kontrolune girmemeli")
	}
}
