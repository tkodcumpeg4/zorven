package api

import "testing"

// Genel "read" token'i WireGuard ag oturumu acamaz; acik net kapsamlari,
// "write" ve tam yetkili token'lar acabilir (mevcut zorven access token'lari).
func TestNetConnectScopeExpansion(t *testing.T) {
	cases := map[string]bool{
		"read": false, "mcp:read": false, "tunnels:read": false,
		"net:read": true, "net:write": true, "net:connect": true,
		"write": true, "*": true, "admin": true,
	}
	for g, want := range cases {
		if got := effectiveScopes([]string{g})[ScopeNetConnect]; got != want {
			t.Errorf("%s: net:connect=%v, beklenen %v", g, got, want)
		}
	}
	if !effectiveScopes([]string{"read"})[ScopeNetRead] {
		t.Fatal("read hala net:read (metadata) icermeli")
	}
}
