package api

import "context"

// ctxKey, paket-ici context anahtar tipi.
//
// Disari verilmeyen bir tip kullaniyoruz ki baska paketlerin koydugu degerlerle
// cakisma imkansiz olsun (context.WithValue'nun bilinen tuzagi).
type ctxKey int

const (
	ctxKeyTenant ctxKey = iota
	ctxKeyPlatformAdmin
	ctxKeyUser
	ctxKeyAPIScopes
)

// AuthUser, giris yapmis Better Auth kullanicisinin temel bilgileri.
type AuthUser struct {
	ID       string
	Email    string
	Name     string
	TenantID string
	Role     string
}

// withUser, kullanici bilgisini context'e koyar.
func withUser(ctx context.Context, u *AuthUser) context.Context {
	return context.WithValue(ctx, ctxKeyUser, u)
}

// userFromContext, context'teki kullaniciyi okur.
func userFromContext(ctx context.Context) (*AuthUser, bool) {
	v, ok := ctx.Value(ctxKeyUser).(*AuthUser)
	return v, ok && v != nil
}

// withAPIScopes, API token kapsamlarini context'e ekler.
func withAPIScopes(ctx context.Context, scopes []string) context.Context {
	return context.WithValue(ctx, ctxKeyAPIScopes, scopes)
}

// apiScopesFromContext, context'ten API token kapsamlarini okur.
func apiScopesFromContext(ctx context.Context) ([]string, bool) {
	v, ok := ctx.Value(ctxKeyAPIScopes).([]string)
	return v, ok
}

// withTenant, istegin kapsamli oldugu kiraciyi context'e koyar.
// YALNIZCA middleware cagirmalidir.
func withTenant(ctx context.Context, tenantID string) context.Context {
	return context.WithValue(ctx, ctxKeyTenant, tenantID)
}

// tenantFromContext, middleware'in koydugu kiraciyi okur.
// ok=false ise istek kapsamsiz gelmis demektir — bu bir programlama hatasidir.
func tenantFromContext(ctx context.Context) (string, bool) {
	v, ok := ctx.Value(ctxKeyTenant).(string)
	return v, ok && v != ""
}

// withPlatformAdmin, istegin admin anahtariyla geldigini isaretler.
func withPlatformAdmin(ctx context.Context) context.Context {
	return context.WithValue(ctx, ctxKeyPlatformAdmin, true)
}

// isPlatformAdmin, kiraci ustu (platform) yetkisi var mi.
// Kiraci oturumlari icin HER ZAMAN false doner.
func isPlatformAdmin(ctx context.Context) bool {
	v, _ := ctx.Value(ctxKeyPlatformAdmin).(bool)
	return v
}
