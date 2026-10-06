package ingress

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

func intelReq() *http.Request {
	return httptest.NewRequest(http.MethodGet, "http://example.com/", nil)
}

// tor_exit / hosting / reputation kosullari derlenip needsIntel isaretlenmeli.
func TestIntelMatchCompiles(t *testing.T) {
	raw := `{"rules":[{"match":{"tor_exit":true,"hosting":false,"reputation":["Spamhaus"]},
	  "action":{"type":"deny","status":403}}]}`
	cp, _ := compilePolicy([]byte(raw), 10, nil)
	if cp == nil || len(cp.rules) != 1 {
		t.Fatal("kural derlenmedi")
	}
	mc := cp.rules[0].match
	if !mc.needsIntel {
		t.Fatal("needsIntel isaretlenmedi")
	}
	if mc.needsGeo {
		t.Error("yalnizca liste kosulu varken needsGeo isaretlenmemeli")
	}
	if mc.torExit == nil || !*mc.torExit {
		t.Error("tor_exit=true derlenmedi")
	}
	if mc.hosting == nil || *mc.hosting {
		t.Error("hosting=false derlenmedi")
	}
	if _, ok := mc.reputation["spamhaus"]; !ok {
		t.Errorf("reputation adi kucuk harfe cevrilmedi: %v", mc.reputation)
	}
}

func TestIntelTorExitMatching(t *testing.T) {
	raw := `{"rules":[{"match":{"tor_exit":true},"action":{"type":"deny"}}]}`
	cp, _ := compilePolicy([]byte(raw), 10, nil)
	mc := cp.rules[0].match
	if !mc.matches(intelReq(), ipInfo{}, intelInfo{torExit: true}) {
		t.Error("tor cikis dugumu eslesmeliydi")
	}
	if mc.matches(intelReq(), ipInfo{}, intelInfo{torExit: false}) {
		t.Error("tor olmayan adres eslesmemeliydi")
	}
}

// tor_exit:false, "Tor OLMAYAN" anlamina gelir (allow-list kaliplari icin).
func TestIntelTorExitFalseIsNegation(t *testing.T) {
	raw := `{"rules":[{"match":{"tor_exit":false},"action":{"type":"deny"}}]}`
	cp, _ := compilePolicy([]byte(raw), 10, nil)
	mc := cp.rules[0].match
	if !mc.matches(intelReq(), ipInfo{}, intelInfo{torExit: false}) {
		t.Error("tor olmayan adres tor_exit:false ile eslesmeliydi")
	}
	if mc.matches(intelReq(), ipInfo{}, intelInfo{torExit: true}) {
		t.Error("tor adresi tor_exit:false ile eslesmemeliydi")
	}
}

func TestIntelReputationAnyMatch(t *testing.T) {
	raw := `{"rules":[{"match":{"reputation":["spamhaus","abuse"]},"action":{"type":"deny"}}]}`
	cp, _ := compilePolicy([]byte(raw), 10, nil)
	mc := cp.rules[0].match
	if !mc.matches(intelReq(), ipInfo{}, intelInfo{reputation: []string{"abuse"}}) {
		t.Error("listelerden biri yeterli olmaliydi (OR)")
	}
	if mc.matches(intelReq(), ipInfo{}, intelInfo{reputation: []string{"baska"}}) {
		t.Error("ilgisiz liste eslesmemeliydi")
	}
	if mc.matches(intelReq(), ipInfo{}, intelInfo{}) {
		t.Error("bos itibar sonucu eslesmemeliydi")
	}
}

// Ayni kuraldaki birden fazla liste kosulu VE ile birlesir.
func TestIntelConditionsAreAnded(t *testing.T) {
	raw := `{"rules":[{"match":{"tor_exit":true,"hosting":true},"action":{"type":"deny"}}]}`
	cp, _ := compilePolicy([]byte(raw), 10, nil)
	mc := cp.rules[0].match
	if !mc.matches(intelReq(), ipInfo{}, intelInfo{torExit: true, hosting: true}) {
		t.Error("her iki kosul saglaninca eslesmeliydi")
	}
	if mc.matches(intelReq(), ipInfo{}, intelInfo{torExit: true}) {
		t.Error("tek kosul yeterli olmamaliydi")
	}
}

// Geo + liste kosulu ayni kuralda birlikte calismali.
func TestGeoAndIntelCombined(t *testing.T) {
	raw := `{"rules":[{"match":{"country":["TR"],"tor_exit":true},"action":{"type":"deny"}}]}`
	cp, _ := compilePolicy([]byte(raw), 10, nil)
	mc := cp.rules[0].match
	if !mc.needsGeo || !mc.needsIntel {
		t.Fatal("hem needsGeo hem needsIntel isaretlenmeliydi")
	}
	if !mc.matches(intelReq(), ipInfo{country: "TR", ok: true}, intelInfo{torExit: true}) {
		t.Error("ikisi de saglaninca eslesmeliydi")
	}
	if mc.matches(intelReq(), ipInfo{country: "DE", ok: true}, intelInfo{torExit: true}) {
		t.Error("ulke tutmayinca eslesmemeliydi")
	}
	if mc.matches(intelReq(), ipInfo{country: "TR", ok: true}, intelInfo{}) {
		t.Error("tor kosulu tutmayinca eslesmemeliydi")
	}
}

// KRITIK: liste saglayicisi tanimsizken kosul eslesmemeli — deny TETIKLENMEMELI.
func TestIntelWithoutProvidersDoesNotTriggerDeny(t *testing.T) {
	raw := `{"rules":[{"match":{"tor_exit":true},"action":{"type":"deny","status":403}}]}`
	cp, _ := compilePolicy([]byte(raw), 10, nil)
	rt := routerWith(cp) // rt.intel == nil

	out := rt.evaluatePolicies("example.com", intelReq(), "185.220.101.5", "tun_1", false)
	if out.terminal() {
		t.Fatalf("saglayici yokken deny tetiklendi: %+v", out)
	}
}

// Uctan uca: gercek listeyle yuklu saglayici deny uretmeli.
func TestIntelEndToEndDeny(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "tor.txt")
	if err := os.WriteFile(src, []byte("185.220.101.0/24\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	p := newIPListProvider("tor", []string{src}, "", quietLog())
	if err := p.refresh(context.Background()); err != nil {
		t.Fatal(err)
	}

	raw := `{"rules":[{"match":{"tor_exit":true},"action":{"type":"deny","status":403}}]}`
	cp, _ := compilePolicy([]byte(raw), 10, nil)
	rt := routerWith(cp)
	rt.SetIPIntel(&ipIntel{tor: p})

	out := rt.evaluatePolicies("example.com", intelReq(), "185.220.101.5", "tun_1", false)
	if !out.terminal() {
		t.Fatalf("tor cikis IP'si icin deny bekleniyordu: %+v", out)
	}

	clean := rt.evaluatePolicies("example.com", intelReq(), "8.8.8.8", "tun_1", false)
	if clean.terminal() {
		t.Fatalf("temiz IP icin deny tetiklenmemeliydi: %+v", clean)
	}
}
