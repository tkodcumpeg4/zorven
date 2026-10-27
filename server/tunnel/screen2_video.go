package tunnel

import (
	"context"

	"github.com/tkodcumpeg4/zorven/shared/protocol"
)

// SupportsScreen2Video, istemci H.264 video modunu (FrameVideo) pazarlamis mi.
func (s *Session) SupportsScreen2Video() bool { return s.screen2 && s.screen2Video }

// Screen2Video, istemciye etkin video modunu (auto|tiles|video) bildirir.
func (s *Session) Screen2Video(ctx context.Context, sessionID, mode string) error {
	if !s.SupportsScreen2Video() {
		return ErrScreen2Unsupported
	}
	if !protocol.ValidVideoMode(mode) {
		mode = protocol.Screen2VideoTiles
	}
	return s.Send(ctx, protocol.Screen2Video{
		Type: protocol.TypeScreen2Video, SessionID: sessionID, Mode: mode,
	}, protocol.TypeScreen2Video)
}

// routeScreen2Video, istemciden gelen bir FrameVideo mesajini aboneye ASLA
// hata dondurmeden iletir (bozuk/bilinmeyen mesaj atilir). Kanal dolarsa
// mesaj dusurulur ve istemciden anahtar kare (video modunda IDR) istenir;
// hub tarafi bundan sonraki IDR'ye kadar o izleyiciye video vermez.
func (s *Session) routeScreen2Video(data []byte) {
	if !s.screen2 || !s.screen2Video {
		return
	}
	m, err := protocol.DecodeVideo(data)
	if err != nil {
		s.log.Debug("bozuk video mesaji atildi", "hata", err)
		return
	}
	s.scrMu.Lock()
	sub := s.scr2[m.StreamID]
	s.scrMu.Unlock()
	if sub == nil {
		return
	}
	cp := append([]byte(nil), data...)
	select {
	case sub.ch <- cp:
	default:
		s.askKeyframe(sub)
	}
}
