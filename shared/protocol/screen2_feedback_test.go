package protocol

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

func TestScreen2FeedbackRoundTripAndValid(t *testing.T) {
	in := Screen2Feedback{
		Type: TypeScreen2Feedback, SessionID: "scr_1", WindowMs: 500,
		Layers: []Screen2FeedbackLayer{
			{ID: 0, FPS: 24, Skipped: 0, QueueMs: 0, Viewers: 1},
			{ID: 1, FPS: 9.5, Skipped: 7, QueueMs: 320, Viewers: 2},
		},
	}
	b, err := json.Marshal(in)
	if err != nil {
		t.Fatal(err)
	}
	var out Screen2Feedback
	if err := json.Unmarshal(b, &out); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(in, out) || !out.Valid() {
		t.Fatalf("gidis-donus: %+v", out)
	}
	if !strings.Contains(string(b), `"type":"screen2_feedback"`) {
		t.Fatalf("tip: %s", b)
	}
	if !(Screen2Feedback{}).Valid() {
		t.Fatal("bos geri bildirim gecerli olmali")
	}

	bad := []func(*Screen2Feedback){
		func(f *Screen2Feedback) { f.WindowMs = -1 },
		func(f *Screen2Feedback) { f.Layers[0].ID = 4 },
		func(f *Screen2Feedback) { f.Layers[0].ID = -2 },
		func(f *Screen2Feedback) { f.Layers[1].ID = 0 }, // yinelenen
		func(f *Screen2Feedback) { f.Layers[0].FPS = -0.5 },
		func(f *Screen2Feedback) { f.Layers[0].FPS = 5000 },
		func(f *Screen2Feedback) { f.Layers[1].Skipped = -1 },
		func(f *Screen2Feedback) { f.Layers[1].QueueMs = 70000 },
		func(f *Screen2Feedback) { f.Layers[1].Viewers = -3 },
		func(f *Screen2Feedback) { f.Layers = make([]Screen2FeedbackLayer, 5) },
	}
	for i, f := range bad {
		c := in
		c.Layers = append([]Screen2FeedbackLayer(nil), in.Layers...)
		f(&c)
		if c.Valid() {
			t.Errorf("gecersiz geri bildirim %d kabul edildi", i)
		}
	}
}

// Eski/ileri surum: eksik alanlar sifir, fazla alanlar yok sayilir.
func TestScreen2FeedbackCompat(t *testing.T) {
	var f Screen2Feedback
	if err := json.Unmarshal([]byte(`{"type":"screen2_feedback","session_id":"a","layers":[{"id":1,"fps":8,"yeni":1}],"x":2}`), &f); err != nil {
		t.Fatal(err)
	}
	if len(f.Layers) != 1 || f.Layers[0].FPS != 8 || !f.Valid() {
		t.Fatalf("%+v", f)
	}
	// Eski ajan stats'inda layers yok => gecerli.
	var s Screen2Stats
	if err := json.Unmarshal([]byte(`{"type":"screen2_stats","fps":3}`), &s); err != nil || !s.Valid() || s.Layers != nil {
		t.Fatalf("%v %+v", err, s)
	}
}
