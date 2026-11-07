package tunnel

import (
	"context"
	"testing"

	"github.com/tkodcumpeg4/zorven/shared/protocol"
)

func hasFeature(fs []string, f string) bool {
	for _, x := range fs {
		if x == f {
			return true
		}
	}
	return false
}

// screen2_fps60 yalniz istemci ilan ederse etkinlesir ve hello_ack'e yansir;
// eski istemci (ilan yok) 30'da kalir.
func TestHandshakeScreen2FPS60Negotiation(t *testing.T) {
	s, _ := runHello(t, protocol.Hello{
		Type: protocol.TypeHello, ClientVersion: "0.3.3", Platform: "linux/amd64",
		Features: []string{protocol.FeatureScreen2, protocol.FeatureScreen2Layers, protocol.FeatureScreen2FPS60},
	})
	if !s.SupportsScreen2FPS60() || !hasFeature(s.Features(), protocol.FeatureScreen2FPS60) {
		t.Fatalf("ilan edilen yetenek etkinlesmedi: features=%v", s.Features())
	}

	old, _ := runHello(t, protocol.Hello{
		Type: protocol.TypeHello, ClientVersion: "0.3.2", Platform: "linux/amd64",
		Features: []string{protocol.FeatureScreen2, protocol.FeatureScreen2Layers},
	})
	if old.SupportsScreen2FPS60() || hasFeature(old.Features(), protocol.FeatureScreen2FPS60) {
		t.Fatalf("eski istemcide yetenek acik: features=%v", old.Features())
	}

	// screen2 olmadan fps60 tek basina yetmez (screen2 yok => screen2 akisi yok).
	lone, _ := runHello(t, protocol.Hello{
		Type: protocol.TypeHello, ClientVersion: "0.3.3", Platform: "linux/amd64",
		Features: []string{protocol.FeatureScreen2FPS60},
	})
	if lone.SupportsScreen2FPS60() {
		t.Fatal("screen2 olmadan fps60 acik")
	}
}

// ClampFPS: yetenek yoksa 30, varsa 60 ustune cikmaz; 0/negatif aynen gecer.
func TestSessionClampFPS(t *testing.T) {
	s := newScr2TestSession(t, true)
	cases := []struct{ in, base, high int }{
		{0, 0, 0}, {-1, -1, -1}, {15, 15, 15}, {30, 30, 30},
		{31, 30, 31}, {45, 30, 45}, {60, 30, 60}, {61, 30, 60}, {1000, 30, 60},
	}
	for _, c := range cases {
		if got := s.ClampFPS(c.in); got != c.base {
			t.Errorf("yetenek yok ClampFPS(%d)=%d, beklenen %d", c.in, got, c.base)
		}
	}
	s.screen2FPS60 = true
	for _, c := range cases {
		if got := s.ClampFPS(c.in); got != c.high {
			t.Errorf("yetenek var ClampFPS(%d)=%d, beklenen %d", c.in, got, c.high)
		}
	}
	// screen2 kapaliysa yetenek bayragi tek basina yetmez.
	s.screen2 = false
	if got := s.ClampFPS(60); got != 30 {
		t.Errorf("screen2 kapaliyken ClampFPS(60)=%d, 30 bekleniyordu", got)
	}
}

// Ajana giden screen_open ve screen2_settings mesajlari fps'i ajan yetenegine kirpar:
// eski ajan 30 ustu degeri gecersiz sayip 15'e dustugunden ona hic 30 ustu gitmez.
func TestScreen2WireFPSClamped(t *testing.T) {
	for _, hi := range []bool{false, true} {
		s := newScr2TestSession(t, true)
		s.screen2FPS60 = hi
		want := 30
		if hi {
			want = 60
		}
		var open []protocol.ScreenOpen
		var set []protocol.Screen2Settings
		s.sendHook = func(_ context.Context, v any, _ protocol.MessageType) error {
			switch m := v.(type) {
			case protocol.ScreenOpen:
				open = append(open, m)
			case protocol.Screen2Settings:
				set = append(set, m)
			}
			return nil
		}
		ctx := context.Background()
		if _, err := s.OpenScreen2(ctx, "scr_1", Screen2Options{FPS: 90}, make(chan []byte, 1), make(chan protocol.ScreenError, 1)); err != nil {
			t.Fatal(err)
		}
		if err := s.Screen2Settings(ctx, "scr_1", Screen2Options{FPS: 75, Display: -1}); err != nil {
			t.Fatal(err)
		}
		// Ayar yoksa (0) sifir kalir: "degistirme" anlami bozulmaz.
		if err := s.Screen2Settings(ctx, "scr_1", Screen2Options{Quality: 50, Display: -1}); err != nil {
			t.Fatal(err)
		}
		if err := s.OpenScreen(ctx, "scr_2", 50, 0, 0, "mjpeg", make(chan protocol.ScreenFrame, 1), make(chan protocol.ScreenError, 1)); err != nil {
			t.Fatal(err)
		}
		if len(open) != 2 || len(set) != 2 {
			t.Fatalf("hi=%v: open=%d set=%d", hi, len(open), len(set))
		}
		if open[0].FPS != want || set[0].FPS != want || open[1].FPS != min(50, want) {
			t.Errorf("hi=%v: screen2 open=%d settings=%d legacy open=%d, beklenen %d", hi, open[0].FPS, set[0].FPS, open[1].FPS, want)
		}
		if set[1].FPS != 0 {
			t.Errorf("hi=%v: fps verilmeyen ayar sifir kalmali: %d", hi, set[1].FPS)
		}
	}
}
