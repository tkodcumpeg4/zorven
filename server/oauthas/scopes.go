package oauthas

import (
	"sort"
	"strings"
)

// Kapsam adlari. mcp:* kisayollari REST API kapsamlarina genisler; digerleri
// API token kapsamlariyla ayni dizgelerdir.
const (
	ScopeMCPRead   = "mcp:read"
	ScopeMCPManage = "mcp:manage"
)

// ScopeInfo, onay ekraninda gosterilen kapsam bilgisi.
type ScopeInfo struct {
	ID    string `json:"id"`
	Write bool   `json:"write"`
}

var scopeCatalog = []ScopeInfo{
	{ScopeMCPRead, false},
	{ScopeMCPManage, true},
	{"clients:read", false}, {"clients:write", true},
	{"tunnels:read", false}, {"tunnels:write", true},
	{"hostnames:read", false}, {"hostnames:write", true},
	{"analytics:read", false},
}

// SupportedScopes, metadata'da duyurulan kapsamlar.
func SupportedScopes() []string {
	out := make([]string, len(scopeCatalog))
	for i, s := range scopeCatalog {
		out[i] = s.ID
	}
	return out
}

func scopeInfo(id string) (ScopeInfo, bool) {
	for _, s := range scopeCatalog {
		if s.ID == id {
			return s, true
		}
	}
	return ScopeInfo{}, false
}

// ParseScopes bosluk ayrimli kapsam dizgesini cozer; tekrarlari atar. Bilinmeyen
// bir kapsam varsa onu da doner (invalid_scope).
func ParseScopes(s string) (scopes []string, unknown string) {
	seen := map[string]bool{}
	for _, f := range strings.Fields(s) {
		if seen[f] {
			continue
		}
		if _, ok := scopeInfo(f); !ok {
			return nil, f
		}
		seen[f] = true
		scopes = append(scopes, f)
	}
	return scopes, ""
}

// APIScopes, OAuth kapsamlarini REST API token kapsamlarina genisletir.
// mcp:read -> "read" (tum *:read + analytics:read); mcp:manage -> read + yazma
// kapsamlari. Silme araci yoktur; yazma kapsamlari yalniz olusturma/guncelleme.
func APIScopes(oauthScopes []string) []string {
	set := map[string]bool{}
	for _, s := range oauthScopes {
		switch s {
		case ScopeMCPRead:
			set["read"] = true
		case ScopeMCPManage:
			set["read"] = true
			set["tunnels:write"] = true
			set["hostnames:write"] = true
			set["clients:write"] = true
		default:
			if _, ok := scopeInfo(s); ok {
				set[s] = true
			}
		}
	}
	out := make([]string, 0, len(set))
	for s := range set {
		out = append(out, s)
	}
	sort.Strings(out)
	return out
}

// covers, super kapsam kumesinin s'yi (mcp:* kisayollarinin ima ettikleri dahil) kapsayip kapsamadigi.
func covers(super map[string]bool, s string) bool {
	if super[s] {
		return true
	}
	if super[ScopeMCPManage] {
		switch s {
		case ScopeMCPRead, "tunnels:write", "hostnames:write", "clients:write":
			return true
		}
	}
	if super[ScopeMCPRead] || super[ScopeMCPManage] {
		return strings.HasSuffix(s, ":read") && s != ScopeMCPRead
	}
	return false
}

// subsetOf, sub'in her ogesi super tarafindan kapsaniyor mu.
func subsetOf(sub, super []string) bool {
	m := map[string]bool{}
	for _, s := range super {
		m[s] = true
	}
	for _, s := range sub {
		if !covers(m, s) {
			return false
		}
	}
	return true
}
