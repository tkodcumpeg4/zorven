package ingress

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/tkodcumpeg4/zorven/server/store"
)

func htmlGet(host string) *http.Request {
	r := httptest.NewRequest(http.MethodGet, "https://"+host+"/", nil)
	r.Host = host
	r.Header.Set("Accept", "text/html,application/xhtml+xml")
	return r
}

func TestShouldShowInterstitial(t *testing.T) {
	h := &Handler{PlatformDomain: "zorven.app"}
	free := store.HostRoute{Plan: store.PlanFree, Proto: store.ProtoHTTP}

	// Ücretsiz + platform-domain + GET html + çerez yok -> göster.
	if !h.shouldShowInterstitial(free, htmlGet("api--acme.zorven.app")) {
		t.Error("ücretsiz platform tüneli interstitial göstermeli")
	}

	// Onay çerezi varsa -> gösterme.
	r := htmlGet("api--acme.zorven.app")
	r.AddCookie(&http.Cookie{Name: interstitialCookie, Value: "1"})
	if h.shouldShowInterstitial(free, r) {
		t.Error("onay çerezi varken gösterilmemeli")
	}

	// Ücretli katman -> gösterme.
	if h.shouldShowInterstitial(store.HostRoute{Plan: store.PlanPro, Proto: store.ProtoHTTP}, htmlGet("api--acme.zorven.app")) {
		t.Error("ücretli katmanda gösterilmemeli")
	}

	// Özel domain (platform altında değil) -> gösterme.
	if h.shouldShowInterstitial(free, htmlGet("api.musteri.com")) {
		t.Error("özel domainde gösterilmemeli")
	}

	// XHR / asset (Accept html değil) -> gösterme.
	rx := httptest.NewRequest(http.MethodGet, "https://api--acme.zorven.app/data.json", nil)
	rx.Host = "api--acme.zorven.app"
	rx.Header.Set("Accept", "application/json")
	if h.shouldShowInterstitial(free, rx) {
		t.Error("XHR/asset isteğinde gösterilmemeli")
	}

	// POST -> gösterme.
	rp := httptest.NewRequest(http.MethodPost, "https://api--acme.zorven.app/", nil)
	rp.Host = "api--acme.zorven.app"
	rp.Header.Set("Accept", "text/html")
	if h.shouldShowInterstitial(free, rp) {
		t.Error("POST isteğinde gösterilmemeli")
	}

	// Ham TCP tüneli -> gösterme.
	if h.shouldShowInterstitial(store.HostRoute{Plan: store.PlanFree, Proto: store.ProtoTCP}, htmlGet("mc--acme.zorven.app")) {
		t.Error("ham TCP tünelinde gösterilmemeli")
	}

	// PlatformDomain boşsa -> kapalı.
	if (&Handler{}).shouldShowInterstitial(free, htmlGet("api--acme.zorven.app")) {
		t.Error("PlatformDomain boşken kapalı olmalı")
	}
}
