package api

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

// Origin semasi/portu ve uretim-gelistirme ayrimi (F-27/F-48).
func TestOriginTrustedSchemePortAndLoopback(t *testing.T) {
	check := func(platform, host, origin string) bool {
		m := &Middleware{
			PlatformDomain: platform,
			TrustedHosts:   TrustedOriginHosts(platform, []string{"panel." + platform}),
		}
		r := httptest.NewRequest(http.MethodPost, "https://"+host+"/api/v1/x", nil)
		r.Host = host
		r.Header.Set("Origin", origin)
		return m.originTrusted(r)
	}
	cases := []struct {
		name                 string
		platform, host, orig string
		want                 bool
	}{
		{"prod-https-ok", "zorven.app", "panel.zorven.app", "https://panel.zorven.app", true},
		{"prod-explicit-443-ok", "zorven.app", "panel.zorven.app", "https://panel.zorven.app:443", true},
		{"prod-http-rejected", "zorven.app", "panel.zorven.app", "http://panel.zorven.app", false},
		{"prod-other-port-rejected", "zorven.app", "panel.zorven.app", "https://panel.zorven.app:9999", false},
		{"prod-loopback-rejected", "zorven.app", "panel.zorven.app", "http://localhost:3000", false},
		{"prod-loopback-https-rejected", "zorven.app", "panel.zorven.app", "https://127.0.0.1", false},
		{"prod-non-default-port-same", "zorven.app", "panel.zorven.app:8443", "https://panel.zorven.app:8443", true},
		{"prod-non-default-port-missing", "zorven.app", "panel.zorven.app:8443", "https://panel.zorven.app", false},
		{"dev-staging-ok", "e2e.test", "panel.e2e.test:18443", "https://panel.e2e.test:18443", true},
		{"dev-staging-http-over-tls", "e2e.test", "panel.e2e.test:18443", "http://panel.e2e.test:18443", false}, // TLS istegine http Origin: sema dusurme
		{"dev-staging-wrong-port", "e2e.test", "panel.e2e.test:18443", "https://panel.e2e.test:9999", false},
		{"dev-loopback-ok", "e2e.test", "panel.e2e.test:18443", "http://localhost:3000", true},
		{"dev-localhost-domain-loopback", "localhost", "localhost:8443", "http://localhost:3000", true},
	}
	for _, c := range cases {
		if got := check(c.platform, c.host, c.orig); got != c.want {
			t.Errorf("%s: %v, beklenen %v", c.name, got, c.want)
		}
	}
}

func TestIsProductionDomain(t *testing.T) {
	for pd, want := range map[string]bool{
		"zorven.app": true, "": false, "localhost": false, "x.localhost": false,
		"e2e.test": false, "127.0.0.1": false, " Zorven.App ": true,
	} {
		if got := IsProductionDomain(pd); got != want {
			t.Errorf("%q: %v, beklenen %v", pd, got, want)
		}
	}
}
