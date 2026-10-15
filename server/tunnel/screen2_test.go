package tunnel

import (
	"context"
	"errors"
	"log/slog"
	"testing"
	"time"

	"github.com/tkodcumpeg4/zorven/shared/protocol"
)

func newScr2TestSession(t *testing.T, enabled bool) *Session {
	t.Helper()
	s := NewSession("s1", "c1", "c", "127.0.0.1:1", nil, slog.Default())
	s.screen2 = enabled
	return s
}

func scr2Msg(t *testing.T, stream uint64, seq uint32, key, first, eof bool) []byte {
	t.Helper()
	b, err := protocol.Screen2Message{
		StreamID: stream, Seq: seq, Keyframe: key, First: first, EOF: eof, W: 128, H: 128,
		Rects: []protocol.Screen2Rect{{X: 0, Y: 0, W: 64, H: 64, Enc: protocol.Screen2EncSolid, Payload: []byte{1, 2, 3, 255}}},
	}.Encode()
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestScreen2Routing(t *testing.T) {
	s := newScr2TestSession(t, true)
	ch := make(chan []byte, 4)
	sub := &screen2Sub{sessionID: "scr_a", ch: ch}
	s.scr2[9] = sub
	s.scr2Ids["scr_a"] = 9

	good := scr2Msg(t, 9, 1, true, true, true)
	s.routeScreen2(good)
	select {
	case got := <-ch:
		if string(got) != string(good) {
			t.Fatal("mesaj degismeden iletilmeli")
		}
	default:
		t.Fatal("mesaj iletilmedi")
	}

	// bilinmeyen akis, bozuk, kesik, asiri buyuk: sessizce atilir
	s.routeScreen2(scr2Msg(t, 77, 1, true, true, true))
	s.routeScreen2(good[:10])
	s.routeScreen2([]byte{protocol.FrameScreen})
	s.routeScreen2(make([]byte, protocol.Screen2MaxMessage+5))
	bad := append([]byte(nil), good...)
	bad[protocol.Screen2HeaderSize+4] = 0xff // rect genisligi ekran disina tasar
	s.routeScreen2(bad)
	if len(ch) != 0 {
		t.Fatalf("gecersiz mesaj iletildi: %d", len(ch))
	}

	// CloseScreen aboneligi temizler (conn nil: Send hata doner, yok sayilir)
	s.scrMu.Lock()
	delete(s.scr2, 9)
	s.scrMu.Unlock()
	s.routeScreen2(good)
	if len(ch) != 0 {
		t.Fatal("kapali akis mesaj aldi")
	}
}

func TestScreen2DisabledDrops(t *testing.T) {
	s := newScr2TestSession(t, false)
	ch := make(chan []byte, 1)
	s.scr2[1] = &screen2Sub{sessionID: "x", ch: ch}
	s.routeScreen2(scr2Msg(t, 1, 1, true, true, true))
	if len(ch) != 0 {
		t.Fatal("pazarlanmamis ozellik kare iletti")
	}
	if _, err := s.OpenScreen2(context.Background(), "x", Screen2Options{}, ch, nil); !errors.Is(err, ErrScreen2Unsupported) {
		t.Fatalf("beklenen ErrScreen2Unsupported, alinan %v", err)
	}
	if err := s.Screen2Keyframe(context.Background(), "x"); !errors.Is(err, ErrScreen2Unsupported) {
		t.Fatal("keyframe desteksizde hata vermeli")
	}
}

func TestScreen2BackpressureResync(t *testing.T) {
	s := newScr2TestSession(t, true)
	ch := make(chan []byte, 1)
	// lastReq simdi: otomatik anahtar kare istegi (conn nil) tetiklenmesin
	sub := &screen2Sub{sessionID: "scr_a", ch: ch, lastReq: time.Now().Add(time.Hour)}
	s.scr2[5] = sub

	s.routeScreen2(scr2Msg(t, 5, 1, true, true, true))  // kanala girer
	s.routeScreen2(scr2Msg(t, 5, 2, false, true, true)) // kanal dolu: dusurulur, resync
	<-ch                                                // tuketici bosaltti
	s.routeScreen2(scr2Msg(t, 5, 3, false, true, true)) // diff: resync suruyor, atilir
	if len(ch) != 0 {
		t.Fatal("resync sirasinda diff iletildi")
	}
	s.routeScreen2(scr2Msg(t, 5, 4, true, false, true)) // anahtar kare ama First degil: atilir
	if len(ch) != 0 {
		t.Fatal("First olmayan parca resync'i bitirmemeli")
	}
	s.routeScreen2(scr2Msg(t, 5, 5, true, true, true)) // temiz anahtar kare
	if len(ch) != 1 {
		t.Fatal("anahtar kare iletilmeliydi")
	}
	m, _ := protocol.DecodeScreen2(<-ch)
	if m.Seq != 5 {
		t.Fatalf("seq=%d", m.Seq)
	}
}
