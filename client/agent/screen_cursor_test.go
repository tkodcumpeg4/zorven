package agent

import (
	"testing"
	"time"

	"github.com/tkodcumpeg4/zorven/shared/protocol"
)

type seqCursorSrc struct {
	states []cursorState
	i      int
	shapes map[uintptr]*cursorShape
	calls  int
}

func (s *seqCursorSrc) Poll() (cursorState, error) {
	st := s.states[min(s.i, len(s.states)-1)]
	s.i++
	return st, nil
}
func (s *seqCursorSrc) Shape(h uintptr) (*cursorShape, bool) {
	s.calls++
	sh, ok := s.shapes[h]
	return sh, ok
}
func (s *seqCursorSrc) Close() {}

func shapeOf(v byte) *cursorShape {
	return &cursorShape{W: 1, H: 1, RGBA: []byte{v, v, v, 255}}
}

// Animasyonlu imlec her adimda yeni tutac verir ama icerik ayniysa panele
// tekrar bicim GONDERILMEMELI.
func TestCursorTrackerDedupsByContent(t *testing.T) {
	src := &seqCursorSrc{
		states: []cursorState{
			{Visible: true, Handle: 1}, {Visible: true, Handle: 2}, {Visible: true, Handle: 3}, {Visible: true, Handle: 4},
		},
		shapes: map[uintptr]*cursorShape{1: shapeOf(10), 2: shapeOf(10), 3: shapeOf(20), 4: shapeOf(20)},
	}
	var shapes int
	tr := newCursorTracker(src, func() displayRect { return displayRect{w: 10, h: 10} },
		func() (int, int) { return 10, 10 }, func(protocol.ScreenCursor) {}, func(protocol.ScreenCursorShape) { shapes++ })
	now := time.Unix(100, 0)
	for i := 0; i < 4; i++ {
		tr.Step(now.Add(time.Duration(i) * time.Second))
	}
	if shapes != 2 {
		t.Fatalf("yalniz icerik degisince gonderilmeli: %d", shapes)
	}
	// Ayni tutac tekrar gorulurse Shape() yeniden cagrilmaz (TTL icinde).
	callsBefore := src.calls
	src.states = []cursorState{{Visible: true, Handle: 1}, {Visible: true, Handle: 3}, {Visible: true, Handle: 1}}
	src.i = 0
	for i := 0; i < 3; i++ {
		tr.Step(now.Add(5*time.Second + time.Duration(i)*time.Millisecond))
	}
	if src.calls != callsBefore {
		t.Fatalf("onbellekli tutac yeniden cozulmemeli: %d -> %d", callsBefore, src.calls)
	}
}

// Ters cevirme (I-beam) imleci koyu zeminde de gorunmeli: beyaz dolgu + siyah kenar.
func TestCursorFromBitmapsInvertOutlined(t *testing.T) {
	// 3x1 tek renkli: [seffaf, ters, seffaf]. AND=1 her yerde; XOR=1 yalniz ortada.
	w, h := 3, 1
	px := func(on bool) []byte {
		if on {
			return []byte{255, 255, 255, 0}
		}
		return []byte{0, 0, 0, 0}
	}
	var mask []byte
	for _, on := range []bool{true, true, true} { // AND satiri
		mask = append(mask, px(on)...)
	}
	for _, on := range []bool{false, true, false} { // XOR satiri
		mask = append(mask, px(on)...)
	}
	s := cursorFromBitmaps(w, h, nil, mask)
	if s == nil {
		t.Fatal("nil")
	}
	mid := s.RGBA[4:8]
	if mid[0] != 255 || mid[3] != 255 {
		t.Fatalf("ters cevirme pikseli beyaz opak olmali: %v", mid)
	}
	for _, i := range []int{0, 8} {
		if s.RGBA[i+3] != 255 || s.RGBA[i] != 0 {
			t.Fatalf("komsu piksel siyah kenar olmali: %v", s.RGBA[i:i+4])
		}
	}
}
