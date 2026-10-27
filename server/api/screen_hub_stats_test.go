package api

import (
	"testing"

	"github.com/tkodcumpeg4/zorven/server/tunnel"
	"github.com/tkodcumpeg4/zorven/shared/protocol"
)

func TestHubRelaysAgentStats(t *testing.T) {
	var reg screen2Registry
	ag := newAuxAgent()
	v1, err := reg.join("c1", ag, tunnel.Screen2Options{}, false, false, nil)
	if err != nil {
		t.Fatal(err)
	}
	v2, err := reg.join("c1", ag, tunnel.Screen2Options{}, true, false, nil)
	if err != nil {
		t.Fatal(err)
	}
	st := &protocol.Screen2Stats{Type: protocol.TypeScreen2Stats, SessionID: "x", FPS: 27.5, Method: "dxgi", TargetFPS: 30,
		Capture: protocol.Screen2StageStat{Avg: 4.5, P95: 9, N: 30}}
	ag.auxCh <- tunnel.ScreenAux{Stats: st}
	for _, v := range []*screen2Viewer{v1, v2} {
		m := msgOfType(t, v, "stats")
		if m["fps"].(float64) != 27.5 || m["method"] != "dxgi" || m["layer"].(float64) != -1 {
			t.Fatalf("stats=%v", m)
		}
		if _, ok := m["session_id"]; ok {
			t.Fatalf("session_id sizdirilmamali: %v", m)
		}
		if c := m["capture"].(map[string]any); c["p95"].(float64) != 9 {
			t.Fatalf("capture=%v", c)
		}
	}
}
