package api

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/tkodcumpeg4/zorven/server/auth"
	"github.com/tkodcumpeg4/zorven/server/entitlements"
	"github.com/tkodcumpeg4/zorven/server/events"
	"github.com/tkodcumpeg4/zorven/server/ratelimit"
	"github.com/tkodcumpeg4/zorven/server/reqlog"
	"github.com/tkodcumpeg4/zorven/server/store"
	"github.com/tkodcumpeg4/zorven/server/store/pgstore"
	"github.com/tkodcumpeg4/zorven/server/tunnel"
)

type dummyDNSVerifier struct{}

func (d *dummyDNSVerifier) LookupCNAME(ctx context.Context, host string) (string, error) {
	return "cname.rpshell.app.", nil
}
func (d *dummyDNSVerifier) LookupTXT(ctx context.Context, name string) ([]string, error) {
	return []string{"rpsh-verify-123"}, nil
}

func TestAPI_LimitEnforcement_ClientsDomainsTunnelsScreen(t *testing.T) {
	dsn := testE2EDSNDatabase(t)
	ctx := context.Background()

	st, err := pgstore.Open(ctx, dsn)
	if err != nil {
		t.Fatalf("pgstore.Open: %v", err)
	}
	defer st.Close()

	ten, err := st.CreateTenant(ctx, "limit-test-ten-"+time.Now().Format("150405"))
	if err != nil {
		t.Fatalf("CreateTenant: %v", err)
	}

	entSvc := entitlements.NewService(st)

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
		DNSVerifier:    &dummyDNSVerifier{},
		Tickets:        NewTicketStore(),
		Entitlements:   entSvc,
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
		req.Header.Set("X-Tenant-ID", ten.ID)
		req.Header.Set("Content-Type", "application/json")
		rec := httptest.NewRecorder()
		mw.ServeHTTP(rec, req)
		return rec
	}

	// 1. Client Limiti Testi (Free: 2 client)
	rec := doReq("POST", "/api/v1/clients", map[string]string{"name": "pc1"})
	if rec.Code != http.StatusCreated {
		t.Fatalf("1. client olusturulamadi: %d, %s", rec.Code, rec.Body.String())
	}
	var c1Resp struct {
		Client store.Client `json:"client"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &c1Resp)

	rec = doReq("POST", "/api/v1/clients", map[string]string{"name": "pc2"})
	if rec.Code != http.StatusCreated {
		t.Fatalf("2. client olusturulamadi: %d", rec.Code)
	}

	// 3. client denemesi -> 403 plan_limit_reached
	rec = doReq("POST", "/api/v1/clients", map[string]string{"name": "pc3"})
	if rec.Code != http.StatusForbidden {
		t.Fatalf("3. client 403 Forbidden olmaliydi: got %d, body: %s", rec.Code, rec.Body.String())
	}

	// 2. Custom Domain Testi (Free: 0 domain)
	rec = doReq("POST", "/api/v1/hostnames/custom", map[string]string{"fqdn": "app.acme.com"})
	if rec.Code != http.StatusForbidden {
		t.Fatalf("Free planda custom domain 403 olmaliydi: got %d, body: %s", rec.Code, rec.Body.String())
	}

	// 3. Tunnel Limiti Testi (Free: 2 tunnel)
	rec = doReq("POST", "/api/v1/tunnels", map[string]string{
		"name":      "tun1",
		"client_id": c1Resp.Client.ID,
		"target":    "http://localhost:3000",
	})
	if rec.Code != http.StatusCreated {
		t.Fatalf("1. tunel olusturulamadi: %d, body: %s", rec.Code, rec.Body.String())
	}

	rec = doReq("POST", "/api/v1/tunnels", map[string]string{
		"name":      "tun2",
		"client_id": c1Resp.Client.ID,
		"target":    "http://localhost:3001",
	})
	if rec.Code != http.StatusCreated {
		t.Fatalf("2. tunel olusturulamadi: %d, body: %s", rec.Code, rec.Body.String())
	}

	// 3. tunel denemesi -> 403 plan_limit_reached
	rec = doReq("POST", "/api/v1/tunnels", map[string]string{
		"name":      "tun3",
		"client_id": c1Resp.Client.ID,
		"target":    "http://localhost:3002",
	})
	if rec.Code != http.StatusForbidden {
		t.Fatalf("3. tunel 403 Forbidden olmaliydi: got %d, body: %s", rec.Code, rec.Body.String())
	}

	// 4. Screen Stream Ticket Limiti (Free: 1 stream)
	rec = doReq("POST", "/api/v1/screen-ticket", map[string]string{"client_id": c1Resp.Client.ID})
	if rec.Code != http.StatusOK {
		t.Fatalf("Ilk ekran bileti alinamadi: %d, body: %s", rec.Code, rec.Body.String())
	}
	var ticketResp struct {
		Ticket string `json:"ticket"`
		MaxFPS int    `json:"max_fps"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &ticketResp)
	if ticketResp.MaxFPS != 30 {
		t.Errorf("Free planda MaxFPS 30 olmali: got %d", ticketResp.MaxFPS)
	}

	// 2. ekran bileti denemesi -> 403 Forbidden (screen_stream_limit_reached)
	rec = doReq("POST", "/api/v1/screen-ticket", map[string]string{"client_id": c1Resp.Client.ID})
	if rec.Code != http.StatusForbidden {
		t.Fatalf("2. ekran bileti 403 olmaliydi: got %d, body: %s", rec.Code, rec.Body.String())
	}
}
