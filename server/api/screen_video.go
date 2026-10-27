package api

import (
	"context"
	"encoding/json"
	"time"

	"github.com/tkodcumpeg4/zorven/shared/protocol"
)

// H.264 video modu (FeatureScreen2Video) -- paylasimli yayinda ilke.
//
// Ajan tek bir akis uretir (ya karo ya video); bu yuzden izleyicilerin
// tercihleri TEK bir etkin moda birlestirilir ve ajana screen2_video ile
// bildirilir. KARAR (basit ve tahmin edilebilir): "en temkinli kazanir".
//
//   - Ajan video pazarlamadiysa ya da hic izleyici tercih bildirmediyse: tiles.
//   - Tercihini bildirmis (video mesaji gonderen) izleyicilerden HERHANGI BIRI
//     video desteklemiyorsa (WebCodecs yok) ya da "tiles" istiyorsa: tiles.
//     Yani video desteklemeyen bir izleyici, o izleyici bagliyken tum oturumu
//     karo moduna zorlar; ayrilinca video yeniden devreye girebilir.
//   - Aksi halde izleyicilerden biri "video" istiyorsa: video; hepsi "auto"
//     ise: auto (ajan hareketli icerigi algilayip kendisi gecer).
//
// Tercihini henuz bildirmemis (bekleyen) izleyici politikayi etkilemez ve
// kendisine video verilmez; bildirince politika yeniden hesaplanir. Boylece
// katilim sirasinda gereksiz tiles/video gidip gelmesi olmaz.
//
// Video cerceveleri izleyicinin ortak v.out kuyrugundan akar (karo
// mesajlariyla sirasi korunur). Kuyruk dolarsa o izleyici icin video, bir
// sonraki config (+IDR) gelene kadar atlanir ve ajandan IDR istenir.

// videoAgent, video modunu destekleyen istemci oturumu (*tunnel.Session);
// screen2Agent'a EKLENMEZ (eski sahte uygulamalar bozulmasin).
type videoAgent interface {
	SupportsScreen2Video() bool
	Screen2Video(ctx context.Context, sessionID, mode string) error
}

// videoState, izleyicinin video durumu (hub.mu altinda).
type videoState struct {
	known     bool   // panel tercihini bildirdi
	supported bool   // WebCodecs var
	mode      string // auto|tiles|video
	resync    bool   // config (IDR) gelene kadar chunk atlanir
}

// videoHubState, hub'in video durumu (hub.mu altinda).
type videoHubState struct {
	ok     bool // ajan pazarladi
	agent  videoAgent
	sent   string // ajana en son bildirilen mod
	active bool   // video akiyor (son cerceve stop degil)
}

// effectiveVideoModeLocked, izleyici tercihlerinden etkin modu hesaplar (h.mu elde).
func (h *screen2Hub) effectiveVideoModeLocked() string {
	if !h.vid.ok {
		return protocol.Screen2VideoTiles
	}
	known, wantVideo := 0, false
	for v := range h.viewers {
		if !v.vid.known {
			continue
		}
		known++
		if !v.vid.supported || v.vid.mode == protocol.Screen2VideoTiles {
			return protocol.Screen2VideoTiles
		}
		if v.vid.mode == protocol.Screen2VideoOn {
			wantVideo = true
		}
	}
	switch {
	case known == 0:
		return protocol.Screen2VideoTiles
	case wantVideo:
		return protocol.Screen2VideoOn
	}
	return protocol.Screen2VideoAuto
}

// vidDeliverLocked, izleyiciye video verilir mi (h.mu elde).
func (h *screen2Hub) vidDeliverLocked(v *screen2Viewer, eff string) bool {
	return eff != protocol.Screen2VideoTiles && v.vid.known && v.vid.supported
}

// setVideoPrefs, panelin video tercihini kaydeder ve politikayi yeniden hesaplar.
func (v *screen2Viewer) setVideoPrefs(supported bool, mode string) {
	h := v.hub
	if !protocol.ValidVideoMode(mode) {
		mode = protocol.Screen2VideoAuto
	}
	h.mu.Lock()
	if h.closed {
		h.mu.Unlock()
		return
	}
	v.vid.known, v.vid.supported, v.vid.mode = true, supported, mode
	v.vid.resync = true
	h.mu.Unlock()
	h.syncVideo(true)
	// Hiz sinirina takilirsa IDR istegini tekrarla.
	time.AfterFunc(screen2KeyframeRetryJoin, func() {
		h.mu.Lock()
		still := !h.closed && !v.gone && v.vid.resync && h.vid.active
		h.mu.Unlock()
		if still {
			h.askKeyframe()
		}
	})
}

// syncVideo, etkin modu hesaplar; degistiyse ajana bildirir ve izleyicilere
// politikayi yayinlar. force: izleyici tercihi degisti, video akiyorsa yeni
// katilana IDR iste.
func (h *screen2Hub) syncVideo(askIDR bool) {
	if !h.vid.ok {
		return
	}
	h.mu.Lock()
	if h.closed {
		h.mu.Unlock()
		return
	}
	eff := h.effectiveVideoModeLocked()
	changed := eff != h.vid.sent
	if changed {
		h.vid.sent = eff
		if eff == protocol.Screen2VideoTiles {
			h.vid.active = false
		}
		for w := range h.viewers {
			w.vid.resync = true
		}
	}
	unsupported, known := 0, 0
	for w := range h.viewers {
		if w.vid.known {
			known++
			if !w.vid.supported {
				unsupported++
			}
		}
	}
	pol, _ := json.Marshal(map[string]any{
		"type": "video_policy", "effective": eff, "unsupported": unsupported, "viewers": known,
	})
	for w := range h.viewers {
		w.sendMsg(pol)
	}
	active := h.vid.active
	h.mu.Unlock()
	if changed {
		go func() {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			_ = h.vid.agent.Screen2Video(ctx, h.sid, eff)
		}()
	}
	if askIDR && active && eff != protocol.Screen2VideoTiles {
		h.askKeyframe() // yeni izleyiciye config+IDR
	}
}

// relayVideo, ajandan gelen FrameVideo mesajini izleyicilere dagitir (pump).
func (h *screen2Hub) relayVideo(b []byte) {
	if len(b) < 15 {
		return
	}
	kind := b[14]
	needAsk := false
	h.mu.Lock()
	if h.closed {
		h.mu.Unlock()
		return
	}
	eff := h.effectiveVideoModeLocked()
	if kind == protocol.VideoKindStop {
		h.vid.active = false
	} else if eff != protocol.Screen2VideoTiles {
		h.vid.active = true
	}
	for v := range h.viewers {
		if kind == protocol.VideoKindStop {
			// Mod zaten tiles'a dondu: stop, video alan izleyicilere yine de gider.
			if !(v.vid.known && v.vid.supported) {
				continue
			}
		} else if !h.vidDeliverLocked(v, eff) {
			continue
		}
		if v.vid.resync && kind == protocol.VideoKindChunk {
			needAsk = true
			continue
		}
		if kind == protocol.VideoKindChunk {
			v.fbArrived++
		}
		select {
		case v.out <- b:
			if kind == protocol.VideoKindConfig {
				v.vid.resync = false
			}
			if kind == protocol.VideoKindChunk {
				v.fbDelivered++
			}
		default:
			if kind != protocol.VideoKindStop {
				v.vid.resync = true
				v.fbBlocked++
				needAsk = true
			}
		}
	}
	h.mu.Unlock()
	if needAsk {
		h.askKeyframe()
	}
}

// videoActiveFor, izleyiciye video akiyor mu (keyframe istegi icin).
func (v *screen2Viewer) videoActive() bool {
	h := v.hub
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.vid.active && h.vidDeliverLocked(v, h.effectiveVideoModeLocked())
}
