package protocol

import (
	"encoding/json"
	"testing"
)

func TestScreenInputStatusRoundTrip(t *testing.T) {
	in := ScreenInputStatus{Type: TypeScreenInputStatus, SessionID: "scr_1", State: InputStateBlocked, Code: 5, Detail: "uipi"}
	b, err := json.Marshal(in)
	if err != nil {
		t.Fatal(err)
	}
	var out ScreenInputStatus
	if err := json.Unmarshal(b, &out); err != nil {
		t.Fatal(err)
	}
	if out != in {
		t.Fatalf("got %+v want %+v", out, in)
	}
	if out.Type != "screen_input_status" {
		t.Fatalf("tip: %s", out.Type)
	}
}
