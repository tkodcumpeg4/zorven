package tunnel

import (
	"testing"

	"github.com/tkodcumpeg4/zorven/shared/protocol"
)

func TestRouteScreen2StatsGatingAndValidation(t *testing.T) {
	s := newScr2TestSession(t, true)
	ch := make(chan ScreenAux, 4)
	s.SubscribeScreenAux("scr_s", ch)

	ok := mustJSON(t, protocol.Screen2Stats{Type: protocol.TypeScreen2Stats, SessionID: "scr_s", FPS: 30, Method: "gdi"})
	bad := mustJSON(t, protocol.Screen2Stats{Type: protocol.TypeScreen2Stats, SessionID: "scr_s", FPS: -1})
	big := mustJSON(t, protocol.Screen2Stats{Type: protocol.TypeScreen2Stats, SessionID: "scr_s", Quality: 500})

	// Yetenek pazarlanmadi: atilir.
	s.routeScreenAux(protocol.TypeScreen2Stats, ok)
	if len(ch) != 0 {
		t.Fatal("pazarlanmamis istatistik atilmaliydi")
	}
	s.screen2Stats = true
	s.routeScreenAux(protocol.TypeScreen2Stats, bad)
	s.routeScreenAux(protocol.TypeScreen2Stats, big)
	s.routeScreenAux(protocol.TypeScreen2Stats, []byte("{bozuk"))
	if len(ch) != 0 {
		t.Fatalf("gecersiz istatistik atilmaliydi, %d", len(ch))
	}
	s.routeScreenAux(protocol.TypeScreen2Stats, ok)
	if len(ch) != 1 {
		t.Fatalf("1 mesaj beklenirdi, %d", len(ch))
	}
	if m := <-ch; m.Stats == nil || m.Stats.Method != "gdi" || m.Stats.FPS != 30 {
		t.Fatalf("stats=%+v", m.Stats)
	}
}
