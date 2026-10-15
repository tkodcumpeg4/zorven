package protocol

import (
	"bytes"
	"math/rand"
	"testing"
)

func sampleRects() []Screen2Rect {
	return []Screen2Rect{
		{X: 0, Y: 0, W: 64, H: 64, Enc: Screen2EncJPEG, Payload: []byte{1, 2, 3}},
		{X: 64, Y: 0, W: 100, H: 10, Enc: Screen2EncPNG, Payload: []byte{9}},
		{X: 10, Y: 20, W: 5, H: 5, Enc: Screen2EncSolid, Payload: []byte{255, 0, 0, 255}},
	}
}

func TestScreen2RoundTrip(t *testing.T) {
	in := Screen2Message{StreamID: 1<<40 + 7, Seq: 99, EOF: true, Keyframe: true, First: true, W: 1920, H: 1080, Rects: sampleRects()}
	b, err := in.Encode()
	if err != nil {
		t.Fatal(err)
	}
	// BodyFrame ile de cozulebilmeli (eski yol tipi tanimali).
	bf, err := DecodeBodyFrame(b)
	if err != nil || bf.FrameType != FrameScreen || bf.ReqID != in.StreamID || !bf.EOF {
		t.Fatalf("bodyframe: %v %+v", err, bf)
	}
	out, err := DecodeScreen2(b)
	if err != nil {
		t.Fatal(err)
	}
	if out.StreamID != in.StreamID || out.Seq != 99 || !out.EOF || !out.Keyframe || !out.First || out.W != 1920 || out.H != 1080 || len(out.Rects) != 3 {
		t.Fatalf("baslik uyusmadi: %+v", out)
	}
	for i, r := range out.Rects {
		e := in.Rects[i]
		if r.X != e.X || r.Y != e.Y || r.W != e.W || r.H != e.H || r.Enc != e.Enc || !bytes.Equal(r.Payload, e.Payload) {
			t.Fatalf("rect %d: %+v != %+v", i, r, e)
		}
	}
}

func TestScreen2Bounds(t *testing.T) {
	cases := map[string]Screen2Rect{
		"disarida":     {X: 1900, Y: 0, W: 100, H: 10, Enc: Screen2EncJPEG, Payload: []byte{1}},
		"bos":          {X: 0, Y: 0, W: 0, H: 10, Enc: Screen2EncJPEG, Payload: []byte{1}},
		"solid-yanlis": {X: 0, Y: 0, W: 1, H: 1, Enc: Screen2EncSolid, Payload: []byte{1}},
		"kodlama":      {X: 0, Y: 0, W: 1, H: 1, Enc: 4, Payload: []byte{1}},
		"buyuk-yuk":    {X: 0, Y: 0, W: 1, H: 1, Enc: Screen2EncJPEG, Payload: make([]byte, Screen2MaxRectPayload+1)},
		"bos-yuk":      {X: 0, Y: 0, W: 1, H: 1, Enc: Screen2EncPNG},
	}
	for name, r := range cases {
		if _, err := (Screen2Message{W: 1920, H: 1080, Rects: []Screen2Rect{r}}).Encode(); err == nil {
			t.Errorf("%s: kodlama hata vermeliydi", name)
		}
	}
	if _, err := (Screen2Message{W: 0, H: 10}).Encode(); err == nil {
		t.Error("sifir tuval kabul edildi")
	}
}

func TestScreen2DecodeRejects(t *testing.T) {
	good, _ := Screen2Message{W: 100, H: 100, Rects: sampleRects()[:1]}.Encode()
	// her kesik uzunluk hata vermeli
	for i := 0; i < len(good); i++ {
		if _, err := DecodeScreen2(good[:i]); err == nil {
			t.Fatalf("%d bayt kesik kabul edildi", i)
		}
	}
	bad := append([]byte(nil), good...)
	bad[14] = 2
	if _, err := DecodeScreen2(bad); err == nil {
		t.Error("yanlis surum kabul edildi")
	}
	// sahte dikdortgen sayisi
	bad = append([]byte(nil), good...)
	bad[19], bad[20] = 0xff, 0xff
	if _, err := DecodeScreen2(bad); err == nil {
		t.Error("sahte nrects kabul edildi")
	}
	// yuk uzunlugu mesaji asiyor
	bad = append([]byte(nil), good...)
	bad[Screen2HeaderSize+9] = 0x7f
	if _, err := DecodeScreen2(bad); err == nil {
		t.Error("sahte uzunluk kabul edildi")
	}
	// fazla bayt
	if _, err := DecodeScreen2(append(append([]byte(nil), good...), 0)); err == nil {
		t.Error("artik bayt kabul edildi")
	}
	if _, err := DecodeScreen2(make([]byte, Screen2MaxMessage+1)); err == nil {
		t.Error("asiri buyuk mesaj kabul edildi")
	}
}

// Rastgele bozma: cozucu asla panic etmemeli; kabul ettigi mesaj yeniden kodlanabilmeli.
func TestScreen2FuzzishNoPanic(t *testing.T) {
	rng := rand.New(rand.NewSource(1))
	base, _ := Screen2Message{StreamID: 5, Seq: 1, W: 640, H: 480, Rects: sampleRects()}.Encode()
	for i := 0; i < 20000; i++ {
		b := append([]byte(nil), base...)
		for k := rng.Intn(4) + 1; k > 0; k-- {
			b[rng.Intn(len(b))] = byte(rng.Intn(256))
		}
		if rng.Intn(4) == 0 {
			b = b[:rng.Intn(len(b)+1)]
		}
		m, err := DecodeScreen2(b)
		if err != nil {
			continue
		}
		if _, err := m.Encode(); err != nil {
			t.Fatalf("kabul edilen mesaj yeniden kodlanamadi: %v", err)
		}
	}
}

func FuzzDecodeScreen2(f *testing.F) {
	b, _ := Screen2Message{W: 64, H: 64, Rects: sampleRects()[:1]}.Encode()
	f.Add(b)
	f.Fuzz(func(t *testing.T, d []byte) { _, _ = DecodeScreen2(d) })
}

func TestPackScreen2Splits(t *testing.T) {
	var rects []Screen2Rect
	for i := 0; i < 10; i++ {
		rects = append(rects, Screen2Rect{X: uint16(i * 10), Y: 0, W: 10, H: 10, Enc: Screen2EncJPEG, Payload: make([]byte, 100<<10)})
	}
	msgs, err := PackScreen2(3, 42, true, 1000, 100, rects, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(msgs) < 4 {
		t.Fatalf("1MB beklenenden az mesaj: %d", len(msgs))
	}
	total := 0
	for i, b := range msgs {
		if len(b) > Screen2TargetMessage {
			t.Errorf("mesaj %d cok buyuk: %d", i, len(b))
		}
		m, err := DecodeScreen2(b)
		if err != nil {
			t.Fatal(err)
		}
		if m.First != (i == 0) || m.EOF != (i == len(msgs)-1) || !m.Keyframe || m.Seq != 42 || m.StreamID != 3 {
			t.Errorf("mesaj %d bayrak/baslik: %+v", i, m)
		}
		total += len(m.Rects)
	}
	if total != 10 {
		t.Fatalf("rect kaybi: %d", total)
	}
	// bos kare = tek mesaj, first+eof
	msgs, err = PackScreen2(3, 1, false, 100, 100, nil, 0)
	if err != nil || len(msgs) != 1 {
		t.Fatal(err, len(msgs))
	}
	m, _ := DecodeScreen2(msgs[0])
	if !m.First || !m.EOF {
		t.Error("bos kare bayraklari")
	}
}
