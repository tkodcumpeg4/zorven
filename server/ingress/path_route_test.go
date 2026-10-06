package ingress

import (
	"testing"

	"github.com/tkodcumpeg4/zorven/server/store"
)

func TestPathMatches(t *testing.T) {
	cases := []struct {
		path, prefix string
		want         bool
	}{
		{"/api", "/api", true},
		{"/api/", "/api", true},
		{"/api/v1/x", "/api", true},
		{"/apix", "/api", false}, // segment sınırı
		{"/admin", "/api", false},
		{"/anything", "/", true},
		{"/anything", "", true},
		{"/", "/api", false},
	}
	for _, c := range cases {
		if got := pathMatches(c.path, c.prefix); got != c.want {
			t.Errorf("pathMatches(%q,%q)=%v; istenen %v", c.path, c.prefix, got, c.want)
		}
	}
}

func TestLookupPath(t *testing.T) {
	r := &Router{
		byHost: map[string]store.HostRoute{
			"ex.com": {FQDN: "ex.com", TunnelID: "def"},
		},
		wild:    map[string]store.HostRoute{},
		traffic: map[string]*trafficPolicy{},
		paths: map[string][]pathRule{
			"ex.com": {
				// En uzun önce (Reload böyle sıralar).
				{prefix: "/api/admin", route: store.HostRoute{FQDN: "ex.com", TunnelID: "admin", PathPrefix: "/api/admin"}},
				{prefix: "/api", route: store.HostRoute{FQDN: "ex.com", TunnelID: "api", PathPrefix: "/api"}},
			},
		},
	}

	check := func(path, wantTunnel string) {
		t.Helper()
		rt, ok := r.LookupPath("ex.com", path)
		if !ok || rt.TunnelID != wantTunnel {
			t.Errorf("LookupPath(%q)=%q ok=%v; istenen %q", path, rt.TunnelID, ok, wantTunnel)
		}
	}
	check("/api/admin/x", "admin") // en uzun ön-ek
	check("/api/users", "api")
	check("/other", "def") // varsayılana düşer
	check("/", "def")

	// Bilinmeyen host.
	if _, ok := r.LookupPath("nope.com", "/api"); ok {
		t.Error("bilinmeyen host eşleşmemeli")
	}
}
