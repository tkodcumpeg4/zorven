package tunnel

import (
	"bytes"
	"context"
	"encoding/binary"
	"testing"
	"time"

	"github.com/tkodcumpeg4/zorven/shared/protocol"
)

// testPNG: gecerli IHDR'li (tam cozulmesi gerekmeyen) sahte PNG; sunucu yalniz
// imza + IHDR + boyut dogrular.
func testPNG(w, h uint32, extra int) []byte {
	b := []byte{0x89, 'P', 'N', 'G', '\r', '\n', 0x1a, '\n', 0, 0, 0, 13}
	b = append(b, "IHDR"...)
	b = binary.BigEndian.AppendUint32(b, w)
	b = binary.BigEndian.AppendUint32(b, h)
	b = append(b, 8, 6, 0, 0, 0, 0, 0, 0, 0)
	return append(b, make([]byte, extra)...)
}

func TestHandshakeNewScreenFlagsNegotiation(t *testing.T) {
	all := []string{protocol.FeatureScreen2, protocol.FeatureScreenClipboard, protocol.FeatureScreenClipboardImage,
		protocol.FeatureScreenInfo, protocol.FeatureScreenPing}
	s, _ := runHello(t, protocol.Hello{Type: protocol.TypeHello, ClientVersion: "0.3.10", Platform: "windows/amd64", Features: all})
	if !s.SupportsScreenClipboardImage() || !s.SupportsScreenInfo() || !s.SupportsScreenPing() {
		t.Fatalf("ilan edilen yetenekler etkinlesmedi: %v", s.Features())
	}
	for _, f := range []string{protocol.FeatureScreenClipboardImage, protocol.FeatureScreenInfo, protocol.FeatureScreenPing} {
		if !hasFeature(s.Features(), f) {
			t.Errorf("%s hello_ack'e yansimadi", f)
		}
	}

	// ESKI ajan (0.3.9): hicbiri ilan edilmez -> hicbiri etkin degil.
	old, _ := runHello(t, protocol.Hello{Type: protocol.TypeHello, ClientVersion: "0.3.9", Platform: "windows/amd64",
		Features: []string{protocol.FeatureScreen2, protocol.FeatureScreenClipboard}})
	if old.SupportsScreenClipboardImage() || old.SupportsScreenInfo() || old.SupportsScreenPing() ||
		hasFeature(old.Features(), protocol.FeatureScreenClipboardImage) {
		t.Fatalf("eski ajanda yeni yetenek acik: %v", old.Features())
	}
	if !old.SupportsScreenClipboard() {
		t.Fatal("metin panosu eski ajanda calismaya devam etmeli")
	}

	// Gorsel, metin panosu olmadan anlasilmaz.
	lone, _ := runHello(t, protocol.Hello{Type: protocol.TypeHello, ClientVersion: "0.3.10", Platform: "windows/amd64",
		Features: []string{protocol.FeatureScreen2, protocol.FeatureScreenClipboardImage}})
	if lone.SupportsScreenClipboardImage() || hasFeature(lone.Features(), protocol.FeatureScreenClipboardImage) {
		t.Fatal("pano bayragi olmadan gorsel acik")
	}
}

func TestRouteScreenInfo(t *testing.T) {
	s := newScr2TestSession(t, true)
	ch := make(chan ScreenAux, 4)
	s.SubscribeScreenAux("scr_a", ch)
	ok := mustJSON(t, protocol.ScreenInfo{Type: protocol.TypeScreenInfo, SessionID: "scr_a", Displays: 3, Display: 2})
	s.routeScreenAux(protocol.TypeScreenInfo, ok)
	if len(ch) != 0 {
		t.Fatal("pazarlanmadan screen_info atilmali")
	}
	s.screenInfo = true
	s.routeScreenAux(protocol.TypeScreenInfo, ok)
	a := <-ch
	if a.Info == nil || a.Info.Displays != 3 || a.Info.Display != 2 {
		t.Fatalf("%+v", a.Info)
	}
	for _, bad := range []protocol.ScreenInfo{
		{SessionID: "scr_a", Displays: 0, Display: 0},
		{SessionID: "scr_a", Displays: 2, Display: 2},
		{SessionID: "scr_a", Displays: 99, Display: 0},
		{SessionID: "baska", Displays: 2, Display: 0},
	} {
		bad.Type = protocol.TypeScreenInfo
		s.routeScreenAux(protocol.TypeScreenInfo, mustJSON(t, bad))
	}
	s.routeScreenAux(protocol.TypeScreenInfo, []byte("{bozuk"))
	if len(ch) != 0 {
		t.Fatalf("gecersiz screen_info atilmali: %d", len(ch))
	}
}

func imgSession(t *testing.T) (*Session, chan ScreenAux) {
	t.Helper()
	s := newScr2TestSession(t, true)
	s.screenClipboard, s.screenClipboardImage = true, true
	ch := make(chan ScreenAux, 4)
	s.SubscribeScreenAux("scr_a", ch)
	s.scr2[9] = &screen2Sub{sessionID: "scr_a", ch: make(chan []byte, 1)}
	s.scr2Ids["scr_a"] = 9
	return s, ch
}

func feedImage(s *Session, stream uint64, png []byte) {
	parts := protocol.SplitClipImage(png)
	for i, p := range parts {
		s.routeClipImage(protocol.BodyFrame{FrameType: protocol.FrameClipImage, ReqID: stream, Seq: uint32(i), EOF: i == len(parts)-1, Payload: p})
	}
}

func TestRouteClipImageDeliversAndRateLimits(t *testing.T) {
	s, ch := imgSession(t)
	png := testPNG(10, 10, protocol.ClipImageChunk+500) // 2 parca
	feedImage(s, 9, png)
	a := <-ch
	if !bytes.Equal(a.ClipImage, png) || a.Done == nil {
		t.Fatalf("gorsel iletilmedi (%d bayt)", len(a.ClipImage))
	}
	if s.clipImgPending.Load() != 1 {
		t.Fatalf("bekleyen sayaci: %d", s.clipImgPending.Load())
	}
	a.Done()
	a.Done() // idempotent
	if s.clipImgPending.Load() != 0 {
		t.Fatalf("Done sayaci dusurmeli: %d", s.clipImgPending.Load())
	}
	// Hemen ardindan ikinci gorsel: hiz siniri.
	feedImage(s, 9, png)
	if len(ch) != 0 {
		t.Fatal("saniyede birden sik gorsel atilmali")
	}
	// Sure gectikten sonra yine kabul.
	s.scrMu.Lock()
	s.clipImgLast["scr_a"] = time.Now().Add(-2 * clipImgMinGap)
	s.scrMu.Unlock()
	feedImage(s, 9, png)
	if len(ch) != 1 {
		t.Fatal("sure dolunca gorsel kabul edilmeli")
	}
}

func TestRouteClipImagePendingCap(t *testing.T) {
	s, ch := imgSession(t)
	png := testPNG(4, 4, 10)
	for i := 0; i < maxClipImgPending+2; i++ {
		s.scrMu.Lock()
		s.clipImgLast["scr_a"] = time.Time{}
		s.scrMu.Unlock()
		feedImage(s, 9, png)
	}
	if len(ch) != maxClipImgPending || s.clipImgPending.Load() != maxClipImgPending {
		t.Fatalf("bekleyen sinirda kalmali: kanal=%d sayac=%d", len(ch), s.clipImgPending.Load())
	}
	// Dolu kanal (tuketici yok): sayac sizmaz.
	full := make(chan ScreenAux)
	s.SubscribeScreenAux("scr_a", full)
	s.clipImgPending.Store(0)
	s.scrMu.Lock()
	s.clipImgLast["scr_a"] = time.Time{}
	s.scrMu.Unlock()
	feedImage(s, 9, png)
	if s.clipImgPending.Load() != 0 {
		t.Fatalf("teslim edilemeyen gorsel sayaci sizdirmamali: %d", s.clipImgPending.Load())
	}
}

func TestRouteClipImageRejects(t *testing.T) {
	s, ch := imgSession(t)
	good := testPNG(4, 4, 10)

	feedImage(s, 77, good) // bilinmeyen akis
	feedImage(s, 9, []byte("png degil png degil png degil png degil png degil"))
	feedImage(s, 9, testPNG(protocol.MaxClipImageDim+1, 2, 0))
	feedImage(s, 9, testPNG(8000, 8000, 10)) // piksel siniri
	// Sira atlama.
	s.routeClipImage(protocol.BodyFrame{FrameType: protocol.FrameClipImage, ReqID: 9, Seq: 0, Payload: good[:20]})
	s.routeClipImage(protocol.BodyFrame{FrameType: protocol.FrameClipImage, ReqID: 9, Seq: 5, EOF: true, Payload: good[20:]})
	// Toplam sinir: parcalar 8 MiB'i asinca atilir, bellek buyumez.
	big := make([]byte, protocol.ClipImageChunk)
	for i := 0; i <= protocol.MaxScreenClipboardImage/protocol.ClipImageChunk+2; i++ {
		s.routeClipImage(protocol.BodyFrame{FrameType: protocol.FrameClipImage, ReqID: 9, Seq: uint32(i), Payload: big})
	}
	if len(ch) != 0 {
		t.Fatalf("gecersiz gorseller atilmali: %d", len(ch))
	}

	// Pazarlanmadi (eski ajan): hic islenmez.
	s2, ch2 := imgSession(t)
	s2.screenClipboardImage = false
	feedImage(s2, 9, good)
	if len(ch2) != 0 {
		t.Fatal("pazarlanmadan gorsel atilmali")
	}
	// Gorsel bayragi var ama metin panosu yok: kapali.
	s3, ch3 := imgSession(t)
	s3.screenClipboard = false
	feedImage(s3, 9, good)
	if len(ch3) != 0 {
		t.Fatal("metin panosu olmadan gorsel atilmali")
	}
}

func TestScreenClipboardImageSet(t *testing.T) {
	s, _ := imgSession(t)
	var frames []protocol.BodyFrame
	s.sendBinHook = func(_ context.Context, f protocol.BodyFrame) error {
		f.Payload = append([]byte(nil), f.Payload...)
		frames = append(frames, f)
		return nil
	}
	ctx := context.Background()
	png := testPNG(6, 6, protocol.ClipImageChunk*2)
	if err := s.ScreenClipboardImageSet(ctx, "scr_a", png); err != nil {
		t.Fatal(err)
	}
	if len(frames) != 3 {
		t.Fatalf("parca=%d", len(frames))
	}
	var asm protocol.ClipImageAssembler
	var got []byte
	for i, f := range frames {
		if f.FrameType != protocol.FrameClipImage || f.ReqID != 9 || f.Seq != uint32(i) || f.EOF != (i == 2) {
			t.Fatalf("cerceve %d: %+v", i, f)
		}
		if out, done, err := asm.Add(f.Seq, f.EOF, f.Payload); err != nil {
			t.Fatal(err)
		} else if done {
			got = out
		}
	}
	if !bytes.Equal(got, png) {
		t.Fatal("birlesen gorsel farkli")
	}
	if err := s.ScreenClipboardImageSet(ctx, "scr_a", []byte("png degil")); err == nil {
		t.Fatal("gecersiz gorsel gonderilmemeli")
	}
	if err := s.ScreenClipboardImageSet(ctx, "yok", png); err == nil {
		t.Fatal("bilinmeyen oturuma gonderilmemeli")
	}
	n := len(frames)
	s.screenClipboardImage = false // eski ajan
	if err := s.ScreenClipboardImageSet(ctx, "scr_a", png); err != ErrScreenClipboardImageUnsupported || len(frames) != n {
		t.Fatalf("eski ajana gorsel gitmemeli: %v", err)
	}
}

func TestAgentRTTMeasurement(t *testing.T) {
	s := newScr2TestSession(t, true)
	if _, ok := s.AgentRTT(); ok {
		t.Fatal("olcum yokken ok=false")
	}
	var sent []protocol.ScreenPing
	s.sendHook = func(_ context.Context, v any, _ protocol.MessageType) error {
		if p, ok := v.(protocol.ScreenPing); ok {
			sent = append(sent, p)
		}
		return nil
	}
	ctx := context.Background()
	if err := s.ScreenPing(ctx); err != ErrScreen2Unsupported || len(sent) != 0 {
		t.Fatalf("pazarlanmadan ping gitmemeli: %v", err)
	}
	s.screenPing = true
	if err := s.ScreenPing(ctx); err != nil || len(sent) != 1 {
		t.Fatalf("ping: %v %d", err, len(sent))
	}
	_ = s.ScreenPing(ctx) // hiz siniri
	if len(sent) != 1 {
		t.Fatalf("hiz siniri: %d", len(sent))
	}
	// Yanit: gidis-donus olculur.
	s.handleScreenPong(mustJSON(t, protocol.ScreenPong{Type: protocol.TypeScreenPong, T: sent[0].T}))
	d, ok := s.AgentRTT()
	if !ok || d < 0 || d > 5*time.Second {
		t.Fatalf("rtt=%v ok=%v", d, ok)
	}
	// Gelecek/cok eski t degeri yok sayilir (olcum degismez).
	before := s.agentRTT.Load()
	s.handleScreenPong(mustJSON(t, protocol.ScreenPong{Type: protocol.TypeScreenPong, T: time.Now().Add(time.Hour).UnixNano()}))
	s.handleScreenPong(mustJSON(t, protocol.ScreenPong{Type: protocol.TypeScreenPong, T: time.Now().Add(-time.Hour).UnixNano()}))
	s.handleScreenPong([]byte("{bozuk"))
	if s.agentRTT.Load() != before {
		t.Fatal("gecersiz pong olcumu bozmamali")
	}
	// Eskiyen olcum bildirilmez.
	s.agentRTTAt.Store(time.Now().Add(-time.Minute).UnixNano())
	if _, ok := s.AgentRTT(); ok {
		t.Fatal("eski olcum bildirilmemeli")
	}
	// Pazarlanmadan gelen pong yok sayilir.
	s2 := newScr2TestSession(t, true)
	s2.handleScreenPong(mustJSON(t, protocol.ScreenPong{Type: protocol.TypeScreenPong, T: time.Now().UnixNano()}))
	if _, ok := s2.AgentRTT(); ok {
		t.Fatal("pazarlanmadan pong sayilmamali")
	}
}
