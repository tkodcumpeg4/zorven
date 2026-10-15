package tunnel

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/tkodcumpeg4/zorven/shared/protocol"
)

// ErrScreen2Unsupported, istemci screen2 yetenegini bildirmediginde doner;
// cagiran eski moda (mjpeg/h264) dusmelidir.
var ErrScreen2Unsupported = errors.New("istemci screen2 modunu desteklemiyor")

// screen2KeyframeRetry, kare dusurulduktan sonra istemciden anahtar kare
// istemenin en kisa tekrar araligi.
const screen2KeyframeRetry = time.Second

// screen2Sub, tek bir screen2 akisinin abonesi.
type screen2Sub struct {
	sessionID string
	ch        chan<- []byte

	mu      sync.Mutex
	resync  bool      // kare dusuruldu: yeni anahtar kare gelene kadar her sey atilir
	lastReq time.Time // son anahtar kare istegi
}

// Screen2Options, OpenScreen2 parametreleri.
type Screen2Options struct {
	FPS      int
	Quality  int
	MaxWidth int
	Display  int
	// Layers dolu ve istemci FeatureScreen2Layers pazarladiysa oturum katmanli acilir.
	Layers []protocol.Screen2LayerSpec
}

// SupportsScreen2Layers, istemci katmanli screen2'yi pazarlamis mi.
func (s *Session) SupportsScreen2Layers() bool { return s.screen2 && s.screen2Layers }

// Screen2Layers, istemcinin etkin katman kumesini degistirir (katmanli oturum).
// key=true: yeniden etkinlesen katman icin tam kare iste.
func (s *Session) Screen2Layers(ctx context.Context, sessionID string,
	layers []protocol.Screen2LayerSpec, key bool) error {
	if !s.SupportsScreen2Layers() {
		return ErrScreen2Unsupported
	}
	if layers == nil {
		layers = []protocol.Screen2LayerSpec{}
	}
	return s.Send(ctx, protocol.Screen2Layers{
		Type: protocol.TypeScreen2Layers, SessionID: sessionID, Layers: layers, Key: key,
	}, protocol.TypeScreen2Layers)
}

// layersForOpen, katmanli mod pazarlanmadiysa katman listesini yok sayar.
func (o Screen2Options) layersForOpen(ok bool) []protocol.Screen2LayerSpec {
	if !ok {
		return nil
	}
	return o.Layers
}

// SupportsScreen2, istemci screen2'yi pazarlamis mi (el sikismadan sonra sabit).
func (s *Session) SupportsScreen2() bool { return s.screen2 }

// OpenScreen2, istemcide screen2 modunda ekran yakalamayi baslatir.
//
// frames kanalina istemcinin gonderdigi HAM ikili mesajlar (14 bayt BodyFrame
// basligi + screen2 yuku, bkz. docs/screen2-protocol.md) AYNEN ve sirayla
// iletilir; panel WS'e oldugu gibi binary yazilabilir. Mesajlar dogrulanmistir
// (protocol.DecodeScreen2). Kanal dolarsa mesaj dusurulur ve akis yeni bir
// anahtar kareye kadar susturulur (sunucu otomatik anahtar kare ister), yani
// tuketici hic bosluklu fark gormez. errs: ScreenError (yakalama hatasi).
// Cagiran isi bitince CloseScreen cagirmalidir. Donen streamID bilgi icindir.
func (s *Session) OpenScreen2(ctx context.Context, sessionID string, o Screen2Options,
	frames chan<- []byte, errs chan<- protocol.ScreenError) (streamID uint64, err error) {
	if !s.screen2 {
		return 0, ErrScreen2Unsupported
	}
	streamID = s.nextReqID.Add(1)

	s.scrMu.Lock()
	s.scr2[streamID] = &screen2Sub{sessionID: sessionID, ch: frames}
	s.scr2Ids[sessionID] = streamID
	s.scrErr[sessionID] = errs
	s.scrMu.Unlock()

	err = s.Send(ctx, protocol.ScreenOpen{
		Type:      protocol.TypeScreenOpen,
		SessionID: sessionID,
		FPS:       o.FPS,
		Quality:   o.Quality,
		MaxWidth:  o.MaxWidth,
		Display:   o.Display,
		Mode:      protocol.ScreenModeScreen2,
		StreamID:  streamID,
		Layers:    o.layersForOpen(s.SupportsScreen2Layers()),
	}, protocol.TypeScreenOpen)
	if err != nil {
		s.scrMu.Lock()
		delete(s.scr2, streamID)
		delete(s.scr2Ids, sessionID)
		delete(s.scrErr, sessionID)
		s.scrMu.Unlock()
	}
	return streamID, err
}

// Screen2Keyframe, istemciden tam kare ister (panel yeniden boyutlandi/kare kaybi).
func (s *Session) Screen2Keyframe(ctx context.Context, sessionID string) error {
	if !s.screen2 {
		return ErrScreen2Unsupported
	}
	return s.Send(ctx, protocol.Screen2Keyframe{
		Type: protocol.TypeScreen2Keyframe, SessionID: sessionID,
	}, protocol.TypeScreen2Keyframe)
}

// Screen2Settings, acik oturumun kalite/FPS/olcek/monitor ayarini canli degistirir.
// Sifir alanlar degistirilmez; Display -1 => monitor degistirme. Monitor veya
// cozunurluk degisince istemci kendiliginden anahtar kare gonderir.
func (s *Session) Screen2Settings(ctx context.Context, sessionID string, o Screen2Options) error {
	if !s.screen2 {
		return ErrScreen2Unsupported
	}
	return s.Send(ctx, protocol.Screen2Settings{
		Type:      protocol.TypeScreen2Settings,
		SessionID: sessionID,
		FPS:       o.FPS,
		Quality:   o.Quality,
		MaxWidth:  o.MaxWidth,
		Display:   o.Display,
	}, protocol.TypeScreen2Settings)
}

// routeScreen2, istemciden gelen bir FrameScreen mesajini aboneye iletir.
// ASLA hata dondurmez/panic etmez: bozuk, bilinmeyen veya asiri buyuk mesaj
// yalnizca atilir (okuma dongusunu ve diger akislari etkilemez).
func (s *Session) routeScreen2(data []byte) {
	if !s.screen2 {
		s.log.Debug("screen2 pazarlanmadi; kare atildi")
		return
	}
	m, err := protocol.DecodeScreen2(data)
	if err != nil {
		s.log.Debug("bozuk screen2 mesaji atildi", "hata", err)
		return
	}
	s.scrMu.Lock()
	sub := s.scr2[m.StreamID]
	s.scrMu.Unlock()
	if sub == nil {
		return // kapanmis/bilinmeyen akis
	}

	sub.mu.Lock()
	if sub.resync {
		if !(m.Keyframe && m.First) {
			sub.mu.Unlock()
			s.askKeyframe(sub) // kayip anahtar kare istegi tekrarlanir
			return
		}
		sub.resync = false
	}
	sub.mu.Unlock()

	// Tuketici kendi tamponunu tutabilir: okuma tamponuna ait dilimi kopyala.
	cp := append([]byte(nil), data...)
	select {
	case sub.ch <- cp:
	default:
		sub.mu.Lock()
		sub.resync = true
		sub.mu.Unlock()
		s.askKeyframe(sub)
	}
}

// askKeyframe, hizlandirilmis (en cok saniyede bir) anahtar kare ister; okuma
// dongusunu bloklamamak icin ayri goroutine'de.
func (s *Session) askKeyframe(sub *screen2Sub) {
	sub.mu.Lock()
	ask := time.Since(sub.lastReq) >= screen2KeyframeRetry
	if ask {
		sub.lastReq = time.Now()
	}
	sub.mu.Unlock()
	if !ask {
		return
	}
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = s.Screen2Keyframe(ctx, sub.sessionID)
	}()
}
