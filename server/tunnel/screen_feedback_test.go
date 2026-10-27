package tunnel

import (
	"context"
	"testing"

	"github.com/tkodcumpeg4/zorven/shared/protocol"
)

func TestScreen2FeedbackSendGating(t *testing.T) {
	s := newScr2TestSession(t, true)
	var sent []protocol.MessageType
	s.sendHook = func(_ context.Context, v any, typ protocol.MessageType) error {
		sent = append(sent, typ)
		if fb, ok := v.(protocol.Screen2Feedback); ok && (!fb.Valid() || fb.Layers == nil) {
			t.Errorf("gecersiz/nil katmanli mesaj: %+v", fb)
		}
		return nil
	}
	ls := []protocol.Screen2FeedbackLayer{{ID: 1, FPS: 8, Skipped: 3, QueueMs: 200, Viewers: 1}}

	// Pazarlanmadi: gonderilmez, hata doner.
	if s.SupportsScreen2Feedback() {
		t.Fatal("pazarlanmamis ozellik acik")
	}
	if err := s.Screen2Feedback(context.Background(), "scr_1", 500, ls); err == nil || len(sent) != 0 {
		t.Fatalf("pazarlanmadan gonderildi: err=%v sent=%v", err, sent)
	}
	// screen2 kapaliysa geri bildirim bayragi tek basina yetmez.
	s.screen2Feedback = true
	s.screen2 = false
	if s.SupportsScreen2Feedback() {
		t.Fatal("screen2 olmadan acik")
	}
	s.screen2 = true
	if !s.SupportsScreen2Feedback() {
		t.Fatal("pazarlandi ama kapali")
	}
	if err := s.Screen2Feedback(context.Background(), "scr_1", 500, nil); err != nil {
		t.Fatal(err)
	}
	if err := s.Screen2Feedback(context.Background(), "scr_1", 500, ls); err != nil {
		t.Fatal(err)
	}
	if len(sent) != 2 || sent[0] != protocol.TypeScreen2Feedback {
		t.Fatalf("sent=%v", sent)
	}
}
