package main

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strings"

	"github.com/tkodcumpeg4/zorven/server/api"
	"github.com/tkodcumpeg4/zorven/server/auth"
)

// memberQuota, davet/uye ekleyen yerlesik Better Auth uclarinda Go'nun plan
// kotasini uygulamak icin kullanilan kontrol (entitlements.EntitlementService).
type memberQuota interface {
	CanAddMember(ctx context.Context, tenantID string) error
}

// memberQuotaGate, native organization/invite-member istegini Better Auth'a
// iletmeden ONCE Go tarafindaki uye kotasiyla denetler (F-22/F-45).
type memberQuotaGate struct {
	Quota memberQuota
	// ActiveOrg, oturum token'inin aktif organizasyonunu doner (govdede
	// organizationId yoksa kullanilir); bilinmiyorsa "".
	ActiveOrg func(ctx context.Context, token string) string
}

// check, istek kota denetimine tabi degilse veya kota uygunsa true doner;
// aksi halde Go'nun entitlement hata bicimiyle yanit yazar ve false doner.
func (g *memberQuotaGate) check(w http.ResponseWriter, r *http.Request) bool {
	if g == nil || g.Quota == nil || r.Method != http.MethodPost ||
		!strings.HasSuffix(strings.TrimSuffix(r.URL.Path, "/"), "/organization/invite-member") {
		return true
	}
	var buf []byte
	if r.Body != nil {
		buf, _ = io.ReadAll(io.LimitReader(r.Body, 1<<20))
		r.Body.Close()
		r.Body = io.NopCloser(bytes.NewReader(buf))
		r.ContentLength = int64(len(buf))
	}
	var body struct {
		OrganizationID string `json:"organizationId"`
	}
	_ = json.Unmarshal(buf, &body)
	org := strings.TrimSpace(body.OrganizationID)
	if org == "" && g.ActiveOrg != nil {
		org = g.ActiveOrg(r.Context(), auth.RequestSessionToken(r))
	}
	if org == "" {
		// Kiraci belirlenemedi: Better Auth kendi yetki kontrolunu yapar.
		return true
	}
	if err := g.Quota.CanAddMember(r.Context(), org); err != nil {
		api.WriteEntitlementError(w, err)
		return false
	}
	return true
}

// sessionCache, auth proxy'sinin Go oturum onbellegini temizlemek icin
// kullandigi arayuz (*auth.BetterAuthVerifier; testte sahte).
type sessionCache interface {
	UserIDForToken(ctx context.Context, token string) string
	InvalidateUser(userID string)
	InvalidateSession(token string)
	InvalidateAll()
}

type authCacheKey struct{}

// authCacheState, istek oncesinde cozulen kimlik; yanit geldiginde (istemciye
// yazilmadan ONCE) ayni kullanicinin onbellegi tekrar temizlenir.
type authCacheState struct {
	token    string
	userID   string
	flushAll bool
}

// newAuthProxy, /api/auth/* isteklerini Better Auth (web/Nitro) servisine
// iletir.
//
//   - X-Forwarded-For istemciden ALINMAZ: sunucu kenarda (:443) calistigi icin
//     RemoteAddr gercek istemci IP'sidir; httputil bunu tek degerli XFF olarak
//     yazar. Better Auth hiz siniri yalnizca tek degerli XFF'ye guvenir; istemcinin
//     uydurdugu XFF eklenseydi ya sahte IP kullanilir ya da tum bu istekler ortak
//     "no-trusted-ip" kovasina duserdi.
//   - Oturum/hesap durumunu degistiren isteklerde (GET disi, oturum token'li;
//     e-posta degisim onayi; sifre sifirlama) Go oturum onbellegi istekten ONCE
//     ve yanit istemciye yazilmadan ONCE temizlenir (30 sn eski oturum/rol,
//     2FA/sifre degisiminden sonra silinen oturumlarin kabulu ve yaris yok).
func newAuthProxy(upstream *url.URL, cache sessionCache, gates ...*memberQuotaGate) http.Handler {
	var gate *memberQuotaGate
	if len(gates) > 0 {
		gate = gates[0]
	}
	proxy := httputil.NewSingleHostReverseProxy(upstream)
	origDirector := proxy.Director
	proxy.Director = func(req *http.Request) {
		origDirector(req)
		req.Host = upstream.Host
		stripForwardHeaders(req.Header)
	}
	if cache == nil {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if !gate.check(w, r) {
				return
			}
			proxy.ServeHTTP(w, r)
		})
	}
	proxy.ModifyResponse = func(resp *http.Response) error {
		st, _ := resp.Request.Context().Value(authCacheKey{}).(*authCacheState)
		if st == nil {
			return nil
		}
		st.invalidate(cache)
		if st.flushAll && resp.StatusCode >= 200 && resp.StatusCode < 400 {
			// Sifre sifirlama (oturumsuz): hangi kullanici oldugu istekten
			// bilinmez; Better Auth tum oturumlarini sildi. Onbellek tumden temizlenir.
			cache.InvalidateAll()
		}
		return nil
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !gate.check(w, r) {
			return
		}
		if st := authCacheStateFor(r, cache); st != nil {
			st.invalidate(cache)
			r = r.WithContext(context.WithValue(r.Context(), authCacheKey{}, st))
		}
		proxy.ServeHTTP(w, r)
	})
}

// stripForwardHeaders, istemcinin gonderdigi (uydurulabilir) iletim basliklarini
// siler. X-Forwarded-For'u httputil RemoteAddr'dan yeniden yazar.
func stripForwardHeaders(h http.Header) {
	for _, k := range []string{"X-Forwarded-For", "Forwarded", "X-Real-Ip", "X-Forwarded-Host", "X-Forwarded-Proto", "X-Client-Ip", "Cf-Connecting-Ip", "True-Client-Ip"} {
		h.Del(k)
	}
}

// authCacheStateFor, istek oturum onbellegini etkiliyorsa durum doner; aksi nil.
func authCacheStateFor(r *http.Request, cache sessionCache) *authCacheState {
	path := strings.TrimSuffix(r.URL.Path, "/")
	tok := auth.RequestSessionToken(r)
	st := &authCacheState{token: tok}
	switch {
	case r.Method == http.MethodPost && strings.HasSuffix(path, "/reset-password"):
		st.flushAll = true
	case r.Method == http.MethodGet || r.Method == http.MethodHead || r.Method == http.MethodOptions:
		// Okuma istekleri onbellegi etkilemez; tek istisna e-posta degisim
		// onayi (GET /verify-email): e-posta degisir.
		// Link baska tarayicida (oturum cerezsiz) acilabilir; o durumda kullanici
		// bilinmez, basarili yanitta onbellek tumden temizlenir.
		if !strings.HasSuffix(path, "/verify-email") {
			return nil
		}
		st.flushAll = true
	default:
		if tok == "" {
			return nil
		}
	}
	if tok != "" {
		st.userID = cache.UserIDForToken(r.Context(), tok)
	}
	return st
}

func (st *authCacheState) invalidate(cache sessionCache) {
	if st.userID != "" {
		cache.InvalidateUser(st.userID)
	}
	if st.token != "" {
		cache.InvalidateSession(st.token)
	}
}
