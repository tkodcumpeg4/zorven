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
	"time"

	"github.com/tkodcumpeg4/zorven/server/api"
	"github.com/tkodcumpeg4/zorven/server/auth"
)

// memberQuota, davet/uye ekleyen yerlesik Better Auth uclarinda Go'nun plan
// kotasini uygulamak icin kullanilan kontrol (entitlements.EntitlementService).
type memberQuota interface {
	CanAddMember(ctx context.Context, tenantID string) error
}

// memberQuotaLocker, (kiraci, kaynak) icin say+ekle kilidi (entitlements.Service).
type memberQuotaLocker interface {
	LockQuota(ctx context.Context, tenantID, resource string) (func(), error)
}

const (
	// quotaLockWait: kilit alma bekleme siniri; asilirsa mevcut davranis (kilitsiz denetim).
	quotaLockWait = 10 * time.Second
	// quotaProxyTimeout: kilit altindaki vekil cagrisinin ust siniri (kilit hic sonsuz tutulmaz).
	quotaProxyTimeout = 15 * time.Second
)

// memberQuotaGate, native organization/invite-member istegini Better Auth'a
// iletmeden ONCE Go tarafindaki uye kotasiyla denetler (F-22/F-45).
type memberQuotaGate struct {
	Quota memberQuota
	// Locker doluysa ayni (kiraci, members) kota kilidi, denetimden vekil cagrisinin
	// bitisine kadar tutulur: denetim ile Better Auth'un davet eklemesi arasinda
	// es zamanli iki istek ayni son koltugu gormesin. nil ise kilitsiz (eski davranis).
	Locker memberQuotaLocker
	// ActiveOrg, oturum token'inin aktif organizasyonunu doner (govdede
	// organizationId yoksa kullanilir); bilinmiyorsa "".
	ActiveOrg func(ctx context.Context, token string) string
}

// check, istek kota denetimine tabi degilse veya kota uygunsa (devam, release, true)
// doner; cagiran vekil cagrisi bitince release() cagirmalidir. Aksi halde Go'nun
// entitlement hata bicimiyle yanit yazar ve false doner. Kilit alinabildiyse istegin
// baglami quotaProxyTimeout ile sinirlanir.
func (g *memberQuotaGate) check(w http.ResponseWriter, r *http.Request) (*http.Request, func(), bool) {
	noop := func() {}
	if g == nil || g.Quota == nil || r.Method != http.MethodPost ||
		!strings.HasSuffix(strings.TrimSuffix(r.URL.Path, "/"), "/organization/invite-member") {
		return r, noop, true
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
		return r, noop, true
	}
	release := noop
	if g.Locker != nil {
		lctx, cancel := context.WithTimeout(r.Context(), quotaLockWait)
		unlock, err := g.Locker.LockQuota(lctx, org, "members")
		cancel()
		if err == nil && unlock != nil {
			pctx, pcancel := context.WithTimeout(r.Context(), quotaProxyTimeout)
			r = r.WithContext(pctx)
			release = func() { pcancel(); unlock() }
		}
		// Kilit alinamadiysa (zaman asimi/depo hatasi) kilitsiz denetim: eski davranis.
	}
	if err := g.Quota.CanAddMember(r.Context(), org); err != nil {
		release()
		api.WriteEntitlementError(w, err)
		return r, noop, false
	}
	return r, release, true
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
		dropTossedAuthCookies(req)
	}
	if cache == nil {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			r, release, ok := gate.check(w, r)
			if !ok {
				return
			}
			defer release()
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
		r, release, ok := gate.check(w, r)
		if !ok {
			return
		}
		defer release()
		if st := authCacheStateFor(r, cache); st != nil {
			st.invalidate(cache)
			r = r.WithContext(context.WithValue(r.Context(), authCacheKey{}, st))
		}
		proxy.ServeHTTP(w, r)
	})
}

// dropTossedAuthCookies, cerez atma korumasi: ayni Better Auth cerez adi farkli
// degerlerle birden cok kez geldiyse (alt alandan atilmis cerez + kurbaninki)
// Better Auth'a hicbir Better Auth cerezi iletilmez; istek oturumsuz islenir.
// (Go API tarafindaki karsiligi: auth.SessionCookieValue.)
func dropTossedAuthCookies(req *http.Request) {
	cs := req.Cookies()
	seen := map[string]string{}
	tossed := false
	for _, c := range cs {
		if !isBetterAuthCookie(c.Name) {
			continue
		}
		if v, ok := seen[c.Name]; ok && v != c.Value {
			tossed = true
			break
		}
		seen[c.Name] = c.Value
	}
	if !tossed {
		return
	}
	req.Header.Del("Cookie")
	for _, c := range cs {
		if !isBetterAuthCookie(c.Name) {
			req.AddCookie(&http.Cookie{Name: c.Name, Value: c.Value})
		}
	}
}

func isBetterAuthCookie(name string) bool {
	return strings.Contains(name, "better-auth.") || strings.HasPrefix(name, "__Host-zorven.")
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
		// Okuma istekleri onbellegi etkilemez; istisnalar:
		//   - e-posta degisim onayi (GET /verify-email): e-posta degisir.
		//     Link baska tarayicida (oturum cerezsiz) acilabilir; o durumda kullanici
		//     bilinmez, basarili yanitta onbellek tumden temizlenir.
		//   - hesap silme onayi (GET /delete-user/callback): kullanici bu istekte
		//     silinir. Kullanici istekten ONCE cerezdeki token'dan cozulur (silindikten
		//     sonra DB'de oturum kalmaz); silinen hesabin oturumu 30 sn gecerli kalmasin.
		//     Oturum cerezi yoksa Better Auth istegi zaten reddeder.
		switch {
		case strings.HasSuffix(path, "/verify-email"):
			st.flushAll = true
		case strings.HasSuffix(path, "/delete-user/callback"):
			if tok == "" {
				return nil
			}
		default:
			return nil
		}
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
