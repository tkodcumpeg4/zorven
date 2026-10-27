package protocol

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

func TestScreen2StatsJSONRoundTripAndValid(t *testing.T) {
	in := Screen2Stats{
		Type: TypeScreen2Stats, SessionID: "scr_1", WindowMs: 1000, FPS: 29.5, CaptureFPS: 30, TargetFPS: 30, MaxFPS: 30,
		Quality: 70, Merged: 2, Idle: 4, KBps: 812.5, Method: "dxgi", SrcW: 1920, SrcH: 1080, W: 1280, H: 720,
		Capture: Screen2StageStat{Avg: 3.2, P95: 6.1, N: 30},
		Write:   Screen2StageStat{Avg: 1, P95: 2, N: 30},
		Layers:  []Screen2LayerStat{{ID: 1, Quality: 40, SpecQuality: 40, TargetFPS: 13}},
	}
	b, err := json.Marshal(in)
	if err != nil {
		t.Fatal(err)
	}
	var out Screen2Stats
	if err := json.Unmarshal(b, &out); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(out, in) || !out.Valid() {
		t.Fatalf("gidis-donus: %+v", out)
	}
	if !strings.Contains(string(b), `"type":"screen2_stats"`) {
		t.Fatalf("tip alani: %s", b)
	}

	bad := []func(*Screen2Stats){
		func(s *Screen2Stats) { s.FPS = -1 },
		func(s *Screen2Stats) { s.Quality = 101 },
		func(s *Screen2Stats) { s.Method = strings.Repeat("x", 33) },
		func(s *Screen2Stats) { s.Encode.P95 = -0.1 },
		func(s *Screen2Stats) { s.TargetFPS = 5000 },
		func(s *Screen2Stats) { s.W = -2 },
		func(s *Screen2Stats) { s.Layers = []Screen2LayerStat{{ID: 9}} },
		func(s *Screen2Stats) { s.Layers = []Screen2LayerStat{{ID: 0, Quality: 101}} },
		func(s *Screen2Stats) { s.Layers = make([]Screen2LayerStat, 5) },
	}
	for i, f := range bad {
		c := in
		f(&c)
		if c.Valid() {
			t.Errorf("gecersiz ozet %d kabul edildi", i)
		}
	}
}

// Eski bir mesaj (alan eksik) veya ileri surumden fazladan alan geriye uyumlu cozulur.
func TestScreen2StatsBackwardForwardCompat(t *testing.T) {
	var s Screen2Stats
	if err := json.Unmarshal([]byte(`{"type":"screen2_stats","session_id":"a","fps":12,"yeni_alan":{"x":1}}`), &s); err != nil {
		t.Fatal(err)
	}
	if s.FPS != 12 || !s.Valid() {
		t.Fatalf("%+v", s)
	}
}
