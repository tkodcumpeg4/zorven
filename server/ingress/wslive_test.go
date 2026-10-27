package ingress

import (
	"context"
	"net/http"
	"path/filepath"
	"sync/atomic"
	"testing"

	"github.com/tkodcumpeg4/zorven/server/store"
)

func wsFixture(t *testing.T, route store.HostRoute) (*Handler, *fakeStore) {
	t.Helper()
	fs := &fakeStore{routes: []store.HostRoute{route}}
	rt := NewRouter(fs)
	if err := rt.Reload(context.Background()); err != nil {
		t.Fatal(err)
	}
	h := &Handler{Router: rt, Visitor: newVisitor("google")}
	return h, fs
}

func wsUpgradeReq(h *Handler, email string) *http.Request {
	r := plainReq("/socket")
	r.Header.Set("Upgrade", "websocket")
	if email != "" {
		r.AddCookie(&http.Cookie{Name: "_zva_session", Value: h.Visitor.IssueSessionValue(testHost, email)})
	}
	return r
}

func TestReevaluateVisitorsOAuthAllowListChange(t *testing.T) {
	route := routeFor(t, "oauth", `{"allowed_emails":["a@example.com"]}`)
	h, fs := wsFixture(t, route)
	var closedA, closedB atomic.Int32
	h.registerWS(wsUpgradeReq(h, "a@example.com"), "tun_1", "ten_1", func() { closedA.Add(1) })
	h.registerWS(wsUpgradeReq(h, "b@example.com"), "tun_1", "ten_1", func() { closedB.Add(1) })
	// b@ zaten izinli degil: yukseltme aninda kapatilmis olurdu; burada yalniz
	// degisiklik sonrasi davranisi olcuyoruz. Once a@ ve b@ ikisi de izinli olsun.
	fs.routes[0] = routeFor(t, "oauth", `{"allowed_emails":["a@example.com","b@example.com"]}`)
	_ = h.Router.Reload(context.Background())
	if n := h.ReevaluateVisitors("ten_1"); n != 0 {
		t.Fatalf("degisiklik yok: %d kesilmemeli", n)
	}
	// b@ listeden cikarilir
	fs.routes[0] = routeFor(t, "oauth", `{"allowed_emails":["a@example.com"]}`)
	_ = h.Router.Reload(context.Background())
	if n := h.ReevaluateVisitors("baska"); n != 0 {
		t.Fatalf("baska kiraci etkilenmemeli: %d", n)
	}
	if n := h.ReevaluateVisitors("ten_1"); n != 1 {
		t.Fatalf("yalniz b@ kesilmeliydi: %d", n)
	}
	if closedA.Load() != 0 || closedB.Load() != 1 {
		t.Fatalf("a=%d b=%d", closedA.Load(), closedB.Load())
	}
}

func TestReevaluateVisitorsAccessModeEnabled(t *testing.T) {
	open := rejectRoute() // erisim kapali
	h, fs := wsFixture(t, open)
	var closed atomic.Int32
	h.registerWS(wsUpgradeReq(h, ""), "tun_1", "ten_1", func() { closed.Add(1) })
	if n := h.ReevaluateVisitors(""); n != 0 {
		t.Fatalf("acik tunelde kesilmemeli: %d", n)
	}
	// Tunele sonradan OAuth zorunlulugu gelir: oturumsuz acik WS kesilir.
	fs.routes[0] = routeFor(t, "oauth", `{}`)
	_ = h.Router.Reload(context.Background())
	if n := h.ReevaluateVisitors(""); n != 1 || closed.Load() != 1 {
		t.Fatalf("OAuth zorunlu olunca oturumsuz WS kesilmeli: n=%d", n)
	}
}

func TestReevaluateVisitorsTunnelDisabledAndGeo(t *testing.T) {
	route := rejectRoute()
	route.SecurityConfig = nil
	h, fs := wsFixture(t, route)
	var c1, c2 atomic.Int32
	rm1 := h.registerWS(wsUpgradeReq(h, ""), "tun_1", "ten_1", func() { c1.Add(1) })
	if h.LiveWebSockets("ten_1") != 1 {
		t.Fatal("kayit yok")
	}
	// Geo ayari gelir (203.0.113.7 = AU)
	path := filepath.Join(t.TempDir(), "g.mmdb")
	writeTestMMDB(t, path, map[string]string{"203.0.113.0/24": "AU:OC"})
	h.Router.SetGeoPath(path)
	fs.routes[0].SecurityConfig = []byte(`{"geo":{"mode":"deny","countries":["AU"]}}`)
	_ = h.Router.Reload(context.Background())
	if n := h.ReevaluateVisitors(""); n != 1 || c1.Load() != 1 {
		t.Fatalf("Geo kurali acik WS'yi kesmeli: n=%d", n)
	}
	rm1()
	if h.LiveWebSockets("") != 0 {
		t.Fatal("kayit silinmeli")
	}
	// Tunel pasif
	fs.routes[0].SecurityConfig = nil
	_ = h.Router.Reload(context.Background())
	h.registerWS(wsUpgradeReq(h, ""), "tun_1", "ten_1", func() { c2.Add(1) })
	fs.routes[0].Enabled = false
	_ = h.Router.Reload(context.Background())
	if n := h.ReevaluateVisitors(""); n != 1 || c2.Load() != 1 {
		t.Fatalf("pasif tunelin WS'i kesilmeli: n=%d", n)
	}
}
