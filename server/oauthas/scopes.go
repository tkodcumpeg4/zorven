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
	// ScopeTunnelAccess, bir kullanicinin MCP tunel kaynagina erisim kapsami
	// (yalnizca tunel kaynaklarinda verilir; yonetim kaynaginda gecersizdir).
	ScopeTunnelAccess = "mcp"
	// Silme kapsamlari: her biri ayri onay ister, onay ekraninda varsayilan KAPALIDIR
	// ve mcp:manage icinde YER ALMAZ.
	ScopeTunnelsDelete   = "tunnels:delete"
	ScopeHostnamesDelete = "hostnames:delete"
	// ScopeFull, "tam erisim": kullanicinin panel oturumuyla yapabildigi HER SEYI
	// (rol, kaynak politikalari, plan kapilari dahil; kullanicinin yetkisini
	// asmadan) yapabilme kapsami. Yalnizca yonetim kaynaginda (/mcp) verilir,
	// onay ekraninda varsayilan KAPALIDIR, taze yeniden giris (step-up) ister ve
	// kisa omurludur (FullAccessTTL, FullRefreshTTL, StepUpMaxAge).
	ScopeFull = "full"
)

// ScopeInfo, onay ekraninda gosterilen kapsam bilgisi.
type ScopeInfo struct {
	ID    string `json:"id"`
	Write bool   `json:"write"`
	// Destructive, geri donusu olmayan islem kapsami; onay ekraninda varsayilan
	// kapali gelir ve acikca secilmedikce verilmez.
	Destructive bool `json:"destructive,omitempty"`
	// Full, "tam erisim" kapsami: onay ekraninda ayri uyari bolumunde, varsayilan
	// kapali; verilmesi icin taze giris (step-up) gerekir.
	Full bool `json:"full,omitempty"`
}

var scopeCatalog = []ScopeInfo{
	{ScopeMCPRead, false, false, false},
	{ScopeMCPManage, true, false, false},
	{"clients:read", false, false, false}, {"clients:write", true, false, false},
	{"tunnels:read", false, false, false}, {"tunnels:write", true, false, false},
	{ScopeTunnelsDelete, true, true, false},
	{"hostnames:read", false, false, false}, {"hostnames:write", true, false, false},
	{ScopeHostnamesDelete, true, true, false},
	{"analytics:read", false, false, false},
	{ScopeTunnelAccess, false, false, false},
	{ScopeFull, true, false, true},
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
// kapsamlari. Silme kapsamlari (tunnels:delete, hostnames:delete) ayri ve acik onaylidir.
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
		case ScopeFull:
			// "full", REST kapsami listesi DEGILDIR: API katmani onu Principal.Full
			// bayragi olarak isler ve panel oturumuyla ayni yetkiyi uygular
			// (api/oauth_principal.go). Burada yalnizca isaretlenir.
			set[ScopeFull] = true
		case ScopeTunnelAccess:
			// Tunel kaynak kapsami yonetim API'sine HICBIR yetki vermez.
		case ScopeTunnelsDelete:
			set[ScopeTunnelsDelete] = true
			set["tunnels:read"] = true
		case ScopeHostnamesDelete:
			set[ScopeHostnamesDelete] = true
			set["hostnames:read"] = true
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
	if super[ScopeFull] {
		// full, yonetim kaynagindaki her kapsami ima eder ('mcp' tunel kapsami haric).
		return s != ScopeTunnelAccess
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

// HasFull, kapsam listesinde "full" var mi.
func HasFull(scopes []string) bool { return hasScope(scopes, ScopeFull) }
