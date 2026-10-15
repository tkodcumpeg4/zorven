package agent

import (
	"context"
	"errors"
	"fmt"
	"image"
	"sort"
	"time"

	"github.com/tkodcumpeg4/zorven/shared/protocol"
)

// screen2 katmanli mod (FeatureScreen2Layers). Sunucu hangi katmanlarin
// gerektigini bildirir; istemci YALNIZ o katmanlari kodlar. Her katman kendi
// genisligine olceklenir ve kendi tuvalinde 64 px karo izgarasina HIZALI,
// karo basina TEK dikdortgen gonderir (sunucu karo basina son yuku tutar;
// bir JPEG parcalanamaz, bu yuzden dikdortgen = tam bir karo). Kenar karolari
// tuval sinirina kirpilir.

const (
	// screen2MaxLayerID, katman kimligi ust siniri (bayrak bit3-4 => 0..3).
	screen2MaxLayerID = 3
	// screen2MinLayerWidth, otomatik hesaplanan katman genisliginin tabani.
	screen2MinLayerWidth = 160
	// screen2IdleTick, etkin katman yokken (kimse izlemiyor) bekleme araligi.
	screen2IdleTick = 100 * time.Millisecond
)

// layerUpdate, sunucudan gelen bekleyen katman kumesi.
type layerUpdate struct {
	set    bool
	layers []protocol.Screen2LayerSpec
	key    bool
}

// SetLayers, etkin katman kumesini degistirir (goroutine-guvenli, bloklamaz).
// Birbirini ezen guncellemelerde en yenisi kazanir; key istegi KAYBOLMAZ.
func (s *screen2Session) SetLayers(l []protocol.Screen2LayerSpec, key bool) {
	s.layerMu.Lock()
	s.layerIn.set = true
	s.layerIn.layers = append([]protocol.Screen2LayerSpec(nil), l...)
	s.layerIn.key = s.layerIn.key || key
	s.layerMu.Unlock()
}

func (s *screen2Session) takeLayerUpdate() layerUpdate {
	s.layerMu.Lock()
	defer s.layerMu.Unlock()
	u := s.layerIn
	s.layerIn = layerUpdate{}
	return u
}

// layerWidth, katmanin etkin genisligini cozer: acik MaxWidth > 0 ise o;
// katman 0 icin varsayilan; diger katmanlar icin katman 0 genisliginin yarisi.
func layerWidth(spec protocol.Screen2LayerSpec, all []protocol.Screen2LayerSpec) int {
	if spec.MaxWidth > 0 {
		return spec.MaxWidth
	}
	if spec.ID == 0 {
		return defaultMaxWidth
	}
	base := defaultMaxWidth
	for _, o := range all {
		if o.ID == 0 && o.MaxWidth > 0 {
			base = o.MaxWidth
		}
	}
	return max(base/2, screen2MinLayerWidth)
}

// layerTileRects, kirli karolari karo basina bir dikdortgen olarak doner
// (izgaraya hizali; sag/alt kenar tuvale kirpilir).
func layerTileRects(dirty []bool, tx, ty, w, h int) []image.Rectangle {
	var out []image.Rectangle
	for j := 0; j < ty; j++ {
		for i := 0; i < tx; i++ {
			if dirty[j*tx+i] {
				out = append(out, image.Rect(i*tileSize, j*tileSize,
					min((i+1)*tileSize, w), min((j+1)*tileSize, h)))
			}
		}
	}
	return out
}

// layerState, bir etkin katmanin yakalama durumu.
type layerState struct {
	spec    protocol.Screen2LayerSpec
	width   int
	prev    *image.RGBA
	needKey bool
}

// applyLayerUpdate, etkin kumeyi gunceller: yeni katman tam kare ister,
// genisligi degisen katman da; kalkan katman silinir (kodlanmaz).
func applyLayerUpdate(cur []*layerState, u layerUpdate) []*layerState {
	if !u.set {
		if u.key {
			for _, st := range cur {
				st.needKey = true
			}
		}
		return cur
	}
	byID := map[int]*layerState{}
	for _, st := range cur {
		byID[st.spec.ID] = st
	}
	var next []*layerState
	seen := map[int]bool{}
	for _, sp := range u.layers {
		if sp.ID < 0 || sp.ID > screen2MaxLayerID || seen[sp.ID] {
			continue
		}
		seen[sp.ID] = true
		w := layerWidth(sp, u.layers)
		st := byID[sp.ID]
		if st == nil {
			st = &layerState{needKey: true}
		} else if st.width != w {
			st.prev, st.needKey = nil, true
		}
		st.spec, st.width = sp, w
		if u.key {
			st.needKey = true
		}
		next = append(next, st)
	}
	sort.Slice(next, func(i, j int) bool { return next[i].spec.ID < next[j].spec.ID })
	return next
}

// runLayered, katmanli yakalama dongusu. Etkin katman yoksa yakalama YAPILMAZ.
func (s *screen2Session) runLayered(ctx context.Context) error {
	var layers []*layerState
	next := time.Now()
	timer := time.NewTimer(0)
	defer timer.Stop()

	for {
		if d := time.Until(next); d > 0 {
			timer.Reset(d)
			select {
			case <-ctx.Done():
				return nil
			case <-timer.C:
			}
		} else if ctx.Err() != nil {
			return nil
		}
		start := time.Now()

		select {
		case p := <-s.setCh:
			if s.applyParams(p) {
				s.notifyDisplay()
				for _, st := range layers {
					st.prev, st.needKey = nil, true
				}
			}
		default:
		}
		if s.keyReq.Swap(false) {
			for _, st := range layers {
				st.needKey = true
			}
		}
		layers = applyLayerUpdate(layers, s.takeLayerUpdate())
		if len(layers) == 0 {
			next = start.Add(screen2IdleTick)
			continue
		}

		img, err := s.cap.Capture(s.display)
		if err != nil {
			if s.onAttach != nil {
				s.onAttach()
			}
			if img, err = s.cap.Capture(s.display); err != nil {
				return fmt.Errorf("ekran yakalanamadi: %w", err)
			}
		}

		var wrote time.Duration
		sent := false
		for i, st := range layers {
			frame := scaleRGBA(img, st.width)
			if frame.Rect.Dx() > 65535 || frame.Rect.Dy() > 65535 {
				return errors.New("ekran cozunurlugu screen2 icin cok buyuk")
			}
			if st.prev != nil && (st.prev.Rect.Dx() != frame.Rect.Dx() || st.prev.Rect.Dy() != frame.Rect.Dy()) {
				st.needKey = true
			}
			var dirty []bool
			var tx, ty int
			if st.needKey {
				st.prev = nil
			}
			dirty, tx, ty = diffTiles(st.prev, frame) // prev nil => hepsi kirli
			st.prev = frame
			if i == 0 {
				s.canvas.Store(uint64(frame.Rect.Dx())<<32 | uint64(frame.Rect.Dy()))
			}
			rects := layerTileRects(dirty, tx, ty, frame.Rect.Dx(), frame.Rect.Dy())
			if len(rects) == 0 {
				continue
			}
			q := st.spec.Quality
			if q <= 0 || q > 100 {
				q = 70
			}
			q = min(q, s.ctl.Quality())
			wire := make([]protocol.Screen2Rect, 0, len(rects))
			for _, r := range rects {
				e, payload, err := s.enc.Encode(frame, r, q)
				if err != nil {
					return fmt.Errorf("kare kodlanamadi: %w", err)
				}
				wire = append(wire, protocol.Screen2Rect{
					X: uint16(r.Min.X), Y: uint16(r.Min.Y), W: uint16(r.Dx()), H: uint16(r.Dy()),
					Enc: e, Payload: payload,
				})
			}
			msgs, err := protocol.PackScreen2Layer(s.streamID, s.seq, st.needKey, uint8(st.spec.ID),
				uint16(frame.Rect.Dx()), uint16(frame.Rect.Dy()), wire, 0)
			if err != nil {
				return fmt.Errorf("kare paketlenemedi: %w", err)
			}
			s.seq++
			wstart := time.Now()
			var n int64
			for _, m := range msgs {
				if err := s.send(ctx, m); err != nil {
					if ctx.Err() != nil {
						return nil
					}
					return err
				}
				n += int64(len(m))
			}
			wrote += time.Since(wstart)
			sent = true
			s.statMu.Lock()
			s.stats.Frames++
			if st.needKey {
				s.stats.Keyframes++
			}
			s.stats.Bytes += n
			s.statMu.Unlock()
			st.needKey = false
		}
		if sent {
			s.ctl.Observe(wrote)
		}

		next = start.Add(s.ctl.Interval())
		if now := time.Now(); next.Before(now) {
			next = now
		}
	}
}
