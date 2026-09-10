package api

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/tkodcumpeg4/zorven/server/auth"
	"github.com/tkodcumpeg4/zorven/server/domain"
	"github.com/tkodcumpeg4/zorven/server/events"
	"github.com/tkodcumpeg4/zorven/server/ratelimit"
	"github.com/tkodcumpeg4/zorven/server/reqlog"
	"github.com/tkodcumpeg4/zorven/server/store"
	"github.com/tkodcumpeg4/zorven/server/store/pgstore"
	"github.com/tkodcumpeg4/zorven/server/tunnel"
)

type mockAPIDNSVerifier struct {
	cnames map[string]string
	txts   map[string][]string
}

func (m *mockAPIDNSVerifier) LookupCNAME(ctx context.Context, host string) (string, error) {
	if val, ok := m.cnames[host]; ok {
		return val, nil
	}
	return "", domain.ErrVerificationFailed
}

func (m *mockAPIDNSVerifier) LookupTXT(ctx context.Context, name string) ([]string, error) {
	if val, ok := m.txts[name]; ok {
		return val, nil
	}
	return nil, domain.ErrVerificationFailed
}

func TestAPI_CustomDomain_Lifecycle(t *testing.T) {
	dsn := testE2EDSNDatabase(t)
	ctx := context.Background()

	st, err := pgstore.Open(ctx, dsn)
	if err != nil {
		t.Fatalf("pgstore.Open: %v", err)
	}
	defer st.Close()

	// 1. Temizlik ve hazirlik
	c, err := st.CreateClient(ctx, store.DefaultTenantID, "test-client", "tok_cust_api", "hash_cust_api")
	if err != nil {
		t.Fatalf("CreateClient: %v", err)
	}
	defer st.DeleteClient(ctx, store.DefaultTenantID, c.ID)

	tun, err := st.CreateTunnel(ctx, store.DefaultTenantID, c.ID, "http://localhost:3000")
	if err != nil {
		t.Fatalf("CreateTunnel: %v", err)
	}
	defer st.DeleteTunnel(ctx, store.DefaultTenantID, tun.ID)

	mockDNS := &mockAPIDNSVerifier{
		cnames: make(map[string]string),
		txts:   make(map[string][]string),
	}

	adminKey, tokenID, hash, err := auth.GenerateAdmin()
	if err != nil {
		t.Fatalf("GenerateAdmin: %v", err)
	}

	apiSrv := &Server{
		Store:          st,
		Hub:            tunnel.NewHub(),
		Log:            reqlog.New(100),
		Events:         events.New(),
		PlatformDomain: "rpshell.app",
		DNSVerifier:    mockDNS,
	}

	mw := &Middleware{
		Key:         AdminKey{TokenID: tokenID, Hash: hash},
		Limiter:     ratelimit.New(100, 100),
		Next:        apiSrv.Routes(),
		PublicPaths: map[string]bool{},
	}

	doReq := func(method, path string, body any) *httptest.ResponseRecorder {
		var reqBody *bytes.Reader
		if body != nil {
			b, _ := json.Marshal(body)
			reqBody = bytes.NewReader(b)
		} else {
			reqBody = bytes.NewReader(nil)
		}
		req := httptest.NewRequest(method, path, reqBody)
		req.Header.Set("Authorization", "Bearer "+adminKey)
		req.Header.Set("Content-Type", "application/json")
		rec := httptest.NewRecorder()
		mw.ServeHTTP(rec, req)
		return rec
	}

	// 2. Custom Domain Ekle (POST /api/v1/hostnames/custom)
	rec := doReq("POST", "/api/v1/hostnames/custom", map[string]string{
		"tunnel_id": tun.ID,
		"fqdn":      "sub.mycustomapp.com",
	})
	if rec.Code != http.StatusCreated {
		t.Fatalf("POST /hostnames/custom status %d, body: %s", rec.Code, rec.Body.String())
	}

	var createdResp customHostnameResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &createdResp); err != nil {
		t.Fatalf("json parse: %v", err)
	}
	if createdResp.Verified {
		t.Errorf("yeni custom domain verified=false olmali")
	}
	if createdResp.Type != store.HostTypeCustom {
		t.Errorf("type=custom olmali, got %s", createdResp.Type)
	}
	if createdResp.Instructions.CNAMETarget != "cname.rpshell.app" {
		t.Errorf("CNAMETarget hatali: %s", createdResp.Instructions.CNAMETarget)
	}
	hostID := createdResp.ID
	defer st.DeleteHostname(ctx, store.DefaultTenantID, hostID)

	// 3. DNS kaydi henuz yokken dogrula (POST /api/v1/hostnames/{id}/verify) -> 422
	rec = doReq("POST", "/api/v1/hostnames/"+hostID+"/verify", nil)
	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("DNS yokken 422 bekleniyordu, got %d: %s", rec.Code, rec.Body.String())
	}

	// 4. Mock DNS'e CNAME kaydi ekle ve tekrar dogrula -> 200 OK
	mockDNS.cnames["sub.mycustomapp.com"] = "cname.rpshell.app."
	rec = doReq("POST", "/api/v1/hostnames/"+hostID+"/verify", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("DNS varken 200 bekleniyordu, got %d: %s", rec.Code, rec.Body.String())
	}

	var verifiedResp store.Hostname
	if err := json.Unmarshal(rec.Body.Bytes(), &verifiedResp); err != nil {
		t.Fatalf("json parse: %v", err)
	}
	if !verifiedResp.Verified {
		t.Errorf("dogrulama sonrasi verified true olmaliydi")
	}

	// 5. Listele (GET /api/v1/hostnames)
	rec = doReq("GET", "/api/v1/hostnames", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /hostnames status %d", rec.Code)
	}
	var list []store.Hostname
	json.Unmarshal(rec.Body.Bytes(), &list)
	found := false
	for _, item := range list {
		if item.ID == hostID && item.Verified {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("dogrulanmis custom domain listede bulunamadi")
	}
}
