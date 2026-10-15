package api

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/tkodcumpeg4/zorven/server/tunnel"
	"github.com/tkodcumpeg4/zorven/shared/protocol"
)

func newAuxAgent() *fakeAgent { return &fakeAgent{curOK: true, clipOK: true} }

func noMsgOfType(t *testing.T, v *screen2Viewer, typ string) {
	t.Helper()
	deadline := time.After(100 * time.Millisecond)
	for {
		select {
		case b := <-v.msgs:
			var m map[string]any
			_ = json.Unmarshal(b, &m)
			if m["type"] == typ {
				t.Fatalf("beklenmeyen %s mesaji: %s", typ, b)
			}
		case <-deadline:
			return
		}
	}
}

func recvCursor(t *testing.T, v *screen2Viewer) map[string]any {
	t.Helper()
	select {
	case b := <-v.cur:
		var m map[string]any
		_ = json.Unmarshal(b, &m)
		return m
	case <-time.After(2 * time.Second):
		t.Fatal("imlec mesaji gelmedi")
		return nil
	}
}

func TestHubCapsAndCursorRelay(t *testing.T) {
	var reg screen2Registry
	ag := newAuxAgent()
	v1, err := reg.join("c1", ag, tunnel.Screen2Options{}, false, false, nil)
	if err != nil {
		t.Fatal(err)
	}
	// Salt-izleyen de imleci gorur (yalniz gorsel).
	v2, err := reg.join("c1", ag, tunnel.Screen2Options{}, true, false, nil)
	if err != nil {
		t.Fatal(err)
	}
	if m := msgOfType(t, v1, "caps"); m["cursor"] != true || m["clipboard"] != true {
		t.Fatalf("caps=%v", m)
	}

	ag.auxCh <- tunnel.ScreenAux{Shape: &protocol.ScreenCursorShape{W: 1, H: 1, Data: make([]byte, 4)}}
	if m := msgOfType(t, v1, "cursor_shape"); m["w"].(float64) != 1 {
		t.Fatalf("shape=%v", m)
	}
	msgOfType(t, v2, "cursor_shape")

	ag.auxCh <- tunnel.ScreenAux{Cursor: &protocol.ScreenCursor{X: 5, Y: 6, Visible: true}}
	if m := recvCursor(t, v1); m["x"].(float64) != 5 || m["visible"] != true {
		t.Fatalf("cursor=%v", m)
	}
	recvCursor(t, v2)

	// Sonradan katilan, son bicimi ve konumu hemen alir.
	v3, err := reg.join("c1", ag, tunnel.Screen2Options{}, false, false, nil)
	if err != nil {
		t.Fatal(err)
	}
	msgOfType(t, v3, "cursor_shape")
	if m := recvCursor(t, v3); m["y"].(float64) != 6 {
		t.Fatalf("katilan konum=%v", m)
	}
}

func TestHubCursorCoalescesForSlowViewer(t *testing.T) {
	var reg screen2Registry
	ag := newAuxAgent()
	v, err := reg.join("c1", ag, tunnel.Screen2Options{}, false, false, nil)
	if err != nil {
		t.Fatal(err)
	}
	for i := 1; i <= 50; i++ {
		ag.auxCh <- tunnel.ScreenAux{Cursor: &protocol.ScreenCursor{X: i, Y: i, Visible: true}}
	}
	time.Sleep(100 * time.Millisecond)
	// Yavas izleyici yalniz EN YENI konumu gorur; kuyruk tasmaz, kontrol mesajlari acimaz.
	m := recvCursor(t, v)
	if m["x"].(float64) != 50 {
		t.Fatalf("son konum beklenirdi, x=%v", m["x"])
	}
	select {
	case <-v.cur:
		t.Fatal("birlestirilmis tek konum beklenirdi")
	default:
	}
}

func TestHubClipboardOnlyToControllers(t *testing.T) {
	var reg screen2Registry
	ag := newAuxAgent()
	ctrl, err := reg.join("c1", ag, tunnel.Screen2Options{}, false, false, nil)
	if err != nil {
		t.Fatal(err)
	}
	ro, err := reg.join("c1", ag, tunnel.Screen2Options{}, true, false, nil)
	if err != nil {
		t.Fatal(err)
	}
	ag.auxCh <- tunnel.ScreenAux{Clipboard: &protocol.ScreenClipboard{Text: "gizli"}}
	if m := msgOfType(t, ctrl, "clipboard"); m["text"] != "gizli" {
		t.Fatalf("clipboard=%v", m)
	}
	noMsgOfType(t, ro, "clipboard")

	// Izleyici salt-izlemeye gecince artik pano almaz.
	ctrl.setViewOnly(true)
	ag.auxCh <- tunnel.ScreenAux{Clipboard: &protocol.ScreenClipboard{Text: "ikinci"}}
	noMsgOfType(t, ctrl, "clipboard")

	// Asiri buyuk metin sunucuda da dusurulur (tunel katmani zaten eler; savunma).
	ro.setViewOnly(false)
	ag.auxCh <- tunnel.ScreenAux{Clipboard: &protocol.ScreenClipboard{Text: strings.Repeat("a", protocol.MaxScreenClipboardSize+1)}}
	noMsgOfType(t, ro, "clipboard")
}

func TestHubClipboardInboundEnforcement(t *testing.T) {
	var reg screen2Registry
	ag := newAuxAgent()
	ctrl, _ := reg.join("c1", ag, tunnel.Screen2Options{}, false, false, nil)
	ro, _ := reg.join("c1", ag, tunnel.Screen2Options{}, true, false, nil)
	ctx := context.Background()

	if r := ro.clipboard(ctx, "x"); r != clipReasonViewOnly {
		t.Fatalf("salt-izleyici reddedilmeli, r=%q", r)
	}
	if m := msgOfType(t, ro, "clipboard_status"); m["ok"] != false || m["reason"] != clipReasonViewOnly {
		t.Fatalf("status=%v", m)
	}
	if r := ctrl.clipboard(ctx, strings.Repeat("a", protocol.MaxScreenClipboardSize+1)); r != clipReasonTooLarge {
		t.Fatalf("buyuk metin reddedilmeli, r=%q", r)
	}
	if r := ctrl.clipboard(ctx, "bozuk\xff"); r != clipReasonInvalid {
		t.Fatalf("gecersiz utf-8 reddedilmeli, r=%q", r)
	}
	if r := ctrl.clipboard(ctx, strings.Repeat("a", protocol.MaxScreenClipboardSize)); r != "" {
		t.Fatalf("tam sinir kabul edilmeli, r=%q", r)
	}
	if r := ctrl.clipboard(ctx, "merhaba"); r != "" {
		t.Fatalf("kontrolcu kabul edilmeli, r=%q", r)
	}
	ag.mu.Lock()
	n := len(ag.clipSets)
	ag.mu.Unlock()
	if n != 2 {
		t.Fatalf("istemciye 2 pano gitmeliydi, %d", n)
	}

	// Desteksiz istemci
	var reg2 screen2Registry
	ag2 := &fakeAgent{curOK: true}
	v, _ := reg2.join("c2", ag2, tunnel.Screen2Options{}, false, false, nil)
	if r := v.clipboard(ctx, "x"); r != clipReasonUnsupported {
		t.Fatalf("desteksiz istemcide reddedilmeli, r=%q", r)
	}
}

func TestHubClipboardWatchFollowsControllers(t *testing.T) {
	var reg screen2Registry
	ag := newAuxAgent()
	waitWatches := func(want ...bool) {
		t.Helper()
		deadline := time.Now().Add(2 * time.Second)
		for time.Now().Before(deadline) {
			ag.mu.Lock()
			got := append([]bool(nil), ag.watches...)
			ag.mu.Unlock()
			if len(got) == len(want) {
				for i := range want {
					if got[i] != want[i] {
						t.Fatalf("watches=%v want=%v", got, want)
					}
				}
				return
			}
			time.Sleep(10 * time.Millisecond)
		}
		t.Fatalf("watches=%v want=%v", ag.watches, want)
	}
	// Yalniz salt-izleyici: izleme acilmaz.
	ro, _ := reg.join("c1", ag, tunnel.Screen2Options{}, true, false, nil)
	time.Sleep(80 * time.Millisecond)
	ag.mu.Lock()
	if len(ag.watches) != 0 {
		t.Fatalf("kontrolcu yokken izleme acilmamali: %v", ag.watches)
	}
	ag.mu.Unlock()
	// Kontrolcu katilinca acilir.
	ctrl, _ := reg.join("c1", ag, tunnel.Screen2Options{}, false, false, nil)
	waitWatches(true)
	// Kontrolcu salt-izlemeye gecince kapanir.
	ctrl.setViewOnly(true)
	waitWatches(true, false)
	ro.setViewOnly(false)
	waitWatches(true, false, true)
}
