//go:build linux

package agent

import (
	"errors"

	"github.com/jezek/xgb"
	"github.com/jezek/xgb/xfixes"
	"github.com/jezek/xgb/xproto"
)

// Linux X11 imlec kaynagi — saf Go (xgb), cgo YOK. Konum: QueryPointer.
// Bicim: XFIXES GetCursorImage; yalniz CursorNotify olayinda yeniden okunur
// (her yoklamada buyuk bicim verisi cekilmez). XFIXES yoksa yalniz konum.
// Wayland (DISPLAY yok): kaynak acilamaz, imlec mesaji gonderilmez.

type x11CursorSource struct {
	conn   *xgb.Conn
	root   xproto.Window
	fixes  bool
	dirty  bool
	serial uint32
	shapes map[uint32]*cursorShape
}

func cursorSupported() bool { return true }

func newCursorSource() cursorSource {
	conn, err := xgb.NewConn()
	if err != nil {
		return nil
	}
	s := &x11CursorSource{
		conn: conn, root: xproto.Setup(conn).DefaultScreen(conn).Root,
		shapes: map[uint32]*cursorShape{}, dirty: true,
	}
	if xfixes.Init(conn) == nil {
		if _, err := xfixes.QueryVersion(conn, 4, 0).Reply(); err == nil {
			if xfixes.SelectCursorInputChecked(conn, s.root, xfixes.CursorNotifyMaskDisplayCursor).Check() == nil {
				s.fixes = true
			}
		}
	}
	return s
}

func (s *x11CursorSource) Close() { s.conn.Close() }

func (s *x11CursorSource) Poll() (cursorState, error) {
	p, err := xproto.QueryPointer(s.conn, s.root).Reply()
	if err != nil {
		return cursorState{}, err
	}
	if !p.SameScreen {
		return cursorState{}, errors.New("imlec baska ekranda")
	}
	st := cursorState{X: int(p.RootX), Y: int(p.RootY), Visible: true}
	if !s.fixes {
		return st, nil
	}
	for {
		ev, _ := s.conn.PollForEvent()
		if ev == nil {
			break
		}
		if _, ok := ev.(xfixes.CursorNotifyEvent); ok {
			s.dirty = true
		}
	}
	if s.dirty {
		s.dirty = false
		if img, err := xfixes.GetCursorImage(s.conn).Reply(); err == nil &&
			img.Width > 0 && img.Height > 0 && img.Width <= 512 && img.Height <= 512 &&
			len(img.CursorImage) >= int(img.Width)*int(img.Height) {
			s.serial = img.CursorSerial
			if _, ok := s.shapes[s.serial]; !ok {
				if len(s.shapes) > 64 {
					s.shapes = map[uint32]*cursorShape{}
				}
				w, h := int(img.Width), int(img.Height)
				s.shapes[s.serial] = &cursorShape{
					W: w, H: h, HotX: min(int(img.Xhot), w-1), HotY: min(int(img.Yhot), h-1),
					RGBA: argbToRGBA(img.CursorImage[:w*h]),
				}
			}
		}
	}
	st.Handle = uintptr(s.serial)
	return st, nil
}

func (s *x11CursorSource) Shape(handle uintptr) (*cursorShape, bool) {
	sh, ok := s.shapes[uint32(handle)]
	return sh, ok
}
