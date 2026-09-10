package pgstore

import (
	"bytes"
	"context"
	"errors"
	"testing"

	"github.com/tkodcumpeg4/zorven/server/store"
	"golang.org/x/crypto/acme/autocert"
)

func TestCustomDomain_LifecycleAndVerification(t *testing.T) {
	ctx := context.Background()
	s := freshStore(t)

	// 1. Kiraci, istemci ve tunel hazirla
	c, err := s.CreateClient(ctx, store.DefaultTenantID, "laptop", "tok_cd1", "hash_cd1")
	if err != nil {
		t.Fatalf("CreateClient: %v", err)
	}
	tun, err := s.CreateTunnel(ctx, store.DefaultTenantID, c.ID, "http://localhost:8000")
	if err != nil {
		t.Fatalf("CreateTunnel: %v", err)
	}

	// 2. Custom domain ekle: verified false olmali
	h, err := s.AddCustomHostname(ctx, store.DefaultTenantID, tun.ID, "app.example.com", "rpsh-verify-123456")
	if err != nil {
		t.Fatalf("AddCustomHostname: %v", err)
	}
	if h.Verified {
		t.Errorf("yeni eklenen custom domain verified=true olmamali")
	}
	if h.VerifyToken != "rpsh-verify-123456" {
		t.Errorf("verifyToken eslesmedi: got %s, want rpsh-verify-123456", h.VerifyToken)
	}
	if h.Type != store.HostTypeCustom {
		t.Errorf("type custom olmali: got %s", h.Type)
	}

	// 3. Ingress routes kontrolu: dogrulanmamis domain ListHostRoutes'ta YER ALMAMALI
	routes, err := s.ListHostRoutes(ctx)
	if err != nil {
		t.Fatalf("ListHostRoutes: %v", err)
	}
	for _, r := range routes {
		if r.FQDN == "app.example.com" {
			t.Fatalf("dogrulanmamis custom domain ListHostRoutes icinde yer almamali!")
		}
	}

	// 4. GetHostnameByID ve GetHostnameByFQDN
	byID, err := s.GetHostnameByID(ctx, store.DefaultTenantID, h.ID)
	if err != nil {
		t.Fatalf("GetHostnameByID: %v", err)
	}
	if byID.FQDN != "app.example.com" || byID.Verified {
		t.Errorf("GetHostnameByID hatali: %+v", byID)
	}

	byFQDN, err := s.GetHostnameByFQDN(ctx, "APP.EXAMPLE.COM") // buyuk/kucuk harf
	if err != nil {
		t.Fatalf("GetHostnameByFQDN: %v", err)
	}
	if byFQDN.ID != h.ID {
		t.Errorf("GetHostnameByFQDN id eslesmedi: %s != %s", byFQDN.ID, h.ID)
	}

	// 5. Domaini dogrula (VerifyHostname)
	verifiedH, err := s.VerifyHostname(ctx, store.DefaultTenantID, h.ID)
	if err != nil {
		t.Fatalf("VerifyHostname: %v", err)
	}
	if !verifiedH.Verified {
		t.Errorf("VerifyHostname sonrasi verified=false kaldi")
	}

	// 6. Ingress routes kontrolu: artik ListHostRoutes icinde YER ALMALI
	routesAfter, err := s.ListHostRoutes(ctx)
	if err != nil {
		t.Fatalf("ListHostRoutes after: %v", err)
	}
	found := false
	for _, r := range routesAfter {
		if r.FQDN == "app.example.com" {
			found = true
			if r.TunnelID != tun.ID || r.ClientID != c.ID {
				t.Errorf("route bilgisi hatali: %+v", r)
			}
		}
	}
	if !found {
		t.Errorf("dogrulanmis custom domain ListHostRoutes icinde bulunamadi!")
	}
}

func TestACMECache_StoreAndRetrieve(t *testing.T) {
	ctx := context.Background()
	s := freshStore(t)
	cache := s.AutocertCache()

	key := "test.example.com"
	data := []byte("cert-data-bytes-for-acme")

	// 1. Olmayan anahtar -> autocert.ErrCacheMiss
	_, err := cache.Get(ctx, key)
	if !errors.Is(err, autocert.ErrCacheMiss) {
		t.Fatalf("beklenen ErrCacheMiss, gelen: %v", err)
	}

	// 2. Put
	if err := cache.Put(ctx, key, data); err != nil {
		t.Fatalf("cache.Put: %v", err)
	}

	// 3. Get
	got, err := cache.Get(ctx, key)
	if err != nil {
		t.Fatalf("cache.Get: %v", err)
	}
	if !bytes.Equal(got, data) {
		t.Errorf("veriler uyusmuyor: got %s, want %s", string(got), string(data))
	}

	// 4. Delete
	if err := cache.Delete(ctx, key); err != nil {
		t.Fatalf("cache.Delete: %v", err)
	}

	// 5. Silindikten sonra tekrar ErrCacheMiss
	_, err = cache.Get(ctx, key)
	if !errors.Is(err, autocert.ErrCacheMiss) {
		t.Fatalf("silindikten sonra beklenen ErrCacheMiss, gelen: %v", err)
	}
}
