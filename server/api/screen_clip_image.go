package api

import (
	"context"
	"encoding/json"
	"time"

	"github.com/tkodcumpeg4/zorven/shared/protocol"
)

// Pano gorseli (panel <-> ajan) ve gidis-donus olcumu (panel ping/pong).
//
// Panel WS'inde gorsel TEK ikili mesajdir: ilk bayt protocol.PanelClipImageMarker,
// kalani PNG (iki yonde). Ekran kareleri 6/9 ile basladigi icin cakismaz.

// clipReasonRateLimited, gorsel hiz sinirina takildi (clipboard_status "reason").
const clipReasonRateLimited = "rate_limited"

// clipImgGap, bir izleyicinin iki gorsel gonderimi arasindaki en kisa sure.
const clipImgGap = time.Second

// pingMinGap, bir izleyicinin iki ping'i arasindaki en kisa sure (hiz siniri).
const pingMinGap = 500 * time.Millisecond

// putImage, gorsel mesajini kapasite-1 yuvaya koyar (eski bekleyen atilir).
func (v *screen2Viewer) putImage(b []byte) {
	for {
		select {
		case v.img <- b:
			return
		default:
		}
		select {
		case <-v.img:
		default:
		}
	}
}

// isViewOnly, izleyici salt-izleme modunda mi (okuma siniri secimi icin).
func (v *screen2Viewer) isViewOnly() bool {
	v.hub.mu.Lock()
	defer v.hub.mu.Unlock()
	return v.viewOnly
}

func (v *screen2Viewer) clipImageStatus(ok bool, reason string) {
	m := map[string]any{"type": "clipboard_status", "ok": ok, "image": true}
	if reason != "" {
		m["reason"] = reason
	}
	b, _ := json.Marshal(m)
	v.sendMsg(b)
}

// clipboardImage, panelin PNG'sini istemci panosuna iletir. Reddedilirse
// clipboard_status{ok:false,image:true,reason} yollanir ve neden doner:
// view_only | unsupported | too_large | invalid | rate_limited.
func (v *screen2Viewer) clipboardImage(ctx context.Context, png []byte) string {
	h := v.hub
	h.mu.Lock()
	vo := v.viewOnly
	h.mu.Unlock()
	reason := ""
	switch {
	case vo:
		reason = clipReasonViewOnly
	case !h.imgOK || h.imgAgent == nil:
		reason = clipReasonUnsupported
	case len(png) > protocol.MaxScreenClipboardImage:
		reason = clipReasonTooLarge
	case !protocol.ClipImageOK(png):
		reason = clipReasonInvalid
	default:
		v.imgMu.Lock()
		if time.Since(v.lastImg) < clipImgGap {
			reason = clipReasonRateLimited
		} else {
			v.lastImg = time.Now()
		}
		v.imgMu.Unlock()
	}
	if reason != "" {
		v.clipImageStatus(false, reason)
		return reason
	}
	if err := h.imgAgent.ScreenClipboardImageSet(ctx, h.sid, png); err != nil {
		v.clipImageStatus(false, clipReasonUnsupported)
		return clipReasonUnsupported
	}
	v.clipImageStatus(true, "")
	return ""
}

// ping, panelin {"type":"ping","t":...} mesajina {"type":"pong","t":...} yanitlar
// (panel<->sunucu gidis-donus; t panelin saatidir, aynen yankilanir). Ajan
// FeatureScreenPing pazarladiysa son olculen sunucu<->ajan gidis-donusu
// "agent_ms" olarak eklenir ve yeni olcum tetiklenir. Izleyici basina hiz
// sinirli (pingMinGap); sinirdaki ping sessizce atilir. t yalniz sayi olabilir.
func (v *screen2Viewer) ping(t json.RawMessage) bool {
	if len(t) == 0 || len(t) > 32 || !json.Valid(t) || (t[0] != '-' && (t[0] < '0' || t[0] > '9')) {
		return false
	}
	v.imgMu.Lock()
	now := time.Now()
	if now.Sub(v.lastPing) < pingMinGap {
		v.imgMu.Unlock()
		return false
	}
	v.lastPing = now
	v.imgMu.Unlock()

	m := map[string]any{"type": "pong", "t": t}
	if pa := v.hub.pingAgent; pa != nil {
		if d, ok := pa.AgentRTT(); ok {
			m["agent_ms"] = float64(d.Microseconds()) / 1000
		}
		go func() {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			_ = pa.ScreenPing(ctx)
		}()
	}
	b, _ := json.Marshal(m)
	v.sendMsg(b)
	return true
}
