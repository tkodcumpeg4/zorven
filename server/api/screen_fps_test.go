package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/tkodcumpeg4/zorven/server/entitlements"
	"github.com/tkodcumpeg4/zorven/server/tunnel"
	"github.com/tkodcumpeg4/zorven/shared/protocol"
)

// Plan bazli ekran fps tavani: Team/Enterprise (plan 60) 60'a cikabilir, diger
// planlar 30'da kalir; eski ajan (screen2_fps60 yok) plan ne olursa olsun 30'da
// kalir. Tavan sunucuda zorlanir: istemcinin istegine guvenilmez.

// --- yardimcilar -------------------------------------------------------------

// fpsLayerAgent: katmanli ajan + screen2_fps60 yetenegi (acik/kapali).
type fpsLayerAgent struct {
	*fakeLayerAgent
	fps60 bool
}

func (f *fpsLayerAgent) SupportsScreen2FPS60() bool { return f.fps60 }

func newFPSLayerAgent(fps60 bool) *fpsLayerAgent {
	return &fpsLayerAgent{fakeLayerAgent: newLayerAgent(), fps60: fps60}
}

func (f *fpsLayerAgent) openedFPS() int {
	f.lmu.Lock()
	defer f.lmu.Unlock()
	return f.opened.FPS
}

// fpsPlainAgent: katmansiz (eski screen2) ajan; OpenScreen2 secenegini kaydeder.
type fpsPlainAgent struct {
	*fakeAgent
	fps60  bool
	opened tunnel.Screen2Options
}

func (f *fpsPlainAgent) SupportsScreen2FPS60() bool { return f.fps60 }
func (f *fpsPlainAgent) OpenScreen2(ctx context.Context, sid string, o tunnel.Screen2Options,
	frames chan<- []byte, errs chan<- protocol.ScreenError) (uint64, error) {
	f.mu.Lock()
	f.opened = o
	f.mu.Unlock()
	return f.fakeAgent.OpenScreen2(ctx, sid, o, frames, errs)
}

// maxSettingsFPS: ajanin aldigi screen2_settings fps degerlerinin en buyugu.
func maxSettingsFPS(f *fakeAgent) int {
	m := 0
	for _, s := range f.settingsSnapshot() {
		m = max(m, s.FPS)
	}
	return m
}

func lastSettingsFPS(f *fakeAgent) int {
	s := f.settingsSnapshot()
	if len(s) == 0 {
		return -1
	}
	return s[len(s)-1].FPS
}

func fpsSettings(fps int) screenControl {
	return screenControl{kind: "settings", opts: tunnel.Screen2Options{FPS: fps, Display: -1}}
}

// --- saf fonksiyonlar --------------------------------------------------------

func TestPlanFPSCap(t *testing.T) {
	cases := []struct{ plan, want int }{
		{0, 30}, {-3, 30}, // plan bilinmiyor (yetkilendirme servisi yok) => 30
		{10, 10}, {30, 30}, {45, 45}, {60, 60},
		{61, 60}, {120, 60}, // 60 mutlak ust sinir
	}
	for _, c := range cases {
		if got := planFPSCap(c.plan); got != c.want {
			t.Errorf("planFPSCap(%d)=%d, beklenen %d", c.plan, got, c.want)
		}
	}
}

func TestResolveFPSLimit(t *testing.T) {
	cases := []struct{ f, limit, want int }{
		{0, 30, 15}, {0, 60, 15}, // varsayilan hicbir planda yukselmez
		{0, 10, 10}, // tavan varsayilanin altindaysa tavan
		{60, 30, 30}, {60, 60, 60}, {45, 60, 45}, {45, 30, 30}, {20, 60, 20},
	}
	for _, c := range cases {
		if got := resolveFPS(c.f, c.limit); got != c.want {
			t.Errorf("resolveFPS(%d,%d)=%d, beklenen %d", c.f, c.limit, got, c.want)
		}
	}
}

func TestAgentAndViewerFPSCap(t *testing.T) {
	if got := agentFPSCap(&fakeAgent{}); got != 30 {
		t.Errorf("yetenek arayuzu olmayan ajan: %d", got)
	}
	if got := agentFPSCap(newFPSLayerAgent(false)); got != 30 {
		t.Errorf("yetenek ilan etmeyen ajan: %d", got)
	}
	if got := agentFPSCap(newFPSLayerAgent(true)); got != 60 {
		t.Errorf("fps60 ajan: %d", got)
	}
	cases := []struct{ plan, agent, want int }{
		{60, 60, 60}, {60, 30, 30}, {30, 60, 30}, {30, 30, 30}, {0, 60, 30}, {120, 60, 60},
	}
	for _, c := range cases {
		if got := viewerFPSCap(c.plan, c.agent); got != c.want {
			t.Errorf("viewerFPSCap(plan=%d, ajan=%d)=%d, beklenen %d", c.plan, c.agent, got, c.want)
		}
	}
	// Elle kurulmus izleyici (fpsCap yok) eski davranisi korur.
	v := &screen2Viewer{fps: 60}
	if v.fpsLimit() != 30 || v.effFPS() != 30 {
		t.Errorf("fpsCap'siz izleyici: limit=%d eff=%d", v.fpsLimit(), v.effFPS())
	}
	if (&screen2Viewer{}).effFPS() != 15 {
		t.Error("istek yoksa 15")
	}
}

// --- katmanli yayin (hub) ----------------------------------------------------

// 30'luk plan 60 isteyince 30 alir; ajana da 30 gider, ayarla yukseltemez.
func TestFPSPlan30CannotRequest60(t *testing.T) {
	var reg screen2Registry
	fa := newFPSLayerAgent(true) // ajan 60 yapabilse bile plan 30
	v, err := reg.joinCapped("c30", fa, tunnel.Screen2Options{FPS: 60}, false, false, nil, 30)
	if err != nil {
		t.Fatal(err)
	}
	defer v.leave()

	if got := fa.openedFPS(); got != 30 {
		t.Fatalf("ajana acilista %d fps gitti, 30 bekleniyordu", got)
	}
	// join sirasi: caps, ..., viewer_state (msgOfType araya giren mesajlari tuketir)
	if m := msgOfType(t, v, "caps"); m["max_fps"] != float64(30) {
		t.Fatalf("caps.max_fps=%v", m["max_fps"])
	}
	if m := msgOfType(t, v, "viewer_state"); m["fps"] != float64(30) {
		t.Fatalf("viewer_state fps=%v", m["fps"])
	}
	v.applySettings(fpsSettings(60))
	v.hub.mu.Lock()
	got, eff := v.fps, v.effFPS()
	v.hub.mu.Unlock()
	if got != 30 || eff != 30 {
		t.Fatalf("ayarla 60 istenince fps=%d eff=%d", got, eff)
	}
	if m := msgOfType(t, v, "viewer_state"); m["fps"] != float64(30) {
		t.Fatalf("ayardan sonra viewer_state fps=%v", m["fps"])
	}
	time.Sleep(80 * time.Millisecond) // syncLayers (async) varsa yakalansin
	if m := maxSettingsFPS(fa.fakeAgent); m > 30 {
		t.Fatalf("ajana %d fps gitti, plan 30", m)
	}
}

// 60'lik plan + fps60 ajan: 60 istenince 60 alinir; ajan yakalama tavani 60'a cikar.
func TestFPSPlan60ReachesAgent(t *testing.T) {
	var reg screen2Registry
	fa := newFPSLayerAgent(true)
	v, err := reg.joinCapped("c60", fa, tunnel.Screen2Options{FPS: 60}, false, false, nil, 60)
	if err != nil {
		t.Fatal(err)
	}
	defer v.leave()

	if got := fa.openedFPS(); got != 60 {
		t.Fatalf("ajana acilista %d fps gitti, 60 bekleniyordu", got)
	}
	if m := msgOfType(t, v, "caps"); m["max_fps"] != float64(60) {
		t.Fatalf("caps.max_fps=%v", m["max_fps"])
	}
	if m := msgOfType(t, v, "viewer_state"); m["fps"] != float64(60) {
		t.Fatalf("viewer_state fps=%v", m["fps"])
	}
	v.hub.mu.Lock()
	iv := v.interval()
	v.hub.mu.Unlock()
	if iv < 16*time.Millisecond || iv > 17*time.Millisecond {
		t.Fatalf("60 fps kare araligi %v", iv)
	}

	// Izleyici dusurur: ajan yakalama tavani izleyiciyi izler, sonra yeniden 60.
	v.applySettings(fpsSettings(45))
	waitUntil(t, func() bool { return lastSettingsFPS(fa.fakeAgent) == 45 })
	v.applySettings(fpsSettings(60))
	waitUntil(t, func() bool { return lastSettingsFPS(fa.fakeAgent) == 60 })
	// 60 ustu istek (panel degeri) 60'ta kalir.
	v.applySettings(fpsSettings(90))
	v.hub.mu.Lock()
	got := v.fps
	v.hub.mu.Unlock()
	if got != 60 {
		t.Fatalf("90 istenince fps=%d, 60 bekleniyordu", got)
	}
}

// Eski ajan (screen2_fps60 yok): plan 60 olsa bile 30'da kalir; ajana 30 ustu gitmez.
func TestFPSOldAgentStaysAt30(t *testing.T) {
	var reg screen2Registry
	fa := newFPSLayerAgent(false)
	v, err := reg.joinCapped("cold", fa, tunnel.Screen2Options{FPS: 60}, false, false, nil, 60)
	if err != nil {
		t.Fatal(err)
	}
	defer v.leave()

	if got := fa.openedFPS(); got != 30 {
		t.Fatalf("eski ajana acilista %d fps gitti, 30 bekleniyordu", got)
	}
	if m := msgOfType(t, v, "caps"); m["max_fps"] != float64(30) {
		t.Fatalf("eski ajanda caps.max_fps=%v, panel 60 sunmamali", m["max_fps"])
	}
	v.applySettings(fpsSettings(60))
	v.applySettings(fpsSettings(45))
	time.Sleep(80 * time.Millisecond)
	if m := maxSettingsFPS(fa.fakeAgent); m > 30 {
		t.Fatalf("eski ajana %d fps gitti", m)
	}
	if m := msgOfType(t, v, "viewer_state"); m["fps"] != float64(30) {
		t.Fatalf("viewer_state fps=%v", m["fps"])
	}
}

// Varsayilan hicbir planda yukselmez: fps verilmezse ajan varsayilani (0) ve 15.
func TestFPSDefaultStaysFifteen(t *testing.T) {
	var reg screen2Registry
	fa := newFPSLayerAgent(true)
	v, err := reg.joinCapped("cdef", fa, tunnel.Screen2Options{}, false, false, nil, 60)
	if err != nil {
		t.Fatal(err)
	}
	defer v.leave()
	if got := fa.openedFPS(); got != 0 {
		t.Fatalf("istek yokken ajana %d fps gitti (0 = ajan varsayilani)", got)
	}
	if m := msgOfType(t, v, "viewer_state"); m["fps"] != float64(15) {
		t.Fatalf("varsayilan viewer_state fps=%v, 15 bekleniyordu", m["fps"])
	}
	v.hub.mu.Lock()
	sent := v.hub.sentFPS
	v.hub.mu.Unlock()
	if sent != 15 {
		t.Fatalf("hub.sentFPS=%d", sent)
	}
}

// Birden cok izleyici: ajan en buyugunu yakalar, her izleyici kendi hizinda alir.
func TestFPSMultiViewerAgentGetsMax(t *testing.T) {
	var reg screen2Registry
	fa := newFPSLayerAgent(true)
	a, err := reg.joinCapped("cmv", fa, tunnel.Screen2Options{FPS: 15}, false, false, nil, 60)
	if err != nil {
		t.Fatal(err)
	}
	defer a.leave()
	b, err := reg.joinCapped("cmv", fa, tunnel.Screen2Options{FPS: 60}, false, false, nil, 60)
	if err != nil {
		t.Fatal(err)
	}
	waitUntil(t, func() bool { return lastSettingsFPS(fa.fakeAgent) == 60 })
	a.hub.mu.Lock()
	ae, be := a.effFPS(), b.effFPS()
	a.hub.mu.Unlock()
	if ae != 15 || be != 60 {
		t.Fatalf("izleyici hizlari a=%d b=%d", ae, be)
	}
	b.leave()
	waitUntil(t, func() bool { return lastSettingsFPS(fa.fakeAgent) == 15 })
}

// Katmansiz (eski screen2) yayinda da tavan uygulanir; ayar yayini kirpilmis deger tasir.
func TestFPSLegacyHubClampsSettings(t *testing.T) {
	for _, tc := range []struct {
		name  string
		fps60 bool
		plan  int
		want  int
	}{
		{"fps60 yok, plan 60", false, 60, 30},
		{"fps60 var, plan 30", true, 30, 30},
		{"fps60 var, plan 60", true, 60, 60},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var reg screen2Registry
			fa := &fpsPlainAgent{fakeAgent: &fakeAgent{}, fps60: tc.fps60}
			a, err := reg.joinCapped("cl", fa, tunnel.Screen2Options{FPS: 60}, false, false, nil, tc.plan)
			if err != nil {
				t.Fatal(err)
			}
			defer a.leave()
			b, err := reg.joinCapped("cl", fa, tunnel.Screen2Options{FPS: 10}, false, false, nil, tc.plan)
			if err != nil {
				t.Fatal(err)
			}
			defer b.leave()
			if a.hub.layered {
				t.Fatal("katmansiz ajanda katmanli mod acilmamali")
			}
			fa.mu.Lock()
			opened := fa.opened.FPS
			fa.mu.Unlock()
			if opened != tc.want {
				t.Fatalf("acilista fps=%d, beklenen %d", opened, tc.want)
			}
			drainMsgs(b)
			a.applySettings(fpsSettings(60))
			if got := lastSettingsFPS(fa.fakeAgent); got != tc.want {
				t.Fatalf("ajana giden ayar fps=%d, beklenen %d", got, tc.want)
			}
			found := false
			for _, m := range drainMsgs(b) {
				var j map[string]any
				_ = json.Unmarshal(m, &j)
				if j["type"] == "settings_changed" {
					found = true
					if j["fps"] != float64(tc.want) {
						t.Fatalf("settings_changed fps=%v, beklenen %d", j["fps"], tc.want)
					}
				}
			}
			if !found {
				t.Fatal("settings_changed yayinlanmadi")
			}
		})
	}
}

// --- bilet -------------------------------------------------------------------

// fpsEnt: ekran slotunda/ozetinde verilen plan degerini donen sahte yetkilendirme.
type fpsEnt struct {
	entitlements.EntitlementService
	spec entitlements.ScreenStreamSpec
}

func (f *fpsEnt) AcquireScreenSlot(context.Context, string) (func(), entitlements.ScreenStreamSpec, error) {
	return func() {}, f.spec, nil
}
func (f *fpsEnt) GetTenantEntitlements(context.Context, string) (entitlements.TenantEntitlements, error) {
	return entitlements.TenantEntitlements{ScreenSpec: f.spec}, nil
}

// Bilet yanitindaki max_fps sunucunun uygulayacagi tavandir ve bilete islenir
// (WS acilisinda okunur): plan 30 -> 30, 60 -> 60, bilinmiyor -> 30, 120 -> 60.
func TestScreenTicketCarriesMaxFPS(t *testing.T) {
	for _, c := range []struct {
		name      string
		ent       entitlements.EntitlementService
		wantResp  float64
		wantStore int
	}{
		{"yetkilendirme yok", nil, 30, 0},
		{"plan 30", &fpsEnt{spec: entitlements.ScreenStreamSpec{MaxFPS: 30}}, 30, 30},
		{"plan 60", &fpsEnt{spec: entitlements.ScreenStreamSpec{MaxFPS: 60}}, 60, 60},
		{"plan 0", &fpsEnt{spec: entitlements.ScreenStreamSpec{MaxFPS: 0}}, 30, 0},
		{"plan 120", &fpsEnt{spec: entitlements.ScreenStreamSpec{MaxFPS: 120}}, 60, 120},
	} {
		t.Run(c.name, func(t *testing.T) {
			srv := &Server{Store: &remoteTicketStore{}, Hub: tunnel.NewHub(), Tickets: NewTicketStore(), Entitlements: c.ent}
			r := httptest.NewRequest("POST", "/x", strings.NewReader(`{"client_id":"cli_a"}`))
			ctx := withUser(withTenant(r.Context(), "ten_a"), &AuthUser{ID: "u1", Role: "owner"})
			w := httptest.NewRecorder()
			srv.issueScreenTicket(w, r.WithContext(ctx))
			if w.Code != http.StatusOK {
				t.Fatalf("%d %s", w.Code, w.Body)
			}
			var resp struct {
				Ticket string   `json:"ticket"`
				MaxFPS *float64 `json:"max_fps"`
			}
			_ = json.Unmarshal(w.Body.Bytes(), &resp)
			if resp.MaxFPS == nil || *resp.MaxFPS != c.wantResp {
				t.Errorf("yanit max_fps=%v, beklenen %v", resp.MaxFPS, c.wantResp)
			}
			info, ok := srv.Tickets.redeemInfo(ticketKindScreen, resp.Ticket, "ten_a", "")
			if !ok || info.MaxFPS != c.wantStore {
				t.Errorf("bilette MaxFPS=%d ok=%v, beklenen %d", info.MaxFPS, ok, c.wantStore)
			}
		})
	}
}

// --- uctan uca (gercek tunel + api + WS) ------------------------------------
