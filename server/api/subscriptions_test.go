package api

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/tkodcumpeg4/zorven/server/auth"
	"github.com/tkodcumpeg4/zorven/server/events"
	"github.com/tkodcumpeg4/zorven/server/ratelimit"
	"github.com/tkodcumpeg4/zorven/server/reqlog"
	"github.com/tkodcumpeg4/zorven/server/store"
	"github.com/tkodcumpeg4/zorven/server/store/pgstore"
	"github.com/tkodcumpeg4/zorven/server/tunnel"
)

func TestAPI_Subscription_Lifecycle(t *testing.T) {
	dsn := testE2EDSNDatabase(t)
	ctx := context.Background()

	st, err := pgstore.Open(ctx, dsn)
	if err != nil {
		t.Fatalf("pgstore.Open: %v", err)
	}
	defer st.Close()

	testTen, err := st.CreateTenant(ctx, "sub-api-tenant")
	if err != nil {
		t.Fatalf("CreateTenant: %v", err)
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
	}

	mw := &Middleware{
		Key:         AdminKey{TokenID: tokenID, Hash: hash},
		Limiter:     ratelimit.New(100, 100),
		Next:        apiSrv.Routes(),
		PublicPaths: map[string]bool{},
	}

	doReq := func(method, path string, body any, isAuth bool) *httptest.ResponseRecorder {
		var reqBody *bytes.Reader
		if body != nil {
			b, _ := json.Marshal(body)
			reqBody = bytes.NewReader(b)
		} else {
			reqBody = bytes.NewReader(nil)
		}
		req := httptest.NewRequest(method, path, reqBody)
		if isAuth {
			req.Header.Set("Authorization", "Bearer "+adminKey)
			req.Header.Set("X-Tenant-ID", testTen.ID)
		}
		req.Header.Set("Content-Type", "application/json")
		rec := httptest.NewRecorder()
		mw.ServeHTTP(rec, req)
		return rec
	}

	// 1. Yetkisiz GET /subscription -> 401
	rec := doReq("GET", "/api/v1/subscription", nil, false)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("yetkisiz istek 401 donmeliydi: got %d", rec.Code)
	}

	// 2. Yetkili GET /subscription -> 200 (varsayilan Free plan)
	rec = doReq("GET", "/api/v1/subscription", nil, true)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /subscription status %d, body: %s", rec.Code, rec.Body.String())
	}

	var res struct {
		Subscription store.Subscription `json:"subscription"`
		Usage        store.TenantUsage  `json:"usage"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &res); err != nil {
		t.Fatalf("json parse: %v", err)
	}
	if res.Subscription.Plan != store.PlanFree {
		t.Errorf("varsayilan plan Free olmali: got %s", res.Subscription.Plan)
	}
	if res.Subscription.MaxClients != 2 || res.Subscription.MaxCustomDomains != 0 {
		t.Errorf("Free plan limitleri hatali: %+v", res.Subscription)
	}

	// 3. Admin: Gecersiz plan guncelleme -> 400
	rec = doReq("PUT", "/api/v1/admin/tenants/"+testTen.ID+"/plan", map[string]string{
		"plan": "super_vip",
	}, true)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("gecersiz plan 400 donmeliydi: got %d", rec.Code)
	}

	// 4. Admin: Plani Hobby'ye yukselt -> 200
	rec = doReq("PUT", "/api/v1/admin/tenants/"+testTen.ID+"/plan", map[string]string{
		"plan":   store.PlanHobby,
		"status": store.SubStatusActive,
	}, true)
	if rec.Code != http.StatusOK {
		t.Fatalf("PUT /admin/tenants/.../plan status %d, body: %s", rec.Code, rec.Body.String())
	}
	var hobbySub store.Subscription
	if err := json.Unmarshal(rec.Body.Bytes(), &hobbySub); err != nil {
		t.Fatalf("json parse: %v", err)
	}
	if hobbySub.Plan != store.PlanHobby || hobbySub.MaxClients != 5 || hobbySub.MaxCustomDomains != 1 {
		t.Errorf("guncellenen plan Hobby olmali: %+v", hobbySub)
	}

	// 5. Admin: Plani Pro'ya yukselt -> 200
	rec = doReq("PUT", "/api/v1/admin/tenants/"+testTen.ID+"/plan", map[string]string{
		"plan":   store.PlanPro,
		"status": store.SubStatusActive,
	}, true)
	if rec.Code != http.StatusOK {
		t.Fatalf("PUT /admin/tenants/.../plan status %d, body: %s", rec.Code, rec.Body.String())
	}

	var updatedSub store.Subscription
	if err := json.Unmarshal(rec.Body.Bytes(), &updatedSub); err != nil {
		t.Fatalf("json parse: %v", err)
	}
	if updatedSub.Plan != store.PlanPro || updatedSub.MaxClients != 15 || updatedSub.MaxCustomDomains != 5 {
		t.Errorf("guncellenen plan Pro olmali: %+v", updatedSub)
	}

	// 6. GET /subscription tekrar cagrildiginda Pro gorunmeli
	rec = doReq("GET", "/api/v1/subscription", nil, true)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /subscription status %d", rec.Code)
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &res); err != nil {
		t.Fatalf("json parse: %v", err)
	}
	if res.Subscription.Plan != store.PlanPro || res.Subscription.MaxClients != 15 {
		t.Errorf("guncel abonelik Pro olmaliydi: %+v", res.Subscription)
	}

	// 7. GET /api/v1/plans -> 200 (5 plan listelenmeli)
	rec = doReq("GET", "/api/v1/plans", nil, true)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /api/v1/plans status %d, body: %s", rec.Code, rec.Body.String())
	}
	var plansRes struct {
		Plans []store.Plan `json:"plans"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &plansRes); err != nil {
		t.Fatalf("json parse plans: %v", err)
	}
	if len(plansRes.Plans) < 5 {
		t.Errorf("en az 5 plan donmeliydi: got %d", len(plansRes.Plans))
	}
}
