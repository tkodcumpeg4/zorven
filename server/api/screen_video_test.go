package api

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/tkodcumpeg4/zorven/server/tunnel"
	"github.com/tkodcumpeg4/zorven/shared/protocol"
)

type videoFakeAgent struct {
	*fakeAgent
	vmu   sync.Mutex
	modes []string
}

func (f *videoFakeAgent) SupportsScreen2Video() bool { return true }
func (f *videoFakeAgent) Screen2Video(_ context.Context, _ string, mode string) error {
	f.vmu.Lock()
	f.modes = append(f.modes, mode)
	f.vmu.Unlock()
	return nil
}
func (f *videoFakeAgent) lastMode() string {
	f.vmu.Lock()
	defer f.vmu.Unlock()
	if len(f.modes) == 0 {
		return ""
	}
	return f.modes[len(f.modes)-1]
}

func waitMode(t *testing.T, ag *videoFakeAgent, want string) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if ag.lastMode() == want {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("ajana mod %q bildirilmedi (son %q)", want, ag.lastMode())
}

func vcfg(t *testing.T) []byte {
	b, err := protocol.VideoMessage{StreamID: 1, Kind: protocol.VideoKindConfig, Keyframe: true, W: 64, H: 64, Codec: "avc1.42e01f"}.Encode()
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func vchunk(t *testing.T, key bool) []byte {
	b, err := protocol.VideoMessage{StreamID: 1, Kind: protocol.VideoKindChunk, Keyframe: key, TimestampUs: 1, Data: []byte{0, 0, 0, 1, 0x65}}.Encode()
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestVideoPolicyTable(t *testing.T) {
	mk := func(vs ...videoState) *screen2Hub {
		h := &screen2Hub{viewers: map[*screen2Viewer]struct{}{}}
		h.vid.ok = true
		for _, s := range vs {
			h.viewers[&screen2Viewer{vid: s}] = struct{}{}
		}
		return h
	}
	sup := func(m string) videoState { return videoState{known: true, supported: true, mode: m} }
	cases := []struct {
		name string
		h    *screen2Hub
		want string
	}{
		{"hic izleyici", mk(), "tiles"},
		{"bekleyen yok sayilir", mk(videoState{}), "tiles"},
		{"tek auto", mk(sup("auto")), "auto"},
		{"auto + video", mk(sup("auto"), sup("video")), "video"},
		{"video + tiles", mk(sup("video"), sup("tiles")), "tiles"},
		{"desteksiz zorlar", mk(sup("video"), videoState{known: true, supported: false, mode: "auto"}), "tiles"},
		{"bekleyen etkilemez", mk(sup("auto"), videoState{}), "auto"},
	}
	for _, c := range cases {
		if got := c.h.effectiveVideoModeLocked(); got != c.want {
			t.Errorf("%s: %s, beklenen %s", c.name, got, c.want)
		}
	}
	h := mk(sup("video"))
	h.vid.ok = false
	if h.effectiveVideoModeLocked() != "tiles" {
		t.Error("ajan pazarlamadiysa tiles olmali")
	}
}

func TestVideoRelayAndPolicy(t *testing.T) {
	var reg screen2Registry
	ag := &videoFakeAgent{fakeAgent: newAuxAgent()}
	v1, err := reg.join("cv1", ag, tunnel.Screen2Options{}, false, false, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer v1.leave()
	h := v1.hub
	if !h.vid.ok {
		t.Fatal("hub video yetenegini algilamadi")
	}
	// Tercih bildirilmeden video gitmez ve ajan tiles'ta kalir.
	if ag.lastMode() != "" {
		t.Fatalf("erken mod: %q", ag.lastMode())
	}
	ag.raw <- vcfg(t)
	time.Sleep(50 * time.Millisecond)
	noRecv(t, v1.out)

	v1.setVideoPrefs(true, "auto")
	waitMode(t, ag, "auto")
	// Katilan izleyiciye config + IDR; ardisik chunk'lar gider.
	ag.raw <- vcfg(t)
	ag.raw <- vchunk(t, true)
	ag.raw <- vchunk(t, false)
	for i, kind := range []uint8{protocol.VideoKindConfig, protocol.VideoKindChunk, protocol.VideoKindChunk} {
		b := recv(t, v1.out)
		if m, err := protocol.DecodeVideo(b); err != nil || m.Kind != kind {
			t.Fatalf("mesaj %d: %+v %v", i, m, err)
		}
	}

	// Desteksiz ikinci izleyici: tum oturum tiles'a zorlanir, video kesilir.
	v2, err := reg.join("cv1", ag, tunnel.Screen2Options{}, false, false, nil)
	if err != nil {
		t.Fatal(err)
	}
	v2.setVideoPrefs(false, "auto")
	waitMode(t, ag, "tiles")
	ag.raw <- vchunk(t, false)
	time.Sleep(50 * time.Millisecond)
	noRecv(t, v1.out)
	// stop mesaji video almis izleyiciye yine de gider.
	stop, _ := protocol.VideoMessage{Kind: protocol.VideoKindStop}.Encode()
	ag.raw <- stop
	if m, err := protocol.DecodeVideo(recv(t, v1.out)); err != nil || m.Kind != protocol.VideoKindStop {
		t.Fatalf("stop iletilmedi: %+v %v", m, err)
	}
	// Desteksiz izleyici ayrilinca video geri gelir.
	v2.leave()
	waitMode(t, ag, "auto")

	// Video isteyen izleyici: video moduna zorlar.
	v1.setVideoPrefs(true, "video")
	waitMode(t, ag, "video")
	// caps bildirimi.
	v3, err := reg.join("cv1", ag, tunnel.Screen2Options{}, false, false, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer v3.leave()
	if m := msgOfType(t, v3, "caps"); m["video"] != true {
		t.Fatalf("caps.video=%v", m["video"])
	}
}

func TestVideoIgnoredWithoutAgentSupport(t *testing.T) {
	var reg screen2Registry
	ag := newAuxAgent() // video yok
	v, err := reg.join("cv2", ag, tunnel.Screen2Options{}, false, false, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer v.leave()
	if v.hub.vid.ok {
		t.Fatal("video algilandi")
	}
	v.setVideoPrefs(true, "video") // sessizce yok sayilir
	ag.raw <- vchunk(t, true)
	time.Sleep(50 * time.Millisecond)
	noRecv(t, v.out)
	if m := msgOfType(t, v, "caps"); m["video"] != false {
		t.Fatalf("caps.video=%v", m["video"])
	}
}

func TestParseVideoControl(t *testing.T) {
	c, ok := parseScreenControl([]byte(`{"type":"video","supported":true,"mode":"video"}`))
	if !ok || c.kind != "video" || !c.value || c.layer != "video" {
		t.Fatalf("%+v %v", c, ok)
	}
}
