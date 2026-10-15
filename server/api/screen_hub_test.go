package api

import (
	"context"
	"encoding/json"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/tkodcumpeg4/zorven/server/tunnel"
	"github.com/tkodcumpeg4/zorven/shared/protocol"
)

type fakeAgent struct {
	mu        sync.Mutex
	opens     int
	closes    int
	keyframes int
	settings  []tunnel.Screen2Options
	inputs    []protocol.ScreenInput
	raw       chan<- []byte
	errs      chan<- protocol.ScreenError
	inp       chan<- protocol.ScreenInputStatus

	// imlec/pano (screen_hub_aux_test.go)
	auxCh    chan<- tunnel.ScreenAux
	curOK    bool
	clipOK   bool
	clipSets []string
	watches  []bool
}

func (f *fakeAgent) OpenScreen2(_ context.Context, _ string, _ tunnel.Screen2Options,
	frames chan<- []byte, errs chan<- protocol.ScreenError) (uint64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.opens++
	f.raw, f.errs = frames, errs
	return 1, nil
}
func (f *fakeAgent) Screen2Keyframe(context.Context, string) error {
	f.mu.Lock()
	f.keyframes++
	f.mu.Unlock()
	return nil
}
func (f *fakeAgent) Screen2Settings(_ context.Context, _ string, o tunnel.Screen2Options) error {
	f.mu.Lock()
	f.settings = append(f.settings, o)
	f.mu.Unlock()
	return nil
}
func (f *fakeAgent) CloseScreen(context.Context, string) {
	f.mu.Lock()
	f.closes++
	f.mu.Unlock()
}
func (f *fakeAgent) SubscribeScreenInputStatus(_ string, ch chan<- protocol.ScreenInputStatus) bool {
	f.inp = ch
	return true
}
func (f *fakeAgent) ScreenInput(_ context.Context, in protocol.ScreenInput) error {
	f.mu.Lock()
	f.inputs = append(f.inputs, in)
	f.mu.Unlock()
	return nil
}
func (f *fakeAgent) SubscribeScreenAux(_ string, ch chan<- tunnel.ScreenAux) (bool, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.auxCh = ch
	return f.curOK, f.clipOK
}
func (f *fakeAgent) ScreenClipboardSet(_ context.Context, _ string, text string) error {
	f.mu.Lock()
	f.clipSets = append(f.clipSets, text)
	f.mu.Unlock()
	return nil
}
func (f *fakeAgent) ScreenClipboardWatch(_ context.Context, _ string, enable bool) error {
	f.mu.Lock()
	f.watches = append(f.watches, enable)
	f.mu.Unlock()
	return nil
}
func (f *fakeAgent) get(fn func() int) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return fn()
}

// frame, 14 bayt basliklik sahte screen2 mesaji (bayrak baytu offset 13).
func frame(flags byte, tag byte) []byte {
	b := make([]byte, 16)
	b[13] = flags
	b[15] = tag
	return b
}

const (
	fKey   = 0x02
	fFirst = 0x04
)

func recv(t *testing.T, ch <-chan []byte) []byte {
	t.Helper()
	select {
	case b := <-ch:
		return b
	case <-time.After(2 * time.Second):
		t.Fatal("mesaj gelmedi")
		return nil
	}
}

func noRecv(t *testing.T, ch <-chan []byte) {
	t.Helper()
	select {
	case b := <-ch:
		t.Fatalf("beklenmeyen mesaj: %v", b)
	case <-time.After(80 * time.Millisecond):
	}
}

// msgOfType, v.msgs'ten istenen tipteki ilk JSON mesajini bulur.
func msgOfType(t *testing.T, v *screen2Viewer, typ string) map[string]any {
	t.Helper()
	deadline := time.After(2 * time.Second)
	for {
		select {
		case b := <-v.msgs:
			var m map[string]any
			_ = json.Unmarshal(b, &m)
			if m["type"] == typ {
				return m
			}
		case <-deadline:
			t.Fatalf("%s mesaji gelmedi", typ)
		}
	}
}

func TestHubSharedStreamJoinLeave(t *testing.T) {
	var reg screen2Registry
	ag := &fakeAgent{}
	var released atomic.Int32
	rel := func() { released.Add(1) }

	v1, err := reg.join("c1", ag, tunnel.Screen2Options{}, false, false, rel)
	if err != nil {
		t.Fatal(err)
	}
	v2, err := reg.join("c1", ag, tunnel.Screen2Options{}, false, false, rel)
	if err != nil {
		t.Fatal(err)
	}
	if ag.get(func() int { return ag.opens }) != 1 {
		t.Fatalf("tek yakalama beklenirdi, opens=%d", ag.opens)
	}
	// Ikinci izleyicinin slotu hemen birakilir, ilkininki yayinda kalir.
	if released.Load() != 1 {
		t.Fatalf("ek slot birakilmaliydi, released=%d", released.Load())
	}
	if m := msgOfType(t, v2, "viewers"); m["count"].(float64) != 2 {
		t.Fatalf("viewers=%v", m)
	}

	// Anahtar kare gelene dek iki izleyici de bekler; sonra ikisi de alir.
	ag.raw <- frame(0, 1)
	noRecv(t, v1.out)
	ag.raw <- frame(fKey|fFirst, 2)
	if b := recv(t, v1.out); b[15] != 2 {
		t.Fatalf("v1 yanlis kare %v", b)
	}
	if b := recv(t, v2.out); b[15] != 2 {
		t.Fatalf("v2 yanlis kare %v", b)
	}

	v1.leave()
	if ag.get(func() int { return ag.closes }) != 0 || released.Load() != 1 {
		t.Fatal("son izleyici ayrilmadan yayin kapanmamali")
	}
	v2.leave()
	if ag.get(func() int { return ag.closes }) != 1 {
		t.Fatal("son izleyici ayrilinca CloseScreen beklenirdi")
	}
	if released.Load() != 2 {
		t.Fatalf("yayin slotu birakilmaliydi, released=%d", released.Load())
	}
	if reg.active("c1") {
		t.Fatal("yayin kayittan silinmeliydi")
	}
	// Yeniden katilim yeni yakalama acar.
	if _, err := reg.join("c1", ag, tunnel.Screen2Options{}, false, false, nil); err != nil {
		t.Fatal(err)
	}
	if ag.get(func() int { return ag.opens }) != 2 {
		t.Fatal("yeniden acilis beklenirdi")
	}
}

func TestHubKeyframeOnJoin(t *testing.T) {
	var reg screen2Registry
	ag := &fakeAgent{}
	if _, err := reg.join("c1", ag, tunnel.Screen2Options{}, false, false, nil); err != nil {
		t.Fatal(err)
	}
	time.Sleep(30 * time.Millisecond)
	base := ag.get(func() int { return ag.keyframes })
	if base < 1 {
		t.Fatal("katilimda anahtar kare istenmeli")
	}
	// Hiz siniri: hemen ikinci katilim ek istek yapmaz, 1.1 sn sonra yeniden dener.
	if _, err := reg.join("c1", ag, tunnel.Screen2Options{}, false, false, nil); err != nil {
		t.Fatal(err)
	}
	time.Sleep(30 * time.Millisecond)
	if ag.get(func() int { return ag.keyframes }) != base {
		t.Fatal("hiz siniri asildi")
	}
	time.Sleep(screen2KeyframeRetryJoin + 300*time.Millisecond)
	if ag.get(func() int { return ag.keyframes }) <= base {
		t.Fatal("yeniden deneme anahtar kare istemeli")
	}
}

func TestHubSlowViewerIsolation(t *testing.T) {
	var reg screen2Registry
	ag := &fakeAgent{}
	fast, _ := reg.join("c1", ag, tunnel.Screen2Options{}, false, false, nil)
	slow, _ := reg.join("c1", ag, tunnel.Screen2Options{}, false, false, nil)
	ag.raw <- frame(fKey|fFirst, 0)
	recv(t, fast.out)

	got := 0
	for i := 1; i <= 100; i++ {
		ag.raw <- frame(0, byte(i))
		select {
		case <-fast.out:
			got++
		case <-time.After(time.Second):
			t.Fatal("hizli izleyici tikandi")
		}
	}
	if got != 100 {
		t.Fatalf("hizli izleyici %d kare aldi", got)
	}
	// Yavas izleyici: kuyruk (32 + ilk) doldu, kalanlar dusuruldu, resync.
	slow.hub.mu.Lock()
	rs := slow.resync
	slow.hub.mu.Unlock()
	if !rs {
		t.Fatal("yavas izleyici resync durumunda olmali")
	}
	// Bosalt; yeni anahtar kareye kadar delta gelmez, sonra tekrar akar.
	for len(slow.out) > 0 {
		<-slow.out
	}
	ag.raw <- frame(0, 200)
	recv(t, fast.out)
	noRecv(t, slow.out)
	ag.raw <- frame(fKey|fFirst, 201)
	if b := recv(t, slow.out); b[15] != 201 {
		t.Fatalf("anahtar kare bekleniyordu %v", b)
	}
}

func TestHubSettingsFanOutLastWriterWins(t *testing.T) {
	var reg screen2Registry
	ag := &fakeAgent{}
	v1, _ := reg.join("c1", ag, tunnel.Screen2Options{FPS: 10}, false, false, nil)
	v2, _ := reg.join("c1", ag, tunnel.Screen2Options{}, false, false, nil)
	v1.settings(tunnel.Screen2Options{FPS: 30, Display: -1})
	v2.settings(tunnel.Screen2Options{Quality: 50, Display: -1})
	for _, v := range []*screen2Viewer{v1, v2} {
		var last map[string]any
		deadline := time.After(time.Second)
	loop:
		for {
			select {
			case b := <-v.msgs:
				var m map[string]any
				_ = json.Unmarshal(b, &m)
				if m["type"] == "settings_changed" {
					last = m
					if m["quality"].(float64) == 50 {
						break loop
					}
				}
			case <-deadline:
				t.Fatalf("settings_changed gelmedi: %v", last)
			}
		}
		if last["fps"].(float64) != 30 {
			t.Fatalf("fps 30 olmali: %v", last)
		}
	}
	if len(ag.settings) != 2 {
		t.Fatalf("istemciye 2 ayar gitmeliydi: %d", len(ag.settings))
	}
}

func TestHubViewOnlyDropsInput(t *testing.T) {
	var reg screen2Registry
	ag := &fakeAgent{}
	vo, _ := reg.join("c1", ag, tunnel.Screen2Options{}, true, false, nil)
	ctl, _ := reg.join("c1", ag, tunnel.Screen2Options{}, false, false, nil)
	ctx := context.Background()
	if vo.input(ctx, protocol.ScreenInput{}) {
		t.Fatal("salt-izleyici girdisi dusurulmeli")
	}
	if !ctl.input(ctx, protocol.ScreenInput{}) {
		t.Fatal("denetleyici girdisi gitmeli")
	}
	if len(ag.inputs) != 1 {
		t.Fatalf("inputs=%d", len(ag.inputs))
	}
	m := msgOfType(t, ctl, "viewers")
	if m["count"].(float64) != 2 || m["controllers"].(float64) != 1 {
		t.Fatalf("viewers=%v", m)
	}
	// Kullanici kendisi acabilir; kilitli olan acamaz.
	if vo.setViewOnly(false) {
		t.Fatal("kilitsiz salt-izleyici kapatilabilmeli")
	}
	if !vo.input(ctx, protocol.ScreenInput{}) {
		t.Fatal("kapatinca girdi gitmeli")
	}
	lk, _ := reg.join("c1", ag, tunnel.Screen2Options{}, true, true, nil)
	if !lk.setViewOnly(false) || lk.input(ctx, protocol.ScreenInput{}) {
		t.Fatal("kilitli izleyici salt-izlemede kalmali")
	}
	if m := msgOfType(t, lk, "view_only"); m["locked"] != true {
		t.Fatalf("locked bekleniyordu: %v", m)
	}
}

func TestHubTooManyViewers(t *testing.T) {
	var reg screen2Registry
	ag := &fakeAgent{}
	for i := 0; i < maxScreenViewers; i++ {
		if _, err := reg.join("c1", ag, tunnel.Screen2Options{}, false, false, nil); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := reg.join("c1", ag, tunnel.Screen2Options{}, false, false, nil); err != ErrTooManyViewers {
		t.Fatalf("limit hatasi bekleniyordu: %v", err)
	}
}

func TestHubAgentErrorClosesAll(t *testing.T) {
	var reg screen2Registry
	ag := &fakeAgent{}
	v, _ := reg.join("c1", ag, tunnel.Screen2Options{}, false, false, nil)
	ag.errs <- protocol.ScreenError{Message: "yakalama coktu"}
	select {
	case <-v.hub.done:
	case <-time.After(time.Second):
		t.Fatal("yayin kapanmaliydi")
	}
	msgOfType(t, v, "error")
	if reg.active("c1") {
		t.Fatal("kayittan silinmeli")
	}
}
