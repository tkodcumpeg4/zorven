package ingress

import (
	"context"
	"testing"

	"github.com/tkodcumpeg4/zorven/server/store"
)

// fakeStore, yalnizca router'in ihtiyac duydugu yonlendirme listesini saglar.
//
// Neden gercek bir veritabani degil: bu testler SAF YONLENDIRME mantigini
// (hostname eslestirme, wildcard onceligi) olcuyor; depolama katmani konu disi.
// store.Store gomulu ve nil oldugu icin, router beklenmedik bir metot cagirirsa
// test panikler — bu ISTENEN davranistir, sessizce yanlis sonuc uretmez.
type fakeStore struct {
	store.Store
	routes []store.HostRoute
}

func (f *fakeStore) ListHostRoutes(context.Context) ([]store.HostRoute, error) {
	return f.routes, nil
}

func newTestRouter(t *testing.T, hostnames ...string) *Router {
	t.Helper()

	st := &fakeStore{}
	for _, h := range hostnames {
		st.routes = append(st.routes, store.HostRoute{
			FQDN:     h,
			TenantID: store.DefaultTenantID,
			TunnelID: "tun_" + h,
			ClientID: "cli_test",
			Target:   "http://localhost:8000",
			Enabled:  true,
		})
	}

	r := NewRouter(st)
	if err := r.Reload(context.Background()); err != nil {
		t.Fatalf("Reload hata verdi: %v", err)
	}
	return r
}

func TestLookupExactMatch(t *testing.T) {
	r := newTestRouter(t, "api.example.com")

	for _, host := range []string{
		"api.example.com",
		"API.Example.COM",      // DNS buyuk/kucuk harf duyarsiz
		"api.example.com:8443", // port yok sayilmali
		"api.example.com.",     // sondaki kok noktasi
	} {
		if _, ok := r.Lookup(host); !ok {
			t.Errorf("Lookup(%q) eslesmeliydi", host)
		}
	}

	if _, ok := r.Lookup("baska.example.com"); ok {
		t.Error("tanimsiz hostname eslesmemeliydi")
	}
}

// DNS semantigi: "*.example.com" TAM OLARAK BIR etiket esler.
func TestLookupWildcard(t *testing.T) {
	r := newTestRouter(t, "*.example.com")

	eslesmeli := []string{"api.example.com", "app.example.com", "a.example.com"}
	for _, h := range eslesmeli {
		if _, ok := r.Lookup(h); !ok {
			t.Errorf("Lookup(%q) wildcard ile eslesmeliydi", h)
		}
	}

	eslesmemeli := []string{
		"example.com",     // ciplak domain wildcard'a girmez
		"a.b.example.com", // birden fazla etiket
		"api.example.org", // farkli domain
		"exampleXcom",     // benzer ama alakasiz
		"api.notexample.com",
	}
	for _, h := range eslesmemeli {
		if _, ok := r.Lookup(h); ok {
			t.Errorf("Lookup(%q) wildcard ile ESLESMEMELIYDI", h)
		}
	}
}

// Tam eslesme her zaman wildcard'i yenmeli; aksi halde bir subdomain'e
// ozel tunel tanimlamak imkansiz olurdu.
func TestExactMatchBeatsWildcard(t *testing.T) {
	wild := store.HostRoute{
		FQDN: "*.example.com", TenantID: store.DefaultTenantID, TunnelID: "tun_wild",
		ClientID: "cli_test", Target: "http://localhost:1111", Enabled: true,
	}
	exact := store.HostRoute{
		FQDN: "api.example.com", TenantID: store.DefaultTenantID, TunnelID: "tun_exact",
		ClientID: "cli_test", Target: "http://localhost:2222", Enabled: true,
	}
	st := &fakeStore{routes: []store.HostRoute{wild, exact}}

	r := NewRouter(st)
	if err := r.Reload(context.Background()); err != nil {
		t.Fatal(err)
	}

	got, ok := r.Lookup("api.example.com")
	if !ok {
		t.Fatal("api.example.com eslesmeliydi")
	}
	if got.TunnelID != exact.TunnelID {
		t.Errorf("tam eslesme kazanmaliydi: aldim %s (%s), beklenen %s",
			got.TunnelID, got.Target, exact.TunnelID)
	}

	// Baska bir subdomain hala wildcard'a dusmeli.
	other, ok := r.Lookup("app.example.com")
	if !ok || other.TunnelID != wild.TunnelID {
		t.Errorf("app.example.com wildcard'a dusmeliydi, aldim: %+v", other)
	}
}

func TestHostMatchesAny(t *testing.T) {
	hosts := []string{"localhost", "127.0.0.1", "[::1]"}

	for _, h := range []string{"localhost", "localhost:8443", "LOCALHOST", "127.0.0.1:8443", "[::1]:8443"} {
		if !HostMatchesAny(h, hosts) {
			t.Errorf("HostMatchesAny(%q) true olmaliydi", h)
		}
	}
	for _, h := range []string{"api.example.com", "example.com", "127.0.0.2"} {
		if HostMatchesAny(h, hosts) {
			t.Errorf("HostMatchesAny(%q) false olmaliydi", h)
		}
	}
}

func TestIsIPHost(t *testing.T) {
	for _, h := range []string{"127.0.0.1", "127.0.0.1:8443", "192.168.1.21", "192.168.1.21:8443", "[::1]", "[::1]:8443", "10.0.0.1"} {
		if !IsIPHost(h) {
			t.Errorf("IsIPHost(%q) true olmaliydi", h)
		}
	}
	for _, h := range []string{"localhost", "localhost:8443", "example.com", "app.example.com:8443", "not-an-ip"} {
		if IsIPHost(h) {
			t.Errorf("IsIPHost(%q) false olmaliydi", h)
		}
	}
}

func TestNormalizeHost(t *testing.T) {
	cases := map[string]string{
		"API.Example.COM":      "api.example.com",
		"api.example.com:8443": "api.example.com",
		"api.example.com.":     "api.example.com",
		"  api.example.com  ":  "api.example.com",
		"[::1]:8443":           "[::1]",
		"[::1]":                "[::1]",
		"127.0.0.1:80":         "127.0.0.1",
	}
	for in, want := range cases {
		if got := normalizeHost(in); got != want {
			t.Errorf("normalizeHost(%q) = %q, beklenen %q", in, got, want)
		}
	}
}
