package tunnel

import (
	"context"
	"encoding/json"
	"errors"

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
