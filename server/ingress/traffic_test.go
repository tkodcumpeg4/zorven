package ingress

import (
	"net/http"
	"testing"
)

func TestParseTrafficPolicy(t *testing.T) {
	if p := parseTrafficPolicy(nil); p != nil {
		t.Fatal("nil ham veri nil dönmeli")
	}
	if p := parseTrafficPolicy([]byte(`{}`)); p != nil {
		t.Fatal("boş politika nil dönmeli (kural yok)")
	}
	if p := parseTrafficPolicy([]byte(`{bozuk`)); p != nil {
		t.Fatal("geçersiz JSON nil dönmeli")
	}
	p := parseTrafficPolicy([]byte(`{"response_headers":{"set":{"X-A":"1"}}}`))
	if p == nil {
		t.Fatal("geçerli politika parse edilmeli")
	}
}

func TestApplyHeaders(t *testing.T) {
	p := parseTrafficPolicy([]byte(`{
		"request_headers":  {"set": {"X-Req":"1"}, "remove": ["X-Drop-Req"]},
		"response_headers": {"set": {"X-Frame-Options":"DENY"}, "remove": ["Server"]}
	}`))
	if p == nil {
		t.Fatal("politika nil")
	}

	req := http.Header{"X-Drop-Req": {"gone"}, "Keep": {"yes"}}
	p.applyToRequest(req)
	if req.Get("X-Req") != "1" {
		t.Error("istek set uygulanmadı")
	}
	if req.Get("X-Drop-Req") != "" {
		t.Error("istek remove uygulanmadı")
	}
	if req.Get("Keep") != "yes" {
		t.Error("ilgisiz başlık korunmalıydı")
	}

	resp := http.Header{"Server": {"nginx"}}
	p.applyToResponse(resp)
	if resp.Get("X-Frame-Options") != "DENY" {
		t.Error("yanıt set uygulanmadı")
	}
	if resp.Get("Server") != "" {
		t.Error("yanıt remove uygulanmadı")
	}
}

func TestApplyNilPolicy(t *testing.T) {
	// nil politika (kural yok): panik olmamalı, başlıklar değişmemeli.
	var p *trafficPolicy
	h := http.Header{"A": {"1"}}
	p.applyToRequest(h)
	p.applyToResponse(h)
	if h.Get("A") != "1" {
		t.Error("nil politika başlıkları değiştirmemeli")
	}
	if _, _, ok := p.matchRedirect("/x"); ok {
		t.Error("nil politika redirect eşleştirmemeli")
	}
}

func TestMatchRedirect(t *testing.T) {
	p := parseTrafficPolicy([]byte(`{"redirects":[
		{"match_prefix":"/eski", "location":"/yeni", "status":301},
		{"match_prefix":"/eski/derin", "location":"/derin-yeni", "status":302},
		{"match_prefix":"", "location":"/kok", "status":0}
	]}`))
	if p == nil {
		t.Fatal("politika nil")
	}

	// En uzun ön-ek kazanır.
	if loc, st, ok := p.matchRedirect("/eski/derin/x"); !ok || loc != "/derin-yeni" || st != 302 {
		t.Errorf("derin eşleşme = %q %d %v; istenen /derin-yeni 302", loc, st, ok)
	}
	// Kısa ön-ek.
	if loc, st, ok := p.matchRedirect("/eski/y"); !ok || loc != "/yeni" || st != 301 {
		t.Errorf("kısa eşleşme = %q %d %v; istenen /yeni 301", loc, st, ok)
	}
	// Boş ön-ek her şeye uyar; status 0 → 302'ye normalize.
	if loc, st, ok := p.matchRedirect("/baska"); !ok || loc != "/kok" || st != http.StatusFound {
		t.Errorf("boş ön-ek eşleşmesi = %q %d %v; istenen /kok 302", loc, st, ok)
	}
}
