package ingress_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/tkodcumpeg4/zorven/server/bandwidth"
	"github.com/tkodcumpeg4/zorven/server/ingress"
	"github.com/tkodcumpeg4/zorven/server/store"
	"github.com/tkodcumpeg4/zorven/server/store/pgstore"
	"github.com/tkodcumpeg4/zorven/server/tunnel"
)

func TestIngress_ThrottlingAndUsageRecording(t *testing.T) {
	ctx := context.Background()
	st, err := pgstore.Open(ctx, "postgres://rpshell:rpshell@localhost:5432/rpshell_test")
	if err != nil {
		t.Skip("Postgres test DB baglanamadi, atlaniyor")
	}
	defer st.Close()

	ten, err := st.CreateTenant(ctx, "throttle-ten-"+time.Now().Format("150405"))
	if err != nil {
		t.Fatalf("CreateTenant: %v", err)
	}

	tokenID := "tok_" + time.Now().Format("150405.000000")
	cli, err := st.CreateClient(ctx, ten.ID, "mac", tokenID, "hash_"+tokenID)
	if err != nil {
		t.Fatalf("CreateClient: %v", err)
	}

	tun, err := st.CreateTunnel(ctx, ten.ID, cli.ID, "http://localhost:8080")
	if err != nil {
		t.Fatalf("CreateTunnel: %v", err)
	}

	fqdn := "app-" + ten.Slug + ".rpshell.app"
	_, err = st.AddHostname(ctx, ten.ID, tun.ID, fqdn, store.HostTypeScoped)
	if err != nil {
		t.Fatalf("AddHostname: %v", err)
	}

	router := ingress.NewRouter(st)
	if err := router.Reload(ctx); err != nil {
		t.Fatalf("router.Reload: %v", err)
	}

	hub := tunnel.NewHub()
	tracker := bandwidth.NewLocalRecorder(st)
	defer tracker.Close()

	handler := &ingress.Handler{
		Router:            router,
		Hub:               hub,
		BandwidthRecorder: tracker,
		BandwidthTracker:  tracker,
	}

	// 1. Istemci bagli degilken 502 Bad Gateway
	req := httptest.NewRequest("GET", "http://"+fqdn+"/test", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadGateway {
		t.Fatalf("Beklenen 502, alinan: %d", rec.Code)
	}

	// 2. Kiraciyi bilerek kota asimina sokalim
	state, err := tracker.GetOrCreateState(ctx, ten.ID)
	if err != nil {
		t.Fatalf("GetOrCreateState: %v", err)
	}
	// Free planda kota 5 GB; 6 GB bayt kaydedelim
	state.Record(3*1024*1024*1024, 3*1024*1024*1024)

	if !state.IsThrottled() {
		t.Fatalf("Kiraci kota asimi sonrasi throttled olmaliydi")
	}

	// 3. Tekrar istek gonderelim; bu sefer X-RPShell-Throttled header'i basilmali
	req = httptest.NewRequest("GET", "http://"+fqdn+"/test", nil)
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Header().Get("X-RPShell-Throttled") != "true" {
		t.Errorf("Kota asildiginda X-RPShell-Throttled header'i true olmali, got: %s", rec.Header().Get("X-RPShell-Throttled"))
	}
	if rec.Header().Get("X-RPShell-Plan-Status") != "bandwidth_exceeded" {
		t.Errorf("X-RPShell-Plan-Status bandwidth_exceeded olmali, got: %s", rec.Header().Get("X-RPShell-Plan-Status"))
	}
}
