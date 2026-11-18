package agent

import (
	"context"
	"errors"
	"github.com/tkodcumpeg4/zorven/client/wgnet"
	"runtime"

	"github.com/tkodcumpeg4/zorven/shared/protocol"
)

var errClipboardTooLarge = errors.New("pano metni gecersiz veya 64 KiB'i asiyor")

// helloFeatures, Hello'da ilan edilen yetenekler. Imlec ve pano yalniz platform
// destekliyorsa; pano ayrica --no-clipboard ile ilan edilmez.
func (a *Agent) helloFeatures() []string {
	f := []string{protocol.FeatureFlowControl, protocol.FeatureScreenInputStatus, protocol.FeatureScreen2, protocol.FeatureScreen2Layers, protocol.FeatureScreen2Stats, protocol.FeatureScreen2Feedback, protocol.FeatureScreen2FPS60, protocol.FeatureScreenInfo, protocol.FeatureScreenPing}
	if videoEncoderAvailable() {
		f = append(f, protocol.FeatureScreen2Video)
	}
	if cursorSupported() {
		f = append(f, protocol.FeatureScreenCursor)
	}
	if clipboardSupported() && !a.NoClipboard {
		f = append(f, protocol.FeatureScreenClipboard)
		if clipboardImageSupported() {
			f = append(f, protocol.FeatureScreenClipboardImage)
		}
	}
	if !a.NoFileTransfer {
		f = append(f, protocol.FeatureScreenFiles)
	}
	f = append(f, protocol.FeatureUDPProbe, protocol.FeatureHTTPTrailers)
	if a.apps != nil && a.apps.Available() {
		f = append(f, protocol.FeatureDeploy)
	}
	if !a.NoPosture {
		f = append(f, protocol.FeaturePosture, protocol.FeatureDeviceCert)
	}
	if !a.NoNet {
		f = append(f, protocol.FeatureWGNet, protocol.FeatureWGRelay)
		if !a.NoP2P && !wgnet.DirectDisabledByEnv() {
			f = append(f, protocol.FeatureWGPunch)
		}
		if a.tunSupported() {
			f = append(f, protocol.FeatureWGTun, protocol.FeatureWGALog)
		}
	}
	return f
}

// startAux, screen2 oturumuna imlec izleyici ve pano izleyiciyi baglar.
func (sm *screenManager) startAux(ctx context.Context, ss *screenSession, s *screen2Session) {
	if sm.cs.screenCursor && cursorSupported() {
		go func() {
			// Windows'ta imlec bilgisi girdi masaustune bagli is parcacigindan alinir.
			runtime.LockOSThread()
			defer runtime.UnlockOSThread()
			attachToInputDesktop()
			src := newCursorSource()
			if src == nil {
				return // X11/Wayland yok vb.: imlec katmani sessizce kapali
			}
			t := newCursorTracker(src, ss.getBounds, s.canvasSize,
				func(c protocol.ScreenCursor) {
					c.SessionID = ss.id
					_ = sm.cs.sendControl(ctx, c, protocol.TypeScreenCursor)
				},
				func(c protocol.ScreenCursorShape) {
					c.SessionID = ss.id
					_ = sm.cs.sendControl(ctx, c, protocol.TypeScreenCursorShape)
				})
			t.Run(ctx)
		}()
	}
	if sm.cs.screenClipboard && clipboardSupported() {
		if be := newClipboardBackend(); be != nil {
			w := newClipboardWatcher(be, func(text string) {
				_ = sm.cs.sendControl(ctx, protocol.ScreenClipboard{Type: protocol.TypeScreenClipboard, SessionID: ss.id, Text: text}, protocol.TypeScreenClipboard)
			})
			if sm.cs.screenInputStatus {
				w.notice = func(detail string) {
					sm.sendInputStatus(ctx, ss.id, inputResult{State: protocol.InputStateDegraded, Detail: detail})
				}
			}
			if sm.cs.screenClipboardImage {
				w.enableImages(func(png []byte) { sm.sendClipImage(ctx, ss, png) })
			}
			ss.boundsMu.Lock()
			ss.clip = w
			ss.boundsMu.Unlock()
			go w.Run(ctx)
		}
	}
}

func (sm *screenManager) clipboardOf(id string) *clipboardWatcher {
	sm.mu.Lock()
	ss := sm.sessions[id]
	sm.mu.Unlock()
	if ss == nil {
		return nil
	}
	ss.boundsMu.RLock()
	defer ss.boundsMu.RUnlock()
	return ss.clip
}

// clipboardSet, panelden gelen metni bu makinenin panosuna yazar.
func (sm *screenManager) clipboardSet(m protocol.ScreenClipboard) {
	w := sm.clipboardOf(m.SessionID)
	if w == nil {
		return
	}
	if err := w.Apply(m.Text); err != nil {
		sm.cs.log.Warn("pano yazilamadi", "session", m.SessionID, "hata", err)
	}
}

// sendClipImage, uzak makinenin panosundaki gorseli (PNG) FrameClipImage
// parcalariyla sunucuya yollar. Tek goroutine'den (izleyici) cagrilir.
func (sm *screenManager) sendClipImage(ctx context.Context, ss *screenSession, png []byte) {
	if !protocol.ClipImageOK(png) {
		return
	}
	parts := protocol.SplitClipImage(png)
	for i, p := range parts {
		fr := protocol.BodyFrame{FrameType: protocol.FrameClipImage, ReqID: ss.streamID, Seq: uint32(i), EOF: i == len(parts)-1, Payload: p}
		if sm.cs.sendBinary(ctx, fr.Encode()) != nil {
			return
		}
	}
}

// clipImageChunk, sunucudan gelen gorsel parcasini (FrameClipImage) birlestirir;
// tamamlaninca panoya yazar. Yalniz okuma dongusu goroutine'i cagirir.
func (sm *screenManager) clipImageChunk(f protocol.BodyFrame) {
	sm.mu.Lock()
	var ss *screenSession
	for _, s := range sm.sessions {
		if s.streamID == f.ReqID && f.ReqID != 0 {
			ss = s
			break
		}
	}
	sm.mu.Unlock()
	if ss == nil {
		return
	}
	png, done, err := ss.clipAsm.Add(f.Seq, f.EOF, f.Payload)
	if err != nil {
		sm.cs.log.Warn("pano gorseli parcasi atildi", "session", ss.id, "hata", err)
		return
	}
	if !done {
		return
	}
	w := sm.clipboardOf(ss.id)
	if w == nil || !w.imagesEnabled() {
		return
	}
	go func() {
		if err := w.ApplyImage(png); err != nil {
			sm.cs.log.Warn("pano gorseli yazilamadi", "session", ss.id, "hata", err)
		}
	}()
}

// sendScreenInfo, monitor sayisini ve secili monitoru sunucuya bildirir
// (yalniz FeatureScreenInfo pazarlandiysa; eski sunucu bu mesaji hic gormez).
func (sm *screenManager) sendScreenInfo(ctx context.Context, sessionID string, display, displays int) {
	if !sm.cs.screenInfo {
		return
	}
	if displays < 1 {
		displays = 1
	}
	if display < 0 || display >= displays {
		display = 0
	}
	_ = sm.cs.sendControl(ctx, protocol.ScreenInfo{Type: protocol.TypeScreenInfo, SessionID: sessionID, Displays: displays, Display: display}, protocol.TypeScreenInfo)
}

// clipboardWatch, pano izlemeyi acar/kapatir (yalniz kontrolcu izleyici varken).
func (sm *screenManager) clipboardWatch(m protocol.ScreenClipboardWatch) {
	if w := sm.clipboardOf(m.SessionID); w != nil {
		w.SetEnabled(m.Enable)
	}
}
