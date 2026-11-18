package tunnel

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"time"

	"github.com/tkodcumpeg4/zorven/shared/protocol"
)

// ErrScreenClipboardUnsupported, istemci panoyu pazarlamadiginda doner.
var ErrScreenClipboardUnsupported = errors.New("istemci ekran panosunu desteklemiyor")

// ScreenAux, istemciden gelen imlec/pano mesajlarindan TAM BIRINI tasir
// (digerleri nil). Mesajlar abone kanalina verilmeden dogrulanir.
type ScreenAux struct {
	Cursor    *protocol.ScreenCursor
	Shape     *protocol.ScreenCursorShape
	Clipboard *protocol.ScreenClipboard
	// Stats, ajanin canli olcum ozeti (FeatureScreen2Stats).
	Stats *protocol.Screen2Stats
	// Info, monitor sayisi + secili monitor (FeatureScreenInfo).
	Info *protocol.ScreenInfo
	// ClipImage, ajan panosundaki gorsel (PNG; FeatureScreenClipboardImage).
	// Tuketici isini bitirince Done'i cagirmalidir (bekleyen gorsel sayaci).
	ClipImage []byte
	Done      func()
}

// Gorsel panosu sinirlari: ayni oturumdan en fazla saniyede bir gorsel kabul
// edilir; hub'in islemedigi en fazla maxClipImgPending gorsel bellekte bekler
// (en kotu durum bellek: 2 x 8 MiB + birlestirici 8 MiB).
const (
	clipImgMinGap     = time.Second
	maxClipImgPending = 2
)

// ErrScreenClipboardImageUnsupported, istemci gorsel panosunu pazarlamadiginda doner.
var ErrScreenClipboardImageUnsupported = errors.New("istemci ekran panosu gorselini desteklemiyor")

// featureListed, ozellik listesinde f var mi.
func featureListed(fs []string, f string) bool {
	for _, x := range fs {
		if x == f {
			return true
		}
	}
	return false
}

// SupportsScreenClipboardImage, istemci gorsel panosunu pazarlamis mi.
func (s *Session) SupportsScreenClipboardImage() bool {
	return s.screenClipboard && s.screenClipboardImage
}

// SupportsScreenInfo, istemci monitor bilgisi (screen_info) gonderiyor mu.
func (s *Session) SupportsScreenInfo() bool { return s.screenInfo }

// SupportsScreenPing, istemci screen_ping'e yanit veriyor mu.
func (s *Session) SupportsScreenPing() bool { return s.screenPing }

// ScreenClipboardImageSet, PNG'yi istemcinin panosuna FrameClipImage parcalariyla
// yollar (panel -> istemci). Gonderimler seridir (parcalar birbirine karismaz).
func (s *Session) ScreenClipboardImageSet(ctx context.Context, sessionID string, png []byte) error {
	if !s.SupportsScreenClipboardImage() {
		return ErrScreenClipboardImageUnsupported
	}
	if !protocol.ClipImageOK(png) {
		return protocol.ErrClipImage
	}
	s.scrMu.Lock()
	streamID, ok := s.scr2Ids[sessionID]
	s.scrMu.Unlock()
	if !ok {
		return ErrScreen2Unsupported
	}
	parts := protocol.SplitClipImage(png)
	s.clipImgSendMu.Lock()
	defer s.clipImgSendMu.Unlock()
	for i, p := range parts {
		if err := s.SendBinary(ctx, protocol.BodyFrame{
			FrameType: protocol.FrameClipImage, ReqID: streamID, Seq: uint32(i), EOF: i == len(parts)-1, Payload: p,
		}); err != nil {
			return err
		}
	}
	return nil
}

// routeClipImage, istemciden gelen gorsel parcasini birlestirir; tamamlaninca
// dogrulayip (PNG imzasi/IHDR/sinirlar) hiz sinirina uyuyorsa aboneye verir.
// Yalniz okuma dongusu goroutine'i cagirir. Pazarlanmamissa ya da bozuksa atilir.
func (s *Session) routeClipImage(f protocol.BodyFrame) {
	if !s.SupportsScreenClipboardImage() {
		return
	}
	s.scrMu.Lock()
	sub := s.scr2[f.ReqID]
	asm := s.clipAsm[f.ReqID]
	if sub != nil && asm == nil {
		if s.clipAsm == nil {
			s.clipAsm = map[uint64]*protocol.ClipImageAssembler{}
		}
		asm = &protocol.ClipImageAssembler{}
		s.clipAsm[f.ReqID] = asm
	}
	s.scrMu.Unlock()
	if sub == nil {
		return // kapanmis/bilinmeyen akis
	}
	png, done, err := asm.Add(f.Seq, f.EOF, f.Payload)
	if err != nil || !done {
		return
	}
	now := time.Now()
	s.scrMu.Lock()
	if s.clipImgLast == nil {
		s.clipImgLast = map[string]time.Time{}
	}
	if last, ok := s.clipImgLast[sub.sessionID]; ok && now.Sub(last) < clipImgMinGap {
		s.scrMu.Unlock()
		return // hiz siniri
	}
	ch := s.scrAux[sub.sessionID]
	if ch == nil || s.clipImgPending.Load() >= maxClipImgPending {
		s.scrMu.Unlock()
		return
	}
	s.clipImgLast[sub.sessionID] = now
	s.scrMu.Unlock()
	s.clipImgPending.Add(1)
	var once sync.Once
	msg := ScreenAux{ClipImage: png, Done: func() { once.Do(func() { s.clipImgPending.Add(-1) }) }}
	select {
	case ch <- msg:
	default:
		msg.Done()
	}
}

// handleScreenPong, screen_pong yanitindan sunucu<->istemci gidis-donusunu olcer.
func (s *Session) handleScreenPong(data []byte) {
	if !s.screenPing {
		return
	}
	var m protocol.ScreenPong
	if json.Unmarshal(data, &m) != nil {
		return
	}
	now := time.Now().UnixNano()
	d := now - m.T
	if d < 0 || d > int64(time.Minute) {
		return // baska bir sunucu/saat degeri ya da cok eski
	}
	s.agentRTT.Store(d)
	s.agentRTTAt.Store(now)
}

// ScreenPing, istemciye gidis-donus olcum istegi yollar (en fazla ~saniyede bir;
// sik cagrilari yok sayar). Yanit AgentRTT'ye yansir.
func (s *Session) ScreenPing(ctx context.Context) error {
	if !s.screenPing {
		return ErrScreen2Unsupported
	}
	now := time.Now().UnixNano()
	last := s.lastPingAt.Load()
	if now-last < int64(900*time.Millisecond) || !s.lastPingAt.CompareAndSwap(last, now) {
		return nil
	}
	return s.Send(ctx, protocol.ScreenPing{Type: protocol.TypeScreenPing, T: now}, protocol.TypeScreenPing)
}

// AgentRTT, son olculen sunucu<->istemci gidis-donusu doner; olcum yoksa ya da
// 15 sn'den eskiyse ok=false.
func (s *Session) AgentRTT() (time.Duration, bool) {
	at := s.agentRTTAt.Load()
	if at == 0 || time.Since(time.Unix(0, at)) > 15*time.Second {
		return 0, false
	}
	return time.Duration(s.agentRTT.Load()), true
}

// SupportsScreenCursor, istemci imlec katmanini pazarlamis mi.
func (s *Session) SupportsScreenCursor() bool { return s.screenCursor }

// SupportsScreenClipboard, istemci pano yetenegini pazarlamis mi.
func (s *Session) SupportsScreenClipboard() bool { return s.screenClipboard }

// SubscribeScreenAux, oturumun imlec/pano mesajlarina abone olur ve istemcinin
// hangi yetenekleri pazarladigini doner. Kanal dolarsa mesaj atilir (okuma
// dongusu asla bloklanmaz). CloseScreen aboneligi temizler.
func (s *Session) SubscribeScreenAux(sessionID string, ch chan<- ScreenAux) (cursor, clipboard bool) {
	s.scrMu.Lock()
	s.scrAux[sessionID] = ch
	s.scrMu.Unlock()
	return s.screenCursor, s.screenClipboard
}

// routeScreenAux, imlec/pano mesajini dogrulayip aboneye iletir. Pazarlanmamis
// yetenek, bozuk veya sinir asan mesaj sessizce atilir.
func (s *Session) routeScreenAux(t protocol.MessageType, data []byte) {
	var (
		sid string
		msg ScreenAux
	)
	switch t {
	case protocol.TypeScreenCursor:
		if !s.screenCursor {
			return
		}
		var m protocol.ScreenCursor
		if json.Unmarshal(data, &m) != nil {
			return
		}
		sid, msg.Cursor = m.SessionID, &m
	case protocol.TypeScreenCursorShape:
		if !s.screenCursor {
			return
		}
		var m protocol.ScreenCursorShape
		if json.Unmarshal(data, &m) != nil || !m.Valid() {
			return
		}
		sid, msg.Shape = m.SessionID, &m
	case protocol.TypeScreen2Stats:
		if !s.screen2Stats {
			return
		}
		var m protocol.Screen2Stats
		if json.Unmarshal(data, &m) != nil || !m.Valid() {
			return
		}
		sid, msg.Stats = m.SessionID, &m
	case protocol.TypeScreenInfo:
		if !s.screenInfo {
			return
		}
		var m protocol.ScreenInfo
		if json.Unmarshal(data, &m) != nil || !m.Valid() {
			return
		}
		sid, msg.Info = m.SessionID, &m
	case protocol.TypeScreenClipboard:
		if !s.screenClipboard {
			return
		}
		var m protocol.ScreenClipboard
		if json.Unmarshal(data, &m) != nil || !protocol.ClipboardTextOK(m.Text) {
			return
		}
		sid, msg.Clipboard = m.SessionID, &m
	default:
		return
	}
	s.scrMu.Lock()
	ch := s.scrAux[sid]
	s.scrMu.Unlock()
	if ch == nil {
		return
	}
	select {
	case ch <- msg:
	default:
	}
}

// ScreenClipboardSet, istemcinin panosunu ayarlatir (panel -> istemci).
func (s *Session) ScreenClipboardSet(ctx context.Context, sessionID, text string) error {
	if !s.screenClipboard {
		return ErrScreenClipboardUnsupported
	}
	if !protocol.ClipboardTextOK(text) {
		return errors.New("pano metni gecersiz veya cok buyuk")
	}
	return s.Send(ctx, protocol.ScreenClipboard{
		Type: protocol.TypeScreenClipboard, SessionID: sessionID, Text: text,
	}, protocol.TypeScreenClipboard)
}

// ScreenClipboardWatch, istemcinin pano izlemesini acar/kapatir.
func (s *Session) ScreenClipboardWatch(ctx context.Context, sessionID string, enable bool) error {
	if !s.screenClipboard {
		return ErrScreenClipboardUnsupported
	}
	return s.Send(ctx, protocol.ScreenClipboardWatch{
		Type: protocol.TypeScreenClipboardWatch, SessionID: sessionID, Enable: enable,
	}, protocol.TypeScreenClipboardWatch)
}
