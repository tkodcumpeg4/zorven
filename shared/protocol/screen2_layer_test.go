package protocol

import "testing"

func TestScreen2LayerFlagRoundTrip(t *testing.T) {
	r := []Screen2Rect{{X: 0, Y: 0, W: 64, H: 64, Enc: Screen2EncSolid, Payload: []byte{1, 2, 3, 255}}}
	for _, l := range []uint8{0, 1, 2, 3} {
		msgs, err := PackScreen2Layer(5, 7, true, l, 64, 64, r, 0)
		if err != nil {
			t.Fatal(err)
		}
		m, err := DecodeScreen2(msgs[0])
		if err != nil || m.Layer != l || !m.Keyframe || !m.First || !m.EOF {
			t.Fatalf("katman %d: %+v %v", l, m, err)
		}
	}
	// katmansiz paketleyici katman 0 yazar
	msgs, _ := PackScreen2(5, 7, false, 64, 64, r, 0)
	if m, _ := DecodeScreen2(msgs[0]); m.Layer != 0 {
		t.Fatal("katmansiz akis katman 0 olmali")
	}
}
