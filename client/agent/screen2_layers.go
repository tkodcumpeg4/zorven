package agent

import (
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
	scaler  frameScaler
	// nextDue, katmanin bir sonraki isleme vadesi (katman basina fps; captureLoop).
	nextDue time.Time
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
