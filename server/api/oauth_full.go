package api

import (
	"context"
	"net/http"
	"strings"

	"github.com/tkodcumpeg4/zorven/server/oauthas"
	"github.com/tkodcumpeg4/zorven/server/store"
	"github.com/tkodcumpeg4/zorven/server/store/pgstore"
)

// Bu dosya OAuth "full" (tam erisim) kapsaminin API tarafini uygular.
//
// Model: full OAuth kimligi, kullanicinin PANEL OTURUMUYLA AYNI yetkiyle calisir.
//
//   - Kiraci rolu (owner/admin/member) GERCEK rol olarak cozulur ("api_token" degil).
//   - API kapsam listesi (withAPIScopes) KOYULMAZ: requireScope panel oturumundaki gibi
//     gecer; ama rol, plan kapilari ve proje secimi
//     (X-Zorven-Project) handler'larda AYNEN uygulanir.
//   - tokenPublicRoutes allowlist'i asilir; yerine fullExcluded kara listesi gelir.
//   - Platform admin YALNIZCA kullanici gercekten platform adminse (panel oturumuyla
//     ayni kural); X-Tenant-ID ile kiraci degistirme full'da YOKTUR (kiraci grant'tir).
//   - Kullanicinin yetkisini ASLA asmaz: oturumla erisilemeyen hicbir uca erisilemez.

const ctxKeyOAuthFull ctxKey = 100

// withOAuthFull, istegin full-kapsamli bir OAuth kimligiyle geldigini isaretler.
func withOAuthFull(ctx context.Context) context.Context {
	return context.WithValue(ctx, ctxKeyOAuthFull, true)
}

// isOAuthFull, istek full-kapsamli OAuth kimligiyle mi geldi.
func isOAuthFull(ctx context.Context) bool {
	v, _ := ctx.Value(ctxKeyOAuthFull).(bool)
	return v
}

// fullExcludedRoutes, full ile BILE erisilemeyen (403 full_scope_excluded) uclar.
// Gerekce: kendini kalicilastirma / yetki zinciri (kalici kimlik bilgisi uretimi),
// hesabin kendisini ele gecirme ve geri alinamaz kiraci silme. Panelden yapilir.
// "{id}" tek-segment jokeri; yolun sonundaki "/*" alt yollari da kapsar.
var fullExcludedRoutes = []tokenRoute{
	// API token olusturma/yonetme (iki ad: /api-tokens ve /tokens).
	{"*", "/api/v1/api-tokens/*"},
	{"*", "/api/v1/tokens/*"},
	// Ekip uyesi adina token uretme/iptal.
	{"*", "/api/v1/team/members/{id}/tokens/*"},
	// Servis hesaplari (ticari katmanda): kalici, sahipsiz kimlik uretir.
	{"*", "/api/v1/service-accounts/*"},
	// Kalici kimlik bilgileri: posta uygulama parolalari, tunel MCP anahtarlari.
	{"*", "/api/v1/mail/app-passwords/*"},
	{"*", "/api/v1/tunnels/{id}/mcp/keys/*"},
	// CLI/cihaz giris onayi: yeni kalici oturum/kimlik uretir.
	{"POST", "/api/v1/device/approve"},
	// Hesap kimlik dogrulama uclari (parola/2FA/e-posta zaten /api/auth altinda;
	// burada kalan SSO/GitHub/logout akislari oturum akisidir, arac degil).
	{"*", "/api/v1/auth/*"},
	// Kiraciyi tum veriyle silme: confirm alani olsa bile.
	{"DELETE", "/api/v1/organization"},
}

// fullExcluded, (method, path) full kapsamindan haric mi.
func fullExcluded(method, path string) bool {
	for _, rt := range fullExcludedRoutes {
		if rt.method != "*" && rt.method != method {
			continue
		}
		if strings.HasSuffix(rt.pattern, "/*") {
			base := strings.TrimSuffix(rt.pattern, "/*")
			if segMatch(base, path) || segPrefixMatch(base, path) {
				return true
			}
			continue
		}
		if segMatch(rt.pattern, path) {
			return true
		}
	}
	return false
}

// segPrefixMatch, desenin (joker destekli) yolun on eki olup olmadigi (>= desen uzunlugu).
func segPrefixMatch(pattern, path string) bool {
	ps := strings.Split(strings.Trim(pattern, "/"), "/")
	xs := strings.Split(strings.Trim(path, "/"), "/")
	if len(xs) < len(ps) {
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

// userProfile, kullanicinin e-posta ve adini doner (panel oturumundaki AuthUser
// alanlari icin). Bulunamazsa bos doner.
func (m *Middleware) userProfile(ctx context.Context, userID string) (email, name string) {
	pgSt, ok := m.Store.(*pgstore.Store)
	if !ok || pgSt.Pool() == nil {
		return "", ""
	}
	_ = pgSt.Pool().QueryRow(ctx,
		`SELECT coalesce("email",''), coalesce("name",'') FROM "user" WHERE "id" = $1`, userID).Scan(&email, &name)
	return email, name
}

// serveFullPrincipal, full-kapsamli OAuth kimligini panel oturumuyla ayni yetkiyle
// isler. Cagrilmadan once uyelik (role) ve plan kapisi serveOAuthPrincipal'da
// dogrulanmistir; hiz siniri da orada uygulanir.
func (m *Middleware) serveFullPrincipal(w http.ResponseWriter, r *http.Request, p oauthas.Principal, role string) {
	if !strings.HasPrefix(r.URL.Path, "/api/v1/") || fullExcluded(r.Method, r.URL.Path) {
		writeJSONError(w, http.StatusForbidden, "full_scope_excluded",
			"bu uc tam erisim (full) kapsaminda bile kullanilamaz; panelden yapin")
		return
	}
	if role != "owner" && role != "admin" && role != "member" {
		writeJSONError(w, http.StatusForbidden, "forbidden", "rolunuz bu islem icin yetkili degil")
		return
	}
	// Platform admin: panel oturumuyla BIREBIR ayni kural (admin.go).
	isPlat := p.TenantID == store.DefaultTenantID && (role == "owner" || role == "admin")
	if !isPlat {
		isPlat = m.isDefaultTenantOwner(r.Context(), p.UserID)
	}
	email, name := m.userProfile(r.Context(), p.UserID)
	ctx := withTenant(r.Context(), p.TenantID)
	ctx = m.resolveProject(ctx, r, p.TenantID)
	if isPlat {
		ctx = withPlatformAdmin(ctx)
	}
	ctx = withOAuthFull(ctx)
	ctx = withUser(ctx, &AuthUser{ID: p.UserID, Email: email, Name: name, TenantID: p.TenantID, Role: role})
	m.next(w, r.WithContext(ctx))
}
