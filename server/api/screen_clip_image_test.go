package api

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/tkodcumpeg4/zorven/server/tunnel"
	"github.com/tkodcumpeg4/zorven/shared/protocol"
)

func apiPNG(w, h uint32, extra int) []byte {
	b := []byte{0x89, 'P', 'N', 'G', '\r', '\n', 0x1a, '\n', 0, 0, 0, 13}
	b = append(b, "IHDR"...)
	b = binary.BigEndian.AppendUint32(b, w)
	b = binary.BigEndian.AppendUint32(b, h)
	b = append(b, 8, 6, 0, 0, 0, 0, 0, 0, 0)
	return append(b, make([]byte, extra)...)
}

// newAgent: YENI ajan (gorsel + ping yetenekli). Embedded fakeAgent eski yuzeyi saglar.
type newAgent struct {
	*fakeAgent
	imgOK   bool
	pingOK  bool
	imgMu   sync.Mutex
	imgSets [][]byte
	imgErr  error
	pings   atomic.Int32
	rtt     atomic.Int64 // ns; 0 => olcum yok
}

func (a *newAgent) SupportsScreenClipboardImage() bool { return a.imgOK }
func (a *newAgent) ScreenClipboardImageSet(_ context.Context, _ string, png []byte) error {
	a.imgMu.Lock()
	defer a.imgMu.Unlock()
	if a.imgErr != nil {
		return a.imgErr
	}
	a.imgSets = append(a.imgSets, png)
	return nil
}
func (a *newAgent) SupportsScreenPing() bool         { return a.pingOK }
func (a *newAgent) ScreenPing(context.Context) error { a.pings.Add(1); return nil }
func (a *newAgent) AgentRTT() (time.Duration, bool) {
	d := a.rtt.Load()
	return time.Duration(d), d > 0
}
func (a *newAgent) sets() int { a.imgMu.Lock(); defer a.imgMu.Unlock(); return len(a.imgSets) }

func newImgAgent() *newAgent {
	return &newAgent{fakeAgent: &fakeAgent{curOK: true, clipOK: true}, imgOK: true, pingOK: true}
}

func recvImage(t *testing.T, v *screen2Viewer) []byte {
	t.Helper()
	select {
	case b := <-v.img:
		return b
	case <-time.After(2 * time.Second):
		t.Fatal("pano gorseli gelmedi")
		return nil
	}
}

func noImage(t *testing.T, v *screen2Viewer) {
	t.Helper()
	select {
	case <-v.img:
		t.Fatal("pano gorseli gelmemeliydi")
	case <-time.After(100 * time.Millisecond):
	}
}

func TestHubMonitorCaps(t *testing.T) {
	var reg screen2Registry
	ag := newImgAgent()
	v1, err := reg.join("c1", ag, tunnel.Screen2Options{}, false, false, nil)
	if err != nil {
		t.Fatal(err)
	}
	// Ajan henuz screen_info yollamadi: alanlar HIC yok (panel 1 varsayar).
	m := msgOfType(t, v1, "caps")
	if _, has := m["displays"]; has {
		t.Fatalf("bilgi yokken displays olmamali: %v", m)
	}
	if _, has := m["display"]; has {
		t.Fatalf("bilgi yokken display olmamali: %v", m)
	}
	if m["clipboard_image"] != true {
		t.Fatalf("clipboard_image: %v", m)
	}

	ag.auxCh <- tunnel.ScreenAux{Info: &protocol.ScreenInfo{Displays: 3, Display: 0}}
	m = msgOfType(t, v1, "caps")
	if m["displays"].(float64) != 3 || m["display"].(float64) != 0 {
		t.Fatalf("caps=%v", m)
	}
	// Secili monitor degisti: guncel display panele doner.
	ag.auxCh <- tunnel.ScreenAux{Info: &protocol.ScreenInfo{Displays: 3, Display: 2}}
	m = msgOfType(t, v1, "caps")
	if m["displays"].(float64) != 3 || m["display"].(float64) != 2 {
		t.Fatalf("caps=%v", m)
	}
	// Degisim yoksa tekrar yayinlanmaz.
	ag.auxCh <- tunnel.ScreenAux{Info: &protocol.ScreenInfo{Displays: 3, Display: 2}}
	noMsgOfType(t, v1, "caps")

	// Sonradan katilan, guncel durumu ilk caps'te alir.
	v2, _ := reg.join("c1", ag, tunnel.Screen2Options{}, true, false, nil)
	m = msgOfType(t, v2, "caps")
	if m["displays"].(float64) != 3 || m["display"].(float64) != 2 {
		t.Fatalf("katilan caps=%v", m)
	}
	// Tek monitor: displays=1.
	ag.auxCh <- tunnel.ScreenAux{Info: &protocol.ScreenInfo{Displays: 1, Display: 0}}
	if m = msgOfType(t, v1, "caps"); m["displays"].(float64) != 1 || m["display"].(float64) != 0 {
		t.Fatalf("tek monitor caps=%v", m)
	}
}

func TestHubOldAgentCapsHaveNoNewFields(t *testing.T) {
	var reg screen2Registry
	ag := newAuxAgent() // ESKI ajan: imageAgent/pingAgent yok, screen_info yok
	v, err := reg.join("c1", ag, tunnel.Screen2Options{}, false, false, nil)
	if err != nil {
		t.Fatal(err)
	}
	m := msgOfType(t, v, "caps")
	if m["clipboard"] != true || m["clipboard_image"] != false {
		t.Fatalf("caps=%v", m)
	}
	if _, has := m["displays"]; has {
		t.Fatalf("eski ajanda displays olmamali: %v", m)
	}
	ctx := context.Background()
	if r := v.clipboardImage(ctx, apiPNG(4, 4, 10)); r != clipReasonUnsupported {
		t.Fatalf("eski ajana gorsel gitmemeli: %q", r)
	}
	if m := msgOfType(t, v, "clipboard_status"); m["ok"] != false || m["image"] != true || m["reason"] != clipReasonUnsupported {
		t.Fatalf("status=%v", m)
	}
	// Metin panosu eski ajanda calismaya devam eder.
	if r := v.clipboard(ctx, "merhaba"); r != "" {
		t.Fatalf("metin pano reddedildi: %q", r)
	}
	// ping eski ajanda de calisir (panel<->sunucu), agent_ms olmadan.
	if !v.ping(json.RawMessage("123.5")) {
		t.Fatal("ping yanitlanmali")
	}
	if m := msgOfType(t, v, "pong"); m["t"].(float64) != 123.5 {
		t.Fatalf("pong=%v", m)
	} else if _, has := m["agent_ms"]; has {
		t.Fatalf("eski ajanda agent_ms olmamali: %v", m)
	}
}

func TestHubClipImageRelayOnlyToControllers(t *testing.T) {
	var reg screen2Registry
	ag := newImgAgent()
	ctrl, _ := reg.join("c1", ag, tunnel.Screen2Options{}, false, false, nil)
	ro, _ := reg.join("c1", ag, tunnel.Screen2Options{}, true, false, nil)

	png := apiPNG(8, 8, 100)
	var done atomic.Int32
	ag.auxCh <- tunnel.ScreenAux{ClipImage: png, Done: func() { done.Add(1) }}
	got := recvImage(t, ctrl)
	if got[0] != protocol.PanelClipImageMarker || !bytes.Equal(got[1:], png) {
		t.Fatalf("mesaj bicimi: ilk bayt=%#x", got[0])
	}
	noImage(t, ro)
	deadline := time.Now().Add(time.Second)
	for done.Load() != 1 && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if done.Load() != 1 {
		t.Fatal("Done cagrilmadi (bekleyen sayaci sizar)")
	}

	// Kontrolcu salt-izlemeye gecince almaz.
	ctrl.setViewOnly(true)
	ag.auxCh <- tunnel.ScreenAux{ClipImage: png, Done: func() { done.Add(1) }}
	noImage(t, ctrl)

	// Gecersiz gorsel (PNG degil) iletilmez ama Done yine cagrilir.
	ro.setViewOnly(false)
	ag.auxCh <- tunnel.ScreenAux{ClipImage: []byte("png degil png degil png degil png degil"), Done: func() { done.Add(1) }}
	noImage(t, ro)
	time.Sleep(50 * time.Millisecond)
	if done.Load() != 3 {
		t.Fatalf("Done sayisi=%d", done.Load())
	}

	// Yavas izleyici: yalniz EN YENI gorsel (kapasite 1) tutulur.
	ro2 := apiPNG(9, 9, 50)
	ag.auxCh <- tunnel.ScreenAux{ClipImage: png}
	ag.auxCh <- tunnel.ScreenAux{ClipImage: ro2}
	time.Sleep(100 * time.Millisecond)
	if g := recvImage(t, ro); !bytes.Equal(g[1:], ro2) {
		t.Fatal("en yeni gorsel beklenirdi")
	}
	noImage(t, ro)
}

func TestHubClipImageInboundEnforcement(t *testing.T) {
	var reg screen2Registry
	ag := newImgAgent()
	ctrl, _ := reg.join("c1", ag, tunnel.Screen2Options{}, false, false, nil)
	ro, _ := reg.join("c1", ag, tunnel.Screen2Options{}, true, false, nil)
	ctx := context.Background()
	png := apiPNG(16, 16, 200)

	if r := ro.clipboardImage(ctx, png); r != clipReasonViewOnly {
		t.Fatalf("salt-izleyici reddedilmeli: %q", r)
	}
	if m := msgOfType(t, ro, "clipboard_status"); m["ok"] != false || m["image"] != true || m["reason"] != clipReasonViewOnly {
		t.Fatalf("status=%v", m)
	}
	if r := ctrl.clipboardImage(ctx, make([]byte, protocol.MaxScreenClipboardImage+1)); r != clipReasonTooLarge {
		t.Fatalf("buyuk gorsel: %q", r)
	}
	if r := ctrl.clipboardImage(ctx, []byte("png degil png degil png degil png degil")); r != clipReasonInvalid {
		t.Fatalf("gecersiz gorsel: %q", r)
	}
	if r := ctrl.clipboardImage(ctx, apiPNG(protocol.MaxClipImageDim+1, 4, 0)); r != clipReasonInvalid {
		t.Fatalf("devasa boyut: %q", r)
	}
	if ag.sets() != 0 {
		t.Fatal("reddedilenler ajana gitmemeli")
	}
	// Tam sinirdaki gecerli gorsel kabul; hemen ardindan ikincisi hiz siniri.
	if r := ctrl.clipboardImage(ctx, apiPNG(2, 2, protocol.MaxScreenClipboardImage-33)); r != "" {
		t.Fatalf("tam sinir kabul edilmeli: %q", r)
	}
	if r := ctrl.clipboardImage(ctx, png); r != clipReasonRateLimited {
		t.Fatalf("hiz siniri: %q", r)
	}
	if m := msgOfType(t, ctrl, "clipboard_status"); m["reason"] != clipReasonTooLarge {
		t.Fatalf("ilk status=%v", m)
	}
	ctrl.imgMu.Lock()
	ctrl.lastImg = time.Now().Add(-2 * clipImgGap)
	ctrl.imgMu.Unlock()
	if r := ctrl.clipboardImage(ctx, png); r != "" {
		t.Fatalf("sure dolunca kabul: %q", r)
	}
	if ag.sets() != 2 {
		t.Fatalf("ajana 2 gorsel gitmeliydi: %d", ag.sets())
	}
	// Ajan gonderimi basarisiz: panele bildirilir.
	ag.imgErr = context.DeadlineExceeded
	ctrl.imgMu.Lock()
	ctrl.lastImg = time.Time{}
	ctrl.imgMu.Unlock()
	if r := ctrl.clipboardImage(ctx, png); r != clipReasonUnsupported {
		t.Fatalf("gonderim hatasi: %q", r)
	}
}

func TestHubPingPong(t *testing.T) {
	var reg screen2Registry
	ag := newImgAgent()
	v, _ := reg.join("c1", ag, tunnel.Screen2Options{}, true, false, nil) // salt-izleyici de olcebilir

	if v.ping(json.RawMessage(`"x"`)) || v.ping(json.RawMessage(`{"a":1}`)) || v.ping(nil) || v.ping(json.RawMessage(`1e999999999999999999999999999999999`)) {
		t.Fatal("sayi olmayan t reddedilmeli")
	}
	if !v.ping(json.RawMessage("1234.5")) {
		t.Fatal("ping yanitlanmali")
	}
	m := msgOfType(t, v, "pong")
	if m["t"].(float64) != 1234.5 {
		t.Fatalf("t aynen yankilanmali: %v", m)
	}
	if _, has := m["agent_ms"]; has {
		t.Fatalf("olcum yokken agent_ms olmamali: %v", m)
	}
	deadline := time.Now().Add(time.Second)
	for ag.pings.Load() < 1 && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if ag.pings.Load() != 1 {
		t.Fatal("ajan ping'i tetiklenmeli")
	}
	// Hiz siniri: hemen ardindan gelen ping atilir.
	if v.ping(json.RawMessage("2")) {
		t.Fatal("hiz sinirina takilmali")
	}
	noMsgOfType(t, v, "pong")
	// Ajan olcumu varsa pong'a eklenir.
	ag.rtt.Store(int64(42500 * time.Microsecond))
	v.imgMu.Lock()
	v.lastPing = time.Now().Add(-time.Second)
	v.imgMu.Unlock()
	if !v.ping(json.RawMessage("3")) {
		t.Fatal("sure dolunca yanitlanmali")
	}
	if m := msgOfType(t, v, "pong"); m["agent_ms"].(float64) != 42.5 {
		t.Fatalf("agent_ms: %v", m)
	}
}

func TestParseScreenControlPing(t *testing.T) {
	c, ok := parseScreenControl([]byte(`{"type":"ping","t":12.25}`))
	if !ok || c.kind != "ping" || string(c.t) != "12.25" {
		t.Fatalf("%+v %v", c, ok)
	}
	if c, ok = parseScreenControl([]byte(`{"type":"ping"}`)); !ok || c.kind != "ping" || len(c.t) != 0 {
		t.Fatalf("t'siz ping: %+v", c)
	}
}
