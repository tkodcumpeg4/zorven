package api

import (
	crand "crypto/rand"
	"encoding/hex"
	"net/http"
	"sort"
	"strconv"
	"strings"
)

// Bu dosya, programatik REST API'nin ortak sozlesmesini uygular:
//   - Kaynak-bazli scope (yetki) modeli ve enforcement (requireScope).
//   - API token'larinin erisebilecegi dar yuzeyin allowlist'i (tokenMayAccess).
//   - Cursor-tabanli, OPT-IN sayfalama yardimcilari (parsePage/paginate).
//   - Istek kimligi uretimi (newRequestID).
//
// Onemli: scope ve yuzey kisiti YALNIZCA API token yolunda uygulanir. Panel
// (Better Auth cerezi) ve admin anahtari kisitsizdir — bkz. requireScope.

// --- Scope modeli -----------------------------------------------------------

// Kaynak-bazli scope sabitleri ("kaynak:aksiyon").
const (
	ScopeClientsRead    = "clients:read"
	ScopeClientsWrite   = "clients:write"
	ScopeTunnelsRead    = "tunnels:read"
	ScopeTunnelsWrite   = "tunnels:write"
	ScopeHostnamesRead  = "hostnames:read"
	ScopeHostnamesWrite = "hostnames:write"
	ScopeIPAllowRead    = "ip_allowlist:read"
	ScopeIPAllowWrite   = "ip_allowlist:write"
	ScopeAnalyticsRead  = "analytics:read"
)

// allReadScopes / allWriteScopes, eski "read"/"write" token'larini yeni modele
// eslemek icin kullanilir.
var (
	allReadScopes = []string{
		ScopeClientsRead, ScopeTunnelsRead, ScopeHostnamesRead,
		ScopeIPAllowRead, ScopeAnalyticsRead,
	}
	allWriteScopes = []string{
		ScopeClientsWrite, ScopeTunnelsWrite, ScopeHostnamesWrite, ScopeIPAllowWrite,
	}
)

func allScopes() []string {
	out := make([]string, 0, len(allReadScopes)+len(allWriteScopes))
	out = append(out, allReadScopes...)
	out = append(out, allWriteScopes...)
	return out
}

// effectiveScopes, token'a yazili ham kapsamlari GERCEK yetki kumesine genisletir:
//   - "*" / "all" / "admin" -> tum public scope'lar
//   - "read"  -> tum *:read + analytics:read (eski token uyumu)
//   - "write" -> tum *:write (+ ilgili :read)
//   - "kaynak:write" -> kendisi + "kaynak:read" (yazan okuyabilir)
func effectiveScopes(granted []string) map[string]bool {
	set := make(map[string]bool)
	for _, g := range granted {
		g = strings.ToLower(strings.TrimSpace(g))
		switch g {
		case "*", "all", "admin":
			for _, s := range allScopes() {
				set[s] = true
			}
		case "read":
			for _, s := range allReadScopes {
				set[s] = true
			}
		case "write":
			for _, s := range allWriteScopes {
				set[s] = true
				set[readOf(s)] = true
			}
		default:
			if g == "" {
				continue
			}
			set[g] = true
			if strings.HasSuffix(g, ":write") {
				set[readOf(g)] = true
			}
		}
	}
	return set
}

// readOf, "kaynak:write" -> "kaynak:read".
func readOf(writeScope string) string {
	return strings.TrimSuffix(writeScope, ":write") + ":read"
}

// requireScope, istegin verilen scope'a sahip olup olmadigini denetler.
//
// Donen deger: true ise cagirici islemine devam eder; false ise yanit
// (403 insufficient_scope) ZATEN yazilmistir, handler hemen return etmelidir.
//
// Cerez/admin auth (context'te API scope yoksa) HER ZAMAN true doner: scope
// kisiti yalnizca programatik token'lara uygulanir.
func requireScope(w http.ResponseWriter, r *http.Request, needed string) bool {
	scopes, ok := apiScopesFromContext(r.Context())
	if !ok {
		return true // cerez/admin: kisit yok
	}
	if effectiveScopes(scopes)[needed] {
		return true
	}
	w.Header().Set("WWW-Authenticate", `Bearer scope="`+needed+`"`)
	writeJSONError(w, http.StatusForbidden, "insufficient_scope",
		"bu islem icin '"+needed+"' kapsami gerekli; API token'inizda bu kapsam yok")
	return false
}

// --- Token yuzeyi (allowlist) ----------------------------------------------

type tokenRoute struct{ method, pattern string }

// tokenPublicRoutes, programatik API token'larinin erisebilecegi TEK uclar.
// Buraya yazili olmayan her /api/v1 ucu token'a KAPALIDIR (default-deny) —
// yeni bir ic uc eklendiginde kazara token'a acilmasin diye bilincli tercih.
var tokenPublicRoutes = []tokenRoute{
	{"GET", "/api/v1/health"},
	{"GET", "/api/v1/me"},
	{"GET", "/api/v1/openapi.yaml"},
	{"GET", "/api/v1/openapi.en.yaml"},
	{"GET", "/api/v1/plans"},

	{"GET", "/api/v1/clients"},
	{"POST", "/api/v1/clients"},
	{"GET", "/api/v1/clients/{id}"},
	{"DELETE", "/api/v1/clients/{id}"},
	{"POST", "/api/v1/clients/{id}/token"},

	{"GET", "/api/v1/tunnels"},
	{"POST", "/api/v1/tunnels"},
	{"GET", "/api/v1/tunnels/{id}"},
	{"PATCH", "/api/v1/tunnels/{id}"},
	{"DELETE", "/api/v1/tunnels/{id}"},

	{"GET", "/api/v1/hostnames"},
	{"POST", "/api/v1/hostnames"},
	{"POST", "/api/v1/hostnames/custom"},
	{"PATCH", "/api/v1/hostnames/{id}"},
	{"POST", "/api/v1/hostnames/{id}/verify"},
	{"DELETE", "/api/v1/hostnames/{id}"},

	{"GET", "/api/v1/ip-allowlist"},
	{"POST", "/api/v1/ip-allowlist"},
	{"PATCH", "/api/v1/ip-allowlist/{id}"},
	{"DELETE", "/api/v1/ip-allowlist/{id}"},

	{"GET", "/api/v1/requests"},
	{"GET", "/api/v1/subscription"},
	{"GET", "/api/v1/events"},
}

// tokenMayAccess, (method, path) bir API token'ina acik mi.
func tokenMayAccess(method, path string) bool {
	for _, rt := range tokenPublicRoutes {
		if rt.method == method && segMatch(rt.pattern, path) {
			return true
		}
	}
	return false
}

// segMatch, "{id}" gibi tek-segment joker iceren bir deseni yola karsi esler.
func segMatch(pattern, path string) bool {
	ps := strings.Split(strings.Trim(pattern, "/"), "/")
	xs := strings.Split(strings.Trim(path, "/"), "/")
	if len(ps) != len(xs) {
		return false
	}
	for i := range ps {
		if strings.HasPrefix(ps[i], "{") && strings.HasSuffix(ps[i], "}") {
			continue
		}
		if ps[i] != xs[i] {
			return false
		}
	}
	return true
}

// --- Sayfalama (cursor-tabanli, OPT-IN) ------------------------------------

const (
	defaultPageLimit = 50
	maxPageLimit     = 200
)

// parsePage, sayfalama parametrelerini cozer.
//
// paginated=false ise istemci NE limit NE cursor gecmemistir -> cagirici TUM
// listeyi dondurmelidir (geriye uyumlu: panel limit gecmez). paginated=true ise
// limit [1..maxPageLimit] araligina kelepcelenmis, cursor (opak) hazirdir.
func parsePage(r *http.Request) (limit int, cursor string, paginated bool) {
	q := r.URL.Query()
	cursor = strings.TrimSpace(q.Get("cursor"))
	lv := strings.TrimSpace(q.Get("limit"))
	if lv == "" && cursor == "" {
		return 0, "", false
	}
	limit = defaultPageLimit
	if lv != "" {
		if n, err := strconv.Atoi(lv); err == nil && n > 0 {
			limit = n
		}
	}
	if limit > maxPageLimit {
		limit = maxPageLimit
	}
	return limit, cursor, true
}

// setPageHeaders, sayfalama sonucunu yanit basliklariyla bildirir (govde cıplak
// dizi kalir): X-Has-More, X-Next-Cursor ve RFC5988 Link rel="next".
func setPageHeaders(w http.ResponseWriter, r *http.Request, nextCursor string, hasMore bool) {
	w.Header().Set("X-Has-More", strconv.FormatBool(hasMore))
	if hasMore && nextCursor != "" {
		w.Header().Set("X-Next-Cursor", nextCursor)
		q := r.URL.Query()
		q.Set("cursor", nextCursor)
		w.Header().Set("Link", "<"+r.URL.Path+"?"+q.Encode()+`>; rel="next"`)
	}
}

// paginate, id'ye gore sirali bir dilime cursor+limit uygular ve (sayfa,
// sonraki-cursor, dahaVar) doner. cursor, onceki sayfanin son id'sidir (opak
// degil — id'nin kendisi; id'ler zaten opak ve tahmin edilemez oldugundan
// ayrica kodlamaya gerek yok). Kararli siralamayi paginate saglar.
func paginate[T any](items []T, id func(T) string, limit int, cursor string) (page []T, next string, hasMore bool) {
	sorted := make([]T, len(items))
	copy(sorted, items)
	sort.Slice(sorted, func(i, j int) bool { return id(sorted[i]) < id(sorted[j]) })

	start := 0
	if cursor != "" {
		for i, it := range sorted {
			if id(it) == cursor {
				start = i + 1
				break
			}
		}
	}
	if start >= len(sorted) {
		return []T{}, "", false
	}
	end := start + limit
	if end >= len(sorted) {
		return sorted[start:], "", false
	}
	page = sorted[start:end]
	return page, id(page[len(page)-1]), true
}

// --- Istek kimligi ----------------------------------------------------------

// newRequestID, gozlemlenebilirlik icin kisa bir istek kimligi uretir.
func newRequestID() string {
	b := make([]byte, 12)
	if _, err := crand.Read(b); err != nil {
		return "req_unknown"
	}
	return "req_" + hex.EncodeToString(b)
}
