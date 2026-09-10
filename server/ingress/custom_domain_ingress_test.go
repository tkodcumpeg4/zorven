package ingress_test

import (
	"context"
	"crypto/tls"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"github.com/tkodcumpeg4/zorven/server/ingress"
	"github.com/tkodcumpeg4/zorven/server/store"
	"github.com/tkodcumpeg4/zorven/server/store/pgstore"
	"github.com/tkodcumpeg4/zorven/server/tlscert"
)

func TestCustomDomain_IngressRoutingAndTLS(t *testing.T) {
	dsn := os.Getenv("ZORVEN_TEST_PG_DSN")
	if dsn == "" {
		t.Skip("ZORVEN_TEST_PG_DSN ayarli degil; Postgres testleri atlaniyor")
	}

	ctx := context.Background()
	st, err := pgstore.Open(ctx, dsn)
	if err != nil {
		t.Fatalf("pgstore.Open: %v", err)
	}
	defer st.Close()

	suffix := fmt.Sprintf("%d", time.Now().UnixNano())

	// 1. Client ve Tunnel olustur
	cli, err := st.CreateClient(ctx, store.DefaultTenantID, "test-pc-"+suffix, "tok_"+suffix, "hash_"+suffix)
	if err != nil {
		t.Fatalf("CreateClient: %v", err)
	}
	tun, err := st.CreateTunnel(ctx, store.DefaultTenantID, cli.ID, "http://localhost:8080")
	if err != nil {
		t.Fatalf("CreateTunnel: %v", err)
	}

	// 2. Custom Domain ekle (Dogrulanmamis olarak baslar)
	customFQDN := fmt.Sprintf("api-%s.custom-customer.com", suffix)
	host, err := st.AddCustomHostname(ctx, store.DefaultTenantID, tun.ID, customFQDN, "rpsh-verify-token-xyz")
	if err != nil {
		t.Fatalf("AddCustomHostname: %v", err)
	}
	if host.Verified {
		t.Fatalf("Yeni eklenen custom domain dogrulanmis olmamali")
	}

	// 3. Ingress Router ilklendir ve yukle
	r := ingress.NewRouter(st)
	if err := r.Reload(ctx); err != nil {
		t.Fatalf("router.Reload: %v", err)
	}

	// 4. Dogrulanmamis ozel domain router'da BULUNAMAMALI
	if _, ok := r.Lookup(customFQDN); ok {
		t.Fatalf("Dogrulanmamis ozel domain Lookup tarafindan bulunmamaliydi!")
	}

	// Ingress HTTP Handler cagrisi 404 donmeli
	h := &ingress.Handler{
		Router: r,
	}
	req := httptest.NewRequest(http.MethodGet, "http://"+customFQDN+"/test", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Errorf("Dogrulanmamis domain icin 404 bekleniyordu, alinan: %d", rec.Code)
	}

	// 5. Hostname'i dogrula (DNS dogrulamasi tamamlandi)
	if _, err := st.VerifyHostname(ctx, store.DefaultTenantID, host.ID); err != nil {
		t.Fatalf("VerifyHostname: %v", err)
	}

	// 6. Router'i tekrar yukle
	if err := r.Reload(ctx); err != nil {
		t.Fatalf("router.Reload 2: %v", err)
	}

	// 7. Artik router'da BULUNMALI ve dogru tunele eslesmeli
	matchedTun, ok := r.Lookup(customFQDN)
	if !ok {
		t.Fatalf("Dogrulanmis ozel domain Lookup tarafindan bulunmaliydi!")
	}
	if matchedTun.TunnelID != tun.ID {
		t.Errorf("Eslenen tunel hatali: got %s, want %s", matchedTun.TunnelID, tun.ID)
	}

	// 8. DualCertManager TLS dogrulamasi
	dualMgr := tlscert.NewDualCertManager(nil, "rpshell.app", []string{"rpshell.app"}, st, "", nil)

	// HostPolicy kontrolu
	if err := dualMgr.HostPolicy(ctx, customFQDN); err != nil {
		t.Fatalf("HostPolicy dogrulanmis ozel domaini kabul etmeliydi: %v", err)
	}
	if err := dualMgr.HostPolicy(ctx, "unregistered-domain.com"); err == nil {
		t.Fatalf("HostPolicy kayitli olmayan domaini reddetmeliydi")
	}

	// GetCertificate cagrilarak dinamik sertifika donusu test edilir
	cert, err := dualMgr.GetCertificate(&tls.ClientHelloInfo{ServerName: customFQDN})
	if err != nil {
		t.Fatalf("GetCertificate ozel domain icin sertifika donmeliydi: %v", err)
	}
	if cert == nil {
		t.Fatalf("Sertifika nil dondu")
	}
}
