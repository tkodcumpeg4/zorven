package api

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestValidPrivateName(t *testing.T) {
	for _, ok := range []string{"db", "db.internal", "nas-1.ev", "a.b.c.d", "x1"} {
		if !validPrivateName(ok) {
			t.Errorf("%q kabul edilmeliydi", ok)
		}
	}
	for _, bad := range []string{"", "-db", "db-", "DB.internal", "db..internal", "db_internal", "db internal", ".db", "db."} {
		if validPrivateName(bad) {
			t.Errorf("%q reddedilmeliydi", bad)
		}
	}
}

func TestParseConnectTarget(t *testing.T) {
	name, port, ok := parseConnectTarget("DB.Internal:5432")
	if !ok || name != "db.internal" || port != "5432" {
		t.Errorf("= %q %q %v (ad kucuk harfe cevrilmeli)", name, port, ok)
	}
	for _, bad := range []string{"", "db.internal", "db.internal:0", "db.internal:70000", "db.internal:abc", "10.0.0.5:22x", "db_x:22"} {
		if _, _, ok := parseConnectTarget(bad); ok {
			t.Errorf("%q reddedilmeliydi", bad)
		}
	}
}

func TestTargetPort(t *testing.T) {
	cases := map[string]string{
		"localhost:5432":      "5432",
		"tcp://10.0.0.5:22":   "22",
		"tcp://10.0.0.5:22/":  "22",
		" 192.168.1.10:3306 ": "3306",
	}
	for in, want := range cases {
		if got, ok := targetPort(in); !ok || got != want {
			t.Errorf("targetPort(%q) = %q %v, beklenen %q", in, got, ok, want)
		}
	}
	if _, ok := targetPort("localhost"); ok {
		t.Error("portsuz hedef kabul edildi")
	}
}

// KRITIK: baglanma ucu cerez/oturumla (API token OLMADAN) gelen istegi
// reddetmeli. Aksi halde kotu niyetli bir sayfa kullanicinin tarayicisi
// uzerinden ozel kaynaga WebSocket acabilirdi. Kontrol her seyden once
// yapildigi icin bos bir Server ile test edilebilir.
func TestConnectNetworkRequiresAPIToken(t *testing.T) {
	s := &Server{}
	r := httptest.NewRequest(http.MethodGet, "/api/v1/network/connect?target=db.internal:5432", nil)
	r = r.WithContext(withUser(r.Context(), &AuthUser{ID: "u1", Role: "owner"}))
	w := httptest.NewRecorder()
	s.connectNetwork(w, r)
	if w.Code != http.StatusForbidden {
		t.Fatalf("durum = %d, 403 beklenirdi (oturumla baglanma reddedilmeli)", w.Code)
	}
}

// Sifir guven: ne kullaniciya ne servis hesabina bagli (kiraci geneli) token
// reddedilir. Aksi halde politika disi sayilip F16/F18 kurallarini atlatirdi.
func TestConnectNetworkRequiresUserBoundToken(t *testing.T) {
	s := &Server{}
	r := httptest.NewRequest(http.MethodGet, "/api/v1/network/connect?target=db.internal:5432", nil)
	r = r.WithContext(withAPIScopes(r.Context(), []string{"*"})) // token var, kullanici yok
	w := httptest.NewRecorder()
	s.connectNetwork(w, r)
	if w.Code != http.StatusForbidden {
		t.Fatalf("durum = %d, 403 beklenirdi", w.Code)
	}
}
