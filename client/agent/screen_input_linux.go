//go:build linux

package agent

import (
	"fmt"
	"os"
	"sync"
	"time"

	"github.com/jezek/xgb"
	"github.com/jezek/xgb/xproto"
	"github.com/jezek/xgb/xtest"
	"github.com/tkodcumpeg4/zorven/shared/protocol"
)

// Linux X11 uzaktan girdi: saf Go (xgb + XTEST FakeInput), cgo YOK.
// Wayland'de uygulama girdisi enjekte edilemez; orada "unsupported" doner.

// displayRect, yakalanan monitorun kok pencere icindeki mutlak dikdortgeni.
type displayRect struct{ x, y, w, h int }

// X olay tipleri (XTEST FakeInput type alani).
const (
	xKeyPress      = 2
	xKeyRelease    = 3
	xButtonPress   = 4
	xButtonRelease = 5
	xMotionNotify  = 6
)

// xRetryAfter, baglanti hatasindan sonra yeniden denemeden once beklenen sure.
const xRetryAfter = time.Second

type xInput struct {
	mu       sync.Mutex
	conn     *xgb.Conn
	root     xproto.Window
	rootW    int
	rootH    int
	keymap   map[uint32]keyLoc
	keymapAt time.Time
	failAt   time.Time
	failRes  inputResult
}

var xin xInput

func applyInput(msg protocol.ScreenInput, rect displayRect) inputResult {
	xin.mu.Lock()
	defer xin.mu.Unlock()
	return xin.apply(msg, rect)
}

func (x *xInput) ensure() inputResult {
	if x.conn != nil {
		return inputResult{State: protocol.InputStateOK}
	}
	if os.Getenv("DISPLAY") == "" {
		if os.Getenv("WAYLAND_DISPLAY") != "" {
			return inputResult{State: protocol.InputStateUnsupported, Detail: "Wayland: uzaktan girdi desteklenmiyor"}
		}
		return inputResult{State: protocol.InputStateNoSession, Detail: "DISPLAY tanimli degil (grafik oturum yok)"}
	}
	if !x.failAt.IsZero() && time.Since(x.failAt) < xRetryAfter {
		return x.failRes
	}
	fail := func(r inputResult) inputResult {
		x.failAt, x.failRes = time.Now(), r
		return r
	}
	c, err := xgb.NewConn()
	if err != nil {
		return fail(inputResult{State: protocol.InputStateError, Detail: "X sunucusuna baglanilamadi: " + err.Error()})
	}
	if err := xtest.Init(c); err != nil {
		c.Close()
		return fail(inputResult{State: protocol.InputStateUnsupported, Detail: "X sunucusunda XTEST yok"})
	}
	si := xproto.Setup(c).DefaultScreen(c)
	x.conn, x.root = c, si.Root
	x.rootW, x.rootH = int(si.WidthInPixels), int(si.HeightInPixels)
	x.keymap, x.keymapAt, x.failAt = nil, time.Time{}, time.Time{}
	return inputResult{State: protocol.InputStateOK}
}

// drop, baglantiyi atar; sonraki olayda yeniden kurulur.
func (x *xInput) drop() {
	if x.conn != nil {
		x.conn.Close()
	}
	x.conn, x.keymap = nil, nil
}

func (x *xInput) apply(msg protocol.ScreenInput, rect displayRect) inputResult {
	if r := x.ensure(); r.State != protocol.InputStateOK {
		return r
	}
	res := inputResult{State: protocol.InputStateOK}
	switch msg.Kind {
	case "mousemove":
		res = res.merge(x.move(msg.X, msg.Y, rect))
	case "mousedown":
		res = res.merge(x.move(msg.X, msg.Y, rect))
		res = res.merge(x.button(browserButton(msg.Button), true))
	case "mouseup":
		res = res.merge(x.move(msg.X, msg.Y, rect))
		res = res.merge(x.button(browserButton(msg.Button), false))
	case "wheel":
		res = res.merge(x.wheel(msg))
	case "keydown":
		res = res.merge(x.key(msg, true))
	case "keyup":
		res = res.merge(x.key(msg, false))
	}
	return res
}

// fake, tek FakeInput istegi gonderir ve sonucu bekler. Protokol hatasi
// (xgb.Error) baglantiyi bozmaz; baska hata baglantiyi atar.
func (x *xInput) fake(typ, detail byte, px, py int16) inputResult {
	err := xtest.FakeInputChecked(x.conn, typ, detail, 0, x.root, px, py, 0).Check()
	if err == nil {
		return inputResult{State: protocol.InputStateOK}
	}
	if _, isProto := err.(xgb.Error); !isProto {
		x.drop()
	}
	return inputResult{State: protocol.InputStateError, Detail: "FakeInput basarisiz: " + err.Error()}
}

func (x *xInput) move(nx, ny float64, rect displayRect) inputResult {
	px, py := scaleToDisplay(nx, ny, rect, x.rootW, x.rootH)
	return x.fake(xMotionNotify, 0, int16(px), int16(py))
}

// browserButton, tarayici dugme numarasini X dugmesine cevirir.
func browserButton(b int) byte {
	switch b {
	case 1:
		return 2
	case 2:
		return 3
	case 3:
		return 8 // geri
	case 4:
		return 9 // ileri
	}
	return 1
}

func (x *xInput) button(b byte, down bool) inputResult {
	return x.fake(pickX(down, byte(xButtonPress), byte(xButtonRelease)), b, 0, 0)
}

func pickX[T any](c bool, a, b T) T {
	if c {
		return a
	}
	return b
}

func (x *xInput) click(b byte, n int) inputResult {
	res := inputResult{State: protocol.InputStateOK}
	for i := 0; i < n; i++ {
		res = res.merge(x.button(b, true)).merge(x.button(b, false))
	}
	return res
}

// wheel: X'te dikey 4 (yukari) / 5 (asagi), yatay 6 (sol) / 7 (sag).
func (x *xInput) wheel(msg protocol.ScreenInput) inputResult {
	res := inputResult{State: protocol.InputStateOK}
	if n := wheelClicks(msg.DeltaY, msg.DeltaMode); n > 0 {
		res = res.merge(x.click(pickX(msg.DeltaY > 0, byte(5), byte(4)), n))
	}
	if n := wheelClicks(msg.DeltaX, msg.DeltaMode); n > 0 {
		res = res.merge(x.click(pickX(msg.DeltaX > 0, byte(7), byte(6)), n))
	}
	return res
}

// lookup, keysym'in keycode'unu bulur; bulamazsa tabloyu (hizi sinirli) yeniler.
func (x *xInput) lookup(sym uint32) (keyLoc, bool) {
	if x.keymap != nil {
		if l, ok := x.keymap[sym]; ok {
			return l, true
		}
		if time.Since(x.keymapAt) < time.Second {
			return keyLoc{}, false
		}
	}
	if err := x.loadKeymap(); err != nil {
		return keyLoc{}, false
	}
	l, ok := x.keymap[sym]
	return l, ok
}

func (x *xInput) loadKeymap() error {
	setup := xproto.Setup(x.conn)
	first := byte(setup.MinKeycode)
	count := byte(int(setup.MaxKeycode) - int(first) + 1)
	rep, err := xproto.GetKeyboardMapping(x.conn, xproto.Keycode(first), count).Reply()
	if err != nil {
		if _, isProto := err.(xgb.Error); !isProto {
			x.drop()
		}
		return err
	}
	syms := make([]uint32, len(rep.Keysyms))
	for i, s := range rep.Keysyms {
		syms[i] = uint32(s)
	}
	x.keymap = buildKeymap(first, int(rep.KeysymsPerKeycode), syms)
	x.keymapAt = time.Now()
	return nil
}

func (x *xInput) pressCode(code byte, down bool) inputResult {
	return x.fake(pickX(down, byte(xKeyPress), byte(xKeyRelease)), code, 0, 0)
}

func noKeysym(msg protocol.ScreenInput) inputResult {
	return inputResult{State: protocol.InputStateError,
		Detail: fmt.Sprintf("tus icin keysym/keycode yok (code=%q key=%q)", msg.Code, msg.Key)}
}

// key, tus olayini uygular. Yazilabilir karakter (kisayol degilken) key'den,
// diger her sey fiziksel code'dan cozulur. Klavye eslemesi DEGISTIRILMEZ;
// eslesmeyen tus error durumu doner.
func (x *xInput) key(msg protocol.ScreenInput, down bool) inputResult {
	shortcut := msg.Ctrl || msg.Alt || msg.Meta
	if !shortcut {
		if r := singleRuneKey(msg.Key); r != 0 && r != ' ' {
			if sym := runeKeysym(r); sym != 0 {
				if loc, ok := x.lookup(sym); ok {
					return x.typeText(loc, msg, down)
				}
			}
		}
	}
	sym, ok := codeToKeysym(msg.Code)
	if !ok {
		if sym, ok = keyNameKeysym(msg.Key); !ok {
			if r := singleRuneKey(msg.Key); r != 0 {
				sym = runeKeysym(r)
				ok = sym != 0
			}
		}
	}
	if !ok {
		return noKeysym(msg)
	}
	loc, found := x.lookup(sym)
	if !found && sym == xkAltR {
		loc, found = x.lookup(xkISOLevel3)
	}
	if !found {
		return noKeysym(msg)
	}
	return x.pressCode(loc.code, down)
}

// typeText, yazilabilir karakteri duzenden bagimsiz dogru seviyede basar.
// Shift gerekiyorsa ve tarayici shift basili bildirmiyorsa gecici shift sarar;
// taban seviye gerekip shift basiliysa gecici olarak birakir.
func (x *xInput) typeText(loc keyLoc, msg protocol.ScreenInput, down bool) inputResult {
	if !down {
		return x.pressCode(loc.code, false)
	}
	needShift := loc.col == 1
	wrap := 0
	switch {
	case needShift && !msg.Shift:
		wrap = 1 // shift bas
	case !needShift && msg.Shift && loc.col == 0:
		wrap = 2 // shift'i gecici birak
	}
	if wrap == 0 {
		return x.pressCode(loc.code, true)
	}
	sh, ok := x.lookup(xkShiftL)
	if !ok {
		return x.pressCode(loc.code, true)
	}
	res := x.pressCode(sh.code, wrap == 1)
	res = res.merge(x.pressCode(loc.code, true))
	return res.merge(x.pressCode(sh.code, wrap != 1))
}
