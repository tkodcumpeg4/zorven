package agent

import (
	"context"
	"errors"
	"runtime"

	"github.com/tkodcumpeg4/zorven/shared/protocol"
)

var errClipboardTooLarge = errors.New("pano metni gecersiz veya 64 KiB'i asiyor")

// helloFeatures, Hello'da ilan edilen yetenekler. Imlec ve pano yalniz platform
// destekliyorsa; pano ayrica --no-clipboard ile ilan edilmez.
func (a *Agent) helloFeatures() []string {
	f := []string{protocol.FeatureFlowControl, protocol.FeatureScreenInputStatus, protocol.FeatureScreen2, protocol.FeatureScreen2Layers}
	if cursorSupported() {
		f = append(f, protocol.FeatureScreenCursor)
	}
	if clipboardSupported() && !a.NoClipboard {
		f = append(f, protocol.FeatureScreenClipboard)
	}
	if !a.NoFileTransfer {
		f = append(f, protocol.FeatureScreenFiles)
	}
	f = append(f, protocol.FeatureUDPProbe)
	if !a.NoNet {
		f = append(f, protocol.FeatureWGNet, protocol.FeatureWGRelay)
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

// clipboardWatch, pano izlemeyi acar/kapatir (yalniz kontrolcu izleyici varken).
func (sm *screenManager) clipboardWatch(m protocol.ScreenClipboardWatch) {
	if w := sm.clipboardOf(m.SessionID); w != nil {
		w.SetEnabled(m.Enable)
	}
}
