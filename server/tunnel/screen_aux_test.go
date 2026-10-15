package tunnel

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/tkodcumpeg4/zorven/shared/protocol"
)

func mustJSON(t *testing.T, v any) []byte {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestRouteScreenAuxGatingAndValidation(t *testing.T) {
	s := newScr2TestSession(t, true)
	ch := make(chan ScreenAux, 8)
	s.SubscribeScreenAux("scr_a", ch)

	cur := mustJSON(t, protocol.ScreenCursor{Type: protocol.TypeScreenCursor, SessionID: "scr_a", X: 1, Y: 2, Visible: true})
	shape := mustJSON(t, protocol.ScreenCursorShape{Type: protocol.TypeScreenCursorShape, SessionID: "scr_a", W: 1, H: 1, Data: make([]byte, 4)})
	clip := mustJSON(t, protocol.ScreenClipboard{Type: protocol.TypeScreenClipboard, SessionID: "scr_a", Text: "x"})

	// Yetenek pazarlanmadi: hepsi atilir.
	s.routeScreenAux(protocol.TypeScreenCursor, cur)
	s.routeScreenAux(protocol.TypeScreenCursorShape, shape)
	s.routeScreenAux(protocol.TypeScreenClipboard, clip)
	if len(ch) != 0 {
		t.Fatal("pazarlanmamis yetenek mesaji atilmaliydi")
	}

	s.screenCursor, s.screenClipboard = true, true
	s.routeScreenAux(protocol.TypeScreenCursor, cur)
	s.routeScreenAux(protocol.TypeScreenCursorShape, shape)
	s.routeScreenAux(protocol.TypeScreenClipboard, clip)
	if len(ch) != 3 {
		t.Fatalf("3 mesaj beklenirdi, %d", len(ch))
	}

	// Gecersiz: asiri buyuk bicim, asiri buyuk pano, bozuk JSON, bilinmeyen oturum.
	for len(ch) > 0 {
		<-ch
	}
	bigShape := mustJSON(t, protocol.ScreenCursorShape{Type: protocol.TypeScreenCursorShape, SessionID: "scr_a", W: 65, H: 1, Data: make([]byte, 65*4)})
	bigClip := mustJSON(t, protocol.ScreenClipboard{Type: protocol.TypeScreenClipboard, SessionID: "scr_a", Text: strings.Repeat("a", protocol.MaxScreenClipboardSize+1)})
	other := mustJSON(t, protocol.ScreenCursor{Type: protocol.TypeScreenCursor, SessionID: "baska"})
	s.routeScreenAux(protocol.TypeScreenCursorShape, bigShape)
	s.routeScreenAux(protocol.TypeScreenClipboard, bigClip)
	s.routeScreenAux(protocol.TypeScreenCursor, []byte("{bozuk"))
	s.routeScreenAux(protocol.TypeScreenCursor, other)
	if len(ch) != 0 {
		t.Fatalf("gecersiz mesajlar atilmaliydi, %d", len(ch))
	}

	// Dolu kanal okuma dongusunu bloklamaz.
	full := make(chan ScreenAux)
	s.SubscribeScreenAux("scr_b", full)
	s.routeScreenAux(protocol.TypeScreenCursor, mustJSON(t, protocol.ScreenCursor{Type: protocol.TypeScreenCursor, SessionID: "scr_b"}))
}

func TestScreenClipboardUnsupportedAndLimits(t *testing.T) {
	s := newScr2TestSession(t, true)
	ctx := context.Background()
	if err := s.ScreenClipboardSet(ctx, "x", "a"); !errors.Is(err, ErrScreenClipboardUnsupported) {
		t.Fatalf("desteksiz: %v", err)
	}
	if err := s.ScreenClipboardWatch(ctx, "x", true); !errors.Is(err, ErrScreenClipboardUnsupported) {
		t.Fatalf("desteksiz izleme: %v", err)
	}
	s.screenClipboard = true
	if err := s.ScreenClipboardSet(ctx, "x", strings.Repeat("a", protocol.MaxScreenClipboardSize+1)); err == nil {
		t.Fatal("asiri buyuk metin reddedilmeli")
	}
}
