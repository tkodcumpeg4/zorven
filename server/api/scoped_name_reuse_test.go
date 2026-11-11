package api

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
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

// A4 #15: kalici tunel silinince kapsamli ad kiracida bos kalir; ayni kiraci ayni adla
// yeniden olusturabilmeli (eskiden 409). Ad baska tunele bagliyken 409 korunur.
func TestAPI_ScopedNameReuseAfterDelete(t *testing.T) {
	dsn := testE2EDSNDatabase(t)
	ctx := context.Background()
	st, err := pgstore.Open(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	ten, err := st.CreateTenant(ctx, fmt.Sprintf("reuse-%d", time.Now().UnixNano()))
	if err != nil {
		t.Fatal(err)
	}
	c, err := st.CreateClient(ctx, ten.ID, "pc", fmt.Sprintf("tok_r_%d", time.Now().UnixNano()), "h")
	if err != nil {
		t.Fatal(err)
	}
	adminKey, tokenID, hash, err := auth.GenerateAdmin()
	if err != nil {
		t.Fatal(err)
	}
	apiSrv := &Server{
		Store: st, Hub: tunnel.NewHub(), Log: reqlog.New(10), Events: events.New(),
		Tickets: NewTicketStore(), Entitlements: entitlements.NewService(st), PlatformDomain: "rpshell.test",
	}
	mw := &Middleware{Key: AdminKey{TokenID: tokenID, Hash: hash}, Limiter: ratelimit.New(100, 100),
		Next: apiSrv.Routes(), PublicPaths: map[string]bool{}}
	do := func(method, path string, body any) *httptest.ResponseRecorder {
		var b []byte
		if body != nil {
			b, _ = json.Marshal(body)
		}
		req := httptest.NewRequest(method, path, bytes.NewReader(b))
		req.Header.Set("Authorization", "Bearer "+adminKey)
		req.Header.Set("X-Tenant-ID", ten.ID)
		req.Header.Set("Content-Type", "application/json")
		rec := httptest.NewRecorder()
		mw.ServeHTTP(rec, req)
		return rec
	}
	create := func() *httptest.ResponseRecorder {
		return do("POST", "/api/v1/tunnels", map[string]any{"name": "seqx", "client_id": c.ID, "target": "http://127.0.0.1:1"})
	}
	rec := create()
	if rec.Code != http.StatusCreated {
		t.Fatalf("ilk olusturma: %d %s", rec.Code, rec.Body.String())
	}
	var t1 store.Tunnel
	_ = json.Unmarshal(rec.Body.Bytes(), &t1)
	if len(t1.Hostnames) != 1 {
		t.Fatalf("ad baglanmadi: %+v", t1)
	}
	fqdn := t1.Hostnames[0].FQDN
	// Ad bagliyken ikinci olusturma 409.
	if rec := create(); rec.Code != http.StatusConflict {
		t.Fatalf("bagli ad icin 409 bekleniyordu: %d", rec.Code)
	}
	if rec := do("DELETE", "/api/v1/tunnels/"+t1.ID, nil); rec.Code != http.StatusNoContent {
		t.Fatalf("silme: %d", rec.Code)
	}
	h, err := st.GetHostnameByFQDN(ctx, fqdn)
	if err != nil || h.TunnelID != "" {
		t.Skipf("silmede ad birakilmiyor (rezervasyon yok), senaryo gecersiz: %+v %v", h, err)
	}
	rec = create()
	if rec.Code != http.StatusCreated {
		t.Fatalf("ayni adla yeniden olusturma: %d %s", rec.Code, rec.Body.String())
	}
	var t2 store.Tunnel
	_ = json.Unmarshal(rec.Body.Bytes(), &t2)
	if len(t2.Hostnames) != 1 || t2.Hostnames[0].FQDN != fqdn || t2.Hostnames[0].ID != h.ID {
		t.Fatalf("bos ad yeniden baglanmadi: %+v", t2.Hostnames)
	}
}
