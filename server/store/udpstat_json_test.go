package store

import (
	"encoding/json"
	"testing"
)

// API sozlesmesi: olcum alanlari her zaman vardir; olcum yoksa null (panel
// "istemci surumu desteklemiyor" / "-" ayrimini bununla yapar).
func TestUDPStatMinuteJSONShape(t *testing.T) {
	b, err := json.Marshal(UDPStatMinute{})
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatal(err)
	}
	for _, k := range []string{"rtt_p50_ms", "rtt_p95_ms", "probe_loss_pct"} {
		v, ok := m[k]
		if !ok || v != nil {
			t.Fatalf("%s null olmali (var=%v deger=%v)", k, ok, v)
		}
	}
	if v, ok := m["dropped_datagrams"]; !ok || v != float64(0) {
		t.Fatalf("dropped_datagrams=%v", v)
	}
	p := 7.5
	b, _ = json.Marshal(UDPStatMinute{RTTP50Ms: &p})
	_ = json.Unmarshal(b, &m)
	if m["rtt_p50_ms"] != 7.5 {
		t.Fatalf("deger kayboldu: %v", m["rtt_p50_ms"])
	}
}
