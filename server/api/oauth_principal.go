package api

import (
	"net/http"
	"strconv"

	"github.com/tkodcumpeg4/zorven/server/entitlements"
	"github.com/tkodcumpeg4/zorven/server/oauthas"
)

// serveOAuthPrincipal, /mcp'nin sureç-ici sentetik isteklerinde OAuth ile
// dogrulanmis kimligi (oauthas.Principal) API token yoluyla AYNI kurallarla isler.
//
// Principal yalnizca oauthas.Authenticate sonucu ve yalnizca surec-ici context ile
// gelir; dis bir istek bu context degerini tasiyamaz. Bu yol REST API token'iyla
// birebir ayni denetimleri uygular:
//
//   - uyelik hala geçerli mi (token'in verildigi kullanici o kiracidan cikarildiysa red)
//   - plan ozelligi (FeatureAPIAccess)
//   - yuzey kisiti (tokenPublicRoutes; full kapsamda kara liste: oauth_full.go)
//   - kiraci basina API hiz siniri
//   - kapsamlar (withAPIScopes) ve kullanici kimligi (role="api_token": gercek rol
//     handler'larda GetMemberRole ile cozulur, tipki API token'larda oldugu gibi)
//
// Donus true ise istek bu yolda ele alinmistir.
func (m *Middleware) serveOAuthPrincipal(w http.ResponseWriter, r *http.Request) bool {
	p, ok := oauthas.PrincipalFrom(r.Context())
	if !ok {
		return false
	}
	if m.Store == nil {
		writeJSONError(w, http.StatusUnauthorized, "invalid_token", "kimlik dogrulanamadi")
		return true
	}
	// Kullanici hala bu kiracinin uyesi mi? (API token'larda olmayan ek guvence.)
	role, err := m.Store.GetMemberRole(r.Context(), p.TenantID, p.UserID)
	if err != nil || role == "" {
		writeJSONError(w, http.StatusUnauthorized, "invalid_token", "yetki sahibi artik bu organizasyonun uyesi degil")
		return true
	}
	if m.Entitlements != nil {
		if err := m.Entitlements.CheckFeature(r.Context(), p.TenantID, entitlements.FeatureAPIAccess); err != nil {
			writeJSONError(w, http.StatusForbidden, "feature_not_available",
				"API erişim özelliği mevcut planınızda desteklenmiyor. Lütfen planınızı yükseltin.")
			return true
		}
	}
	// full: allowlist yerine kara liste (fullExcluded) uygulanir; bkz. oauth_full.go.
	if !p.Full && !tokenMayAccess(r.Method, r.URL.Path) {
		writeJSONError(w, http.StatusForbidden, "endpoint_not_available_for_token",
			"bu uc programatik erisim icin kullanilamaz; panelden erisin")
		return true
	}
	if m.APILimiter != nil {
		w.Header().Set("X-RateLimit-Limit", strconv.Itoa(m.APILimiter.Burst()))
		if !m.APILimiter.Allow(p.TenantID) {
			w.Header().Set("X-RateLimit-Remaining", "0")
			w.Header().Set("Retry-After", strconv.Itoa(int(m.APILimiter.RetryAfter().Seconds())))
			writeJSONError(w, http.StatusTooManyRequests, "rate_limited",
				"API hiz sinirina ulasildi, lutfen biraz sonra tekrar deneyin")
			return true
		}
		w.Header().Set("X-RateLimit-Remaining", strconv.Itoa(m.APILimiter.Remaining(p.TenantID)))
	}
	if p.Full {
		m.serveFullPrincipal(w, r, p, role)
		return true
	}
	ctx := withTenant(r.Context(), p.TenantID)
	ctx = m.resolveProject(ctx, r, p.TenantID)
	ctx = withAPIScopes(ctx, p.APIScopes)
	ctx = withUser(ctx, &AuthUser{ID: p.UserID, TenantID: p.TenantID, Role: "api_token"})
	m.next(w, r.WithContext(ctx))
	return true
}
