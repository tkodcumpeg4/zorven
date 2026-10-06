package ingress

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

const geoPolicy = `{"rules":[{"match":{"path_prefix":"/admin","country":["TR","DE"]},
  "action":{"type":"deny","status":403}}]}`

func geoReq(target string) *http.Request {
	return httptest.NewRequest(http.MethodGet, target, nil)
}

// Geo kosulu derlenmeli ve needsGeo isaretlenmeli.
func TestGeoMatchCompiles(t *testing.T) {
	cp, _ := compilePolicy([]byte(geoPolicy), 10, nil)
	if cp == nil || len(cp.rules) != 1 {
		t.Fatal("geo kurali derlenmedi")
	}
	mc := cp.rules[0].match
	if !mc.needsGeo {
		t.Error("needsGeo isaretlenmedi")
	}
	if len(mc.country) != 2 {
		t.Errorf("2 ulke bekleniyordu: %v", mc.country)
	}
	if _, ok := mc.country["TR"]; !ok {
		t.Error("TR kumeye girmedi")
	}
}

// Ulke kodu kucuk harf verilse de eslesmelidir.
func TestGeoCountryCaseInsensitive(t *testing.T) {
	raw := `{"rules":[{"match":{"country":["tr"]},"action":{"type":"deny"}}]}`
	cp, _ := compilePolicy([]byte(raw), 10, nil)
	if _, ok := cp.rules[0].match.country["TR"]; !ok {
		t.Error("kucuk harf ulke kodu buyuk harfe cevrilmedi")
	}
}

// KRITIK: DB yokken Geo kosulu eslesmemeli — deny TETIKLENMEMELI.
func TestGeoWithoutDatabaseDoesNotTriggerDeny(t *testing.T) {
	cp, _ := compilePolicy([]byte(geoPolicy), 10, nil)
	rt := routerWith(cp) // rt.geo == nil (DB yok)

	out := rt.evaluatePolicies("example.com", geoReq("http://example.com/admin/panel"),
		"1.2.3.4", "tun_1", false)
	if out.terminal() {
		t.Fatalf("DB yokken deny tetiklendi — istek normal akmaliydi: %+v", out)
	}
}

// Cozulememis bilgi ile hicbir Geo kosulu eslesmemeli.
func TestGeoUnresolvedNeverMatches(t *testing.T) {
	cp, _ := compilePolicy([]byte(geoPolicy), 10, nil)
	mc := cp.rules[0].match
	if mc.matches(geoReq("http://example.com/admin/x"), ipInfo{ok: false}, intelInfo{}) {
		t.Error("cozulememis IP ile Geo kosulu eslesti")
	}
}

// Cozulmus bilgi ile dogru eslesme / eslesmeme.
func TestGeoMatchesResolvedInfo(t *testing.T) {
	cp, _ := compilePolicy([]byte(geoPolicy), 10, nil)
	mc := cp.rules[0].match
	req := geoReq("http://example.com/admin/x")

	if !mc.matches(req, ipInfo{country: "TR", ok: true}, intelInfo{}) {
		t.Error("TR eslesmedi")
	}
	if !mc.matches(req, ipInfo{country: "DE", ok: true}, intelInfo{}) {
		t.Error("DE eslesmedi")
	}
	if mc.matches(req, ipInfo{country: "US", ok: true}, intelInfo{}) {
		t.Error("US eslesmemeliydi")
	}
	// Yol on-eki tutmazsa Geo tutsa bile eslesmemeli.
	if mc.matches(geoReq("http://example.com/public"), ipInfo{country: "TR", ok: true}, intelInfo{}) {
		t.Error("yol on-eki disinda eslesti")
	}
}

// ASN ve ISP kosullari.
func TestGeoASNAndISP(t *testing.T) {
	raw := `{"rules":[{"match":{"asn":[15169],"isp":["Google LLC"]},"action":{"type":"deny"}}]}`
	cp, _ := compilePolicy([]byte(raw), 10, nil)
	mc := cp.rules[0].match
	req := geoReq("http://example.com/")

	if !mc.matches(req, ipInfo{asn: 15169, isp: "Google LLC", ok: true}, intelInfo{}) {
		t.Error("ASN+ISP eslesmedi")
	}
	if mc.matches(req, ipInfo{asn: 15169, isp: "Baska AS", ok: true}, intelInfo{}) {
		t.Error("yanlis ISP ile eslesti")
	}
	if mc.matches(req, ipInfo{asn: 64512, isp: "Google LLC", ok: true}, intelInfo{}) {
		t.Error("yanlis ASN ile eslesti")
	}
	// ISP eslesmesi buyuk/kucuk duyarsiz olmali.
	if !mc.matches(req, ipInfo{asn: 15169, isp: "google llc", ok: true}, intelInfo{}) {
		t.Error("ISP buyuk/kucuk duyarsiz eslesmedi")
	}
}

// Kita kosulu.
func TestGeoContinent(t *testing.T) {
	raw := `{"rules":[{"match":{"continent":["EU"]},"action":{"type":"deny"}}]}`
	cp, _ := compilePolicy([]byte(raw), 10, nil)
	mc := cp.rules[0].match
	req := geoReq("http://example.com/")
	if !mc.matches(req, ipInfo{continent: "EU", ok: true}, intelInfo{}) {
		t.Error("EU eslesmedi")
	}
	if mc.matches(req, ipInfo{continent: "AS", ok: true}, intelInfo{}) {
		t.Error("AS eslesmemeliydi")
	}
}

// Geo kosulu OLMAYAN kural, cozum yapilmadan calismali (needsGeo false).
func TestNonGeoRuleIgnoresGeo(t *testing.T) {
	raw := `{"rules":[{"match":{"path_prefix":"/x"},"action":{"type":"deny"}}]}`
	cp, _ := compilePolicy([]byte(raw), 10, nil)
	if cp.rules[0].match.needsGeo {
		t.Error("Geo kosulu olmayan kuralda needsGeo isaretlendi")
	}
	if !cp.rules[0].match.matches(geoReq("http://example.com/x"), ipInfo{}, intelInfo{}) {
		t.Error("Geo kosulu olmayan kural bos ipInfo ile eslesmedi")
	}
}

// Resolver nil iken lookup guvenli olmali (panic yok).
func TestGeoResolverNilSafe(t *testing.T) {
	var g *geoResolver
	if info := g.lookup("1.2.3.4"); info.ok {
		t.Error("nil resolver ok=true dondurdu")
	}
	g2, err := newGeoResolver("")
	if err != nil || g2 != nil {
		t.Errorf("bos yol nil resolver + nil hata dondurmeli: %v %v", g2, err)
	}
	g2.close() // panic etmemeli
}

// Gecersiz yol hata dondurmeli (sessizce yutulmamali).
func TestGeoResolverBadPath(t *testing.T) {
	if _, err := newGeoResolver("/yok/boyle/bir/dosya.mmdb"); err == nil {
		t.Error("gecersiz yol icin hata bekleniyordu")
	}
}
