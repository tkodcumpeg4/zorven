package protocol

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestScreenCursorRoundTrip(t *testing.T) {
	in := ScreenCursor{Type: TypeScreenCursor, SessionID: "scr_1", X: 10, Y: 20, Visible: true, W: 1600, H: 900}
	b, err := Marshal(in, TypeScreenCursor)
	if err != nil {
		t.Fatal(err)
	}
	var out ScreenCursor
	if err := json.Unmarshal(b, &out); err != nil || out != in {
		t.Fatalf("gidis donus: %+v %v", out, err)
	}
}

func TestScreenCursorShapeRoundTripAndValid(t *testing.T) {
	in := ScreenCursorShape{Type: TypeScreenCursorShape, SessionID: "s", W: 2, H: 2, HotX: 1, HotY: 0, Data: make([]byte, 16)}
	b, err := Marshal(in, TypeScreenCursorShape)
	if err != nil {
		t.Fatal(err)
	}
	var out ScreenCursorShape
	if err := json.Unmarshal(b, &out); err != nil {
		t.Fatal(err)
	}
	if !out.Valid() || out.HotX != 1 || len(out.Data) != 16 {
		t.Fatalf("bozuk: %+v", out)
	}
	bad := []ScreenCursorShape{
		{W: 0, H: 1, Data: nil},
		{W: 65, H: 1, Data: make([]byte, 65*4)},
		{W: 2, H: 2, Data: make([]byte, 15)},
		{W: 2, H: 2, HotX: 2, Data: make([]byte, 16)},
		{W: 2, H: 2, HotY: -1, Data: make([]byte, 16)},
	}
	for i, s := range bad {
		if s.Valid() {
			t.Errorf("gecersiz bicim %d kabul edildi", i)
		}
	}
	if !(ScreenCursorShape{W: 64, H: 64, Data: make([]byte, 64*64*4)}).Valid() {
		t.Error("64x64 gecerli olmali")
	}
}

func TestScreenClipboardRoundTripAndLimits(t *testing.T) {
	in := ScreenClipboard{Type: TypeScreenClipboard, SessionID: "s", Text: "merhaba dunya ğüşiöç"}
	b, err := Marshal(in, TypeScreenClipboard)
	if err != nil {
		t.Fatal(err)
	}
	var out ScreenClipboard
	if err := json.Unmarshal(b, &out); err != nil || out != in {
		t.Fatalf("gidis donus: %+v %v", out, err)
	}
	if !ClipboardTextOK(strings.Repeat("a", MaxScreenClipboardSize)) {
		t.Error("tam sinir kabul edilmeli")
	}
	if ClipboardTextOK(strings.Repeat("a", MaxScreenClipboardSize+1)) {
		t.Error("sinir asimi reddedilmeli")
	}
	if ClipboardTextOK("bozuk\xff") {
		t.Error("gecersiz UTF-8 reddedilmeli")
	}
	w, err := Marshal(ScreenClipboardWatch{Type: TypeScreenClipboardWatch, SessionID: "s", Enable: true}, TypeScreenClipboardWatch)
	if err != nil || !strings.Contains(string(w), `"enable":true`) {
		t.Fatalf("watch: %s %v", w, err)
	}
}
