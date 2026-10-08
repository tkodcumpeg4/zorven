package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/tkodcumpeg4/zorven/server/entitlements"
	"github.com/tkodcumpeg4/zorven/server/store"
)

func TestDeriveDoorHost(t *testing.T) {
	hs := []store.Hostname{{FQDN: "ssh--acme.zorven.app"}}
	if got := deriveDoorHost(hs, "tun_ab12", "zorven.app"); got != "door--ssh--acme.zorven.app" {
		t.Fatalf("beklenmedik: %s", got)
	}
	// Platform disi / noktali adlar yok sayilir; tunel kimliginden turetilir.
	hs = []store.Hostname{{FQDN: "api.musteri.com"}, {FQDN: "a.b.zorven.app"}}
	if got := deriveDoorHost(hs, "tun_ab12", "zorven.app"); got != "door--id--tunab12.zorven.app" {
		t.Fatalf("kimlik tabanli bekleniyordu: %s", got)
	}
	// 63 karakteri asan etiket.
	long := strings.Repeat("a", 60) + ".zorven.app"
	if got := deriveDoorHost([]store.Hostname{{FQDN: long}}, "tun_1", "zorven.app"); !strings.HasPrefix(got, "door--id--") {
		t.Fatalf("uzun etiket kimlige dusmeli: %s", got)
	}
	if deriveDoorHost(hs, "tun_1", "") != "" {
		t.Fatal("platform yokken bos olmali")
	}
}

type doorAPIStore struct {
	store.Store
	tun    store.Tunnel
	door   store.TunnelDoor
	policy store.TunnelAccessPolicy
	host   []store.Hostname
	grants []store.DoorGrant
}

func (f *doorAPIStore) GetTunnel(_ context.Context, _, id string) (store.Tunnel, error) {
	if id != f.tun.ID {
		return store.Tunnel{}, store.ErrNotFound
	}
	return f.tun, nil
}
func (f *doorAPIStore) GetTunnelDoor(context.Context, string, string) (store.TunnelDoor, error) {
	if f.door.DurationSec == 0 {
		f.door.DurationSec = store.DoorDuration12h
	}
	return f.door, nil
}
func (f *doorAPIStore) SetTunnelDoor(_ context.Context, _ string, d store.TunnelDoor) error {
	f.door = d
	return nil
}
func (f *doorAPIStore) GetTunnelAccessPolicy(context.Context, string, string) (store.TunnelAccessPolicy, error) {
	return f.policy, nil
}
func (f *doorAPIStore) ListHostnamesByTunnel(context.Context, string, string) ([]store.Hostname, error) {
	return f.host, nil
}
func (f *doorAPIStore) ListDoorGrants(context.Context, string, string, bool, int) ([]store.DoorGrant, error) {
	return f.grants, nil
}
func (f *doorAPIStore) RevokeDoorGrant(_ context.Context, _, _, id string) (store.DoorGrant, error) {
	for _, g := range f.grants {
		if g.ID == id {
			return g, nil
		}
	}
	return store.DoorGrant{}, store.ErrNotFound
}

type proOnlyEnt struct {
	entitlements.EntitlementService
	pro bool
}

func (p proOnlyEnt) CheckFeature(_ context.Context, _ string, f entitlements.Feature) error {
	if f == entitlements.FeatureWebDoor && !p.pro {
		return &entitlements.EntitlementError{Code: "feature_not_available", Message: "Pro gerekli"}
	}
	return nil
}

func doorServer(pro bool) (*Server, *doorAPIStore) {
	st := &doorAPIStore{
		tun:    store.Tunnel{ID: "tun_1", Proto: store.ProtoTCP, Exposure: store.ExposurePort, PublicPort: 10005},
		policy: store.TunnelAccessPolicy{TunnelID: "tun_1", Mode: "basic", Enabled: true},
		host:   []store.Hostname{{FQDN: "ssh--acme.zorven.app"}},
	}
	return &Server{Store: st, Entitlements: proOnlyEnt{pro: pro}, PlatformDomain: "zorven.app"}, st
}

func doorCall(s *Server, h http.HandlerFunc, method, body string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(method, "/api/v1/tunnels/tun_1/door", strings.NewReader(body))
	r = r.WithContext(withTenant(r.Context(), "ten_1"))
	r.SetPathValue("id", "tun_1")
	r.SetPathValue("grantId", "dg_1")
	w := httptest.NewRecorder()
	h(w, r)
	return w
}

func TestSetTunnelDoorEntitlementGate(t *testing.T) {
	s, st := doorServer(false)
	w := doorCall(s, s.setTunnelDoor, "PUT", `{"enabled":true,"duration_sec":3600}`)
	if w.Code != http.StatusForbidden || !strings.Contains(w.Body.String(), "feature_not_available") {
		t.Fatalf("alt planda 403 feature_not_available bekleniyordu: %d %s", w.Code, w.Body.String())
	}
	if st.door.Enabled {
		t.Fatal("alt planda kapi acilmamali")
	}
	// Kapatmak her planda serbest.
	if w := doorCall(s, s.setTunnelDoor, "PUT", `{"enabled":false}`); w.Code != http.StatusOK {
		t.Fatalf("kapatma serbest olmali: %d %s", w.Code, w.Body.String())
	}
	// GET alt planda da calisir ve available=false doner (panel yukseltme gosterir).
	w = doorCall(s, s.getTunnelDoor, "GET", "")
	var v map[string]any
	_ = json.Unmarshal(w.Body.Bytes(), &v)
	if w.Code != 200 || v["available"] != false {
		t.Fatalf("available=false bekleniyordu: %s", w.Body.String())
	}
}

func TestSetTunnelDoorPro(t *testing.T) {
	s, st := doorServer(true)

	w := doorCall(s, s.setTunnelDoor, "PUT", `{"enabled":true,"duration_sec":999}`)
	if w.Code != http.StatusUnprocessableEntity || !strings.Contains(w.Body.String(), "invalid_duration") {
		t.Fatalf("gecersiz sure 422: %d %s", w.Code, w.Body.String())
	}
	st.policy = store.TunnelAccessPolicy{TunnelID: "tun_1", Mode: "none"}
	w = doorCall(s, s.setTunnelDoor, "PUT", `{"enabled":true,"duration_sec":3600}`)
	if w.Code != http.StatusUnprocessableEntity || !strings.Contains(w.Body.String(), "access_not_configured") {
		t.Fatalf("giris yontemi yoksa 422: %d %s", w.Code, w.Body.String())
	}
	st.policy = store.TunnelAccessPolicy{TunnelID: "tun_1", Mode: "oauth", Enabled: true}
	w = doorCall(s, s.setTunnelDoor, "PUT", `{"enabled":true,"duration_sec":86400}`)
	if w.Code != http.StatusOK {
		t.Fatalf("200 bekleniyordu: %d %s", w.Code, w.Body.String())
	}
	var v map[string]any
	_ = json.Unmarshal(w.Body.Bytes(), &v)
	if v["url"] != "https://door--ssh--acme.zorven.app/" || v["connect_address"] != "zorven.app:10005" ||
		v["enabled"] != true || v["duration_sec"] != float64(86400) {
		t.Fatalf("beklenmedik yanit: %v", v)
	}
	if st.door.Host != "door--ssh--acme.zorven.app" {
		t.Fatalf("host kaydedilmedi: %q", st.door.Host)
	}

	// HTTP tuneli: 409.
	st.tun.Proto = store.ProtoHTTP
	if w := doorCall(s, s.getTunnelDoor, "GET", ""); w.Code != http.StatusConflict {
		t.Fatalf("http tunelinde 409 bekleniyordu: %d", w.Code)
	}
}

func TestRevokeDoorGrantAPI(t *testing.T) {
	s, st := doorServer(true)
	st.grants = []store.DoorGrant{{ID: "dg_1", TunnelID: "tun_1", IP: "203.0.113.7"}}
	if w := doorCall(s, s.revokeDoorGrant, "DELETE", ""); w.Code != http.StatusNoContent {
		t.Fatalf("204 bekleniyordu: %d", w.Code)
	}
	st.grants = nil
	if w := doorCall(s, s.revokeDoorGrant, "DELETE", ""); w.Code != http.StatusNotFound {
		t.Fatalf("404 bekleniyordu: %d", w.Code)
	}
}
