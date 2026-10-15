package agent

import (
	"context"
	"time"

	"github.com/tkodcumpeg4/zorven/shared/protocol"
)

// Imlec katmani: kareler imleci icermez; istemci konum/gorunurluk ile bicimi
// AYRI bildirir, panel ustte cizer. Platform kaynagi cursorSource arkasinda
// (Windows: GetCursorInfo/GetIconInfo; Linux X11: QueryPointer + XFIXES).

// cursorState, bir yoklamanin sonucu. X/Y SANAL MASAUSTU piksel koordinatidir.
type cursorState struct {
	X, Y    int
	Visible bool
	Handle  uintptr // bicim kimligi: ayni bicim ayni tutacagi doner
}

// cursorShape, RGBA imlec bicimi (en fazla protocol.MaxCursorDim kare).
type cursorShape struct {
	W, H       int
	HotX, HotY int
	RGBA       []byte
}

// cursorSource, platform imlec kaynagi.
type cursorSource interface {
	Poll() (cursorState, error)
	// Shape, tutacagin bicimini dondurur (cagiran onbellekler).
	Shape(handle uintptr) (*cursorShape, bool)
	Close()
}

const (
	// cursorMinInterval: konum mesajlari arasi en az sure (~30/sn).
	cursorMinInterval = 33 * time.Millisecond
	// cursorPollInterval: kaynak yoklama araligi.
	cursorPollInterval = 25 * time.Millisecond
	// cursorShapeCacheMax: tutac -> bicim onbellegi siniri.
	cursorShapeCacheMax = 32
)

// cursorTracker, yoklama sonuclarini kisip degisimde gonderen platformdan
// bagimsiz mantik (birim testli).
type cursorTracker struct {
	src cursorSource
	// bounds: yakalanan monitorun sanal masaustundeki dikdortgeni (anlik).
	bounds func() displayRect
	// canvas: screen2 tuvalinin (olceklenmis kare) boyutu; 0 => henuz kare yok.
	canvas    func() (w, h int)
	sendPos   func(protocol.ScreenCursor)
	sendShape func(protocol.ScreenCursorShape)

	minInterval time.Duration

	// durum (tek goroutine)
	haveLast   bool
	last       protocol.ScreenCursor
	lastAt     time.Time
	handle     uintptr
	haveHandle bool
	cache      map[uintptr]*cursorShape
}

func newCursorTracker(src cursorSource, bounds func() displayRect, canvas func() (int, int),
	sendPos func(protocol.ScreenCursor), sendShape func(protocol.ScreenCursorShape)) *cursorTracker {
	return &cursorTracker{
		src: src, bounds: bounds, canvas: canvas, sendPos: sendPos, sendShape: sendShape,
		minInterval: cursorMinInterval, cache: map[uintptr]*cursorShape{},
	}
}

// toCanvas, sanal masaustu koordinatini tuval (olceklenmis kare) pikseline
// cevirir. Imlec yakalanan monitorun disindaysa visible=false doner.
func toCanvas(x, y int, b displayRect, cw, ch int) (cx, cy int, inside bool) {
	if b.w <= 0 || b.h <= 0 || cw <= 0 || ch <= 0 {
		return 0, 0, false
	}
	rx, ry := x-b.x, y-b.y
	if rx < 0 || ry < 0 || rx >= b.w || ry >= b.h {
		return 0, 0, false
	}
	return rx * cw / b.w, ry * ch / b.h, true
}

// Step, tek yoklama turu. now enjekte edilir (test).
func (t *cursorTracker) Step(now time.Time) {
	st, err := t.src.Poll()
	if err != nil {
		st = cursorState{} // gizli say
	}

	// Bicim: yalniz tutac degisince gonderilir; tutac basina onbellek.
	if st.Visible && (!t.haveHandle || st.Handle != t.handle) {
		sh, ok := t.cache[st.Handle]
		if !ok {
			if s, got := t.src.Shape(st.Handle); got && s != nil {
				s = limitCursorShape(s)
				if len(t.cache) >= cursorShapeCacheMax {
					t.cache = map[uintptr]*cursorShape{}
				}
				t.cache[st.Handle] = s
				sh, ok = s, true
			}
		}
		if ok && sh != nil {
			t.sendShape(protocol.ScreenCursorShape{
				Type: protocol.TypeScreenCursorShape,
				W:    sh.W, H: sh.H, HotX: sh.HotX, HotY: sh.HotY, Data: sh.RGBA,
			})
			t.handle, t.haveHandle = st.Handle, true
		}
	}

	cw, ch := t.canvas()
	cx, cy, inside := toCanvas(st.X, st.Y, t.bounds(), cw, ch)
	cur := protocol.ScreenCursor{
		Type: protocol.TypeScreenCursor, X: cx, Y: cy, Visible: st.Visible && inside, W: cw, H: ch,
	}
	if !cur.Visible {
		cur.X, cur.Y = 0, 0 // gizliyken konum anlamsiz: gereksiz degisim uretme
	}
	if t.haveLast && cur == t.last {
		return
	}
	if t.haveLast && now.Sub(t.lastAt) < t.minInterval {
		return // sonraki turda (degisim last'tan farkli kaldigi icin) gonderilir
	}
	t.sendPos(cur)
	t.last, t.lastAt, t.haveLast = cur, now, true
}

// Run, ctx bitene kadar yoklar.
func (t *cursorTracker) Run(ctx context.Context) {
	tk := time.NewTicker(cursorPollInterval)
	defer tk.Stop()
	defer t.src.Close()
	for {
		select {
		case <-ctx.Done():
			return
		case now := <-tk.C:
			t.Step(now)
		}
	}
}

// limitCursorShape, bicimi protocol.MaxCursorDim karesine sigdirir (en yakin
// komsu); hotspot oranlanir. Sinirdaysa aynen doner.
func limitCursorShape(s *cursorShape) *cursorShape {
	m := protocol.MaxCursorDim
	if s.W <= m && s.H <= m {
		return s
	}
	nw, nh := s.W, s.H
	if nw >= nh {
		nh = max(1, nh*m/nw)
		nw = m
	} else {
		nw = max(1, nw*m/nh)
		nh = m
	}
	out := make([]byte, nw*nh*4)
	for y := 0; y < nh; y++ {
		sy := y * s.H / nh
		for x := 0; x < nw; x++ {
			sx := x * s.W / nw
			copy(out[(y*nw+x)*4:(y*nw+x)*4+4], s.RGBA[(sy*s.W+sx)*4:(sy*s.W+sx)*4+4])
		}
	}
	return &cursorShape{
		W: nw, H: nh,
		HotX: min(nw-1, s.HotX*nw/s.W), HotY: min(nh-1, s.HotY*nh/s.H),
		RGBA: out,
	}
}

// cursorFromBitmaps, Windows GetDIBits ciktilarindan RGBA imleci kurar.
//
//	color: w*h*4 BGRA (renkli imlec) veya nil (tek renkli imlec)
//	mask:  w*maskH*4 BGRA; 1bpp AND/XOR maskesinin 32bpp'ye acilmis hali
//	       (piksel != 0 => maske biti 1). Renkli imlecte maskH==h (AND maskesi),
//	       tek renklide maskH==2*h (ust yari AND, alt yari XOR).
//
// Renkli imlecte alfa kanali varsa (on-carpimli) duz RGBA'ya cevrilir; yoksa
// opaklik AND maskesinden gelir. Tek renklide: AND=0,XOR=0 siyah; AND=0,XOR=1
// beyaz; AND=1,XOR=0 seffaf; AND=1,XOR=1 (ters cevirme) siyah olarak yaklasik.
func cursorFromBitmaps(w, h int, color, mask []byte) *cursorShape {
	if w <= 0 || h <= 0 {
		return nil
	}
	out := make([]byte, w*h*4)
	andBit := func(x, y int) bool {
		i := (y*w + x) * 4
		return i+2 < len(mask) && (mask[i] != 0 || mask[i+1] != 0 || mask[i+2] != 0)
	}
	if color != nil {
		if len(color) < w*h*4 {
			return nil
		}
		hasAlpha := false
		for i := 3; i < w*h*4; i += 4 {
			if color[i] != 0 {
				hasAlpha = true
				break
			}
		}
		for y := 0; y < h; y++ {
			for x := 0; x < w; x++ {
				i := (y*w + x) * 4
				b, g, r, a := color[i], color[i+1], color[i+2], color[i+3]
				if hasAlpha {
					if a > 0 && a < 255 { // on-carpimli -> duz
						r = uint8(min(255, int(r)*255/int(a)))
						g = uint8(min(255, int(g)*255/int(a)))
						b = uint8(min(255, int(b)*255/int(a)))
					}
				} else if mask != nil && andBit(x, y) {
					a = 0
				} else {
					a = 255
				}
				out[i], out[i+1], out[i+2], out[i+3] = r, g, b, a
			}
		}
		return &cursorShape{W: w, H: h, RGBA: out}
	}
	// tek renkli
	if len(mask) < w*h*2*4 {
		return nil
	}
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			and, xor := andBit(x, y), andBit(x, y+h)
			i := (y*w + x) * 4
			switch {
			case and && !xor:
				// seffaf
			case !and && xor:
				out[i], out[i+1], out[i+2], out[i+3] = 255, 255, 255, 255
			default:
				out[i+3] = 255 // siyah (ters cevirme yaklasigi dahil)
			}
		}
	}
	return &cursorShape{W: w, H: h, RGBA: out}
}

// argbToRGBA, X11 XFIXES on-carpimli ARGB (uint32) piksellerini duz RGBA'ya cevirir.
func argbToRGBA(px []uint32) []byte {
	out := make([]byte, len(px)*4)
	for i, p := range px {
		a := uint8(p >> 24)
		r, g, b := uint8(p>>16), uint8(p>>8), uint8(p)
		if a > 0 && a < 255 {
			r = uint8(min(255, int(r)*255/int(a)))
			g = uint8(min(255, int(g)*255/int(a)))
			b = uint8(min(255, int(b)*255/int(a)))
		}
		out[i*4], out[i*4+1], out[i*4+2], out[i*4+3] = r, g, b, a
	}
	return out
}
