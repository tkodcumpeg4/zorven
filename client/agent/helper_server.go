package agent

import (
	"encoding/json"
	"image"
	"io"

	"github.com/tkodcumpeg4/zorven/shared/protocol"
)

// helperBackend, yardimci surecin kullanici oturumunda yaptigi gercek isler.
// Windows'ta capture/girdi/pano uygulamalari; testte sahte.
type helperBackend interface {
	NumDisplays() int
	Bounds(d int) image.Rectangle
	// CaptureHinted: hintCapturer sozlesmesi (screen2_hint.go).
	CaptureHinted(d int) (img *image.RGBA, hint []image.Rectangle, hinted bool, err error)
	Method() string
	// Attach, calisan thread'i girdi masaustune yeniden baglar (UAC/kilit sonrasi).
	Attach() bool
	Input(msg protocol.ScreenInput, rect displayRect) inputResult
	ClipSeq() uint32
	ClipGet() (string, error)
	ClipSet(text string) error
}

type helperInputReq struct {
	Msg  protocol.ScreenInput `json:"msg"`
	Rect [4]int               `json:"rect"`
}

type helperInputResp struct {
	State  string `json:"state"`
	Code   int    `json:"code"`
	Detail string `json:"detail"`
}

const (
	clipOpSeq byte = 0
	clipOpGet byte = 1
	clipOpSet byte = 2
)

// helperServer, yardimci tarafinda istek dongusu. Tek is parcaciginda calisir
// (Windows masaustu baglamasi thread'e baglidir).
type helperServer struct {
	b          helperBackend
	onActivity func() // her istekte (bosta kalma zamanlayicisi icin)

	last     *image.RGBA
	lastDisp int
}

func serveHelper(rw io.ReadWriter, b helperBackend, onActivity func()) error {
	s := &helperServer{b: b, onActivity: onActivity, lastDisp: -1}
	for {
		typ, p, err := readFrame(rw)
		if err != nil {
			return err
		}
		if s.onActivity != nil {
			s.onActivity()
		}
		var respTyp byte
		var resp []byte
		switch typ {
		case hmShutdown:
			return nil
		case hmInfoReq:
			respTyp, resp = hmInfoResp, s.info()
		case hmCapReq:
			r := rbuf{b: p}
			d := r.i32()
			respTyp, resp = hmCapResp, s.capture(d, r.err)
		case hmInputReq:
			respTyp, resp = hmInputResp, s.input(p)
		case hmClipReq:
			respTyp, resp = hmClipResp, s.clip(p)
		default:
			return errHelperFrame
		}
		if err := writeFrame(rw, respTyp, resp); err != nil {
			return err
		}
	}
}

func (s *helperServer) info() []byte {
	n := s.b.NumDisplays()
	var b wbuf
	b.u8(0)
	b.u16(uint16(n))
	for i := 0; i < n; i++ {
		b.rect(s.b.Bounds(i))
	}
	return b.Bytes()
}

func (s *helperServer) capture(d int, perr error) []byte {
	if perr != nil {
		return encodeErr("bad request")
	}
	img, hint, hinted, err := s.b.CaptureHinted(d)
	if err != nil {
		// Masaustu degismis olabilir (UAC, kilit): yeniden baglan ve bir kez daha dene.
		s.b.Attach()
		img, hint, hinted, err = s.b.CaptureHinted(d)
	}
	c := capResp{Bounds: s.b.Bounds(d), Displays: s.b.NumDisplays(), Method: s.b.Method()}
	if err != nil {
		// Guvenli masaustu (UAC/kilit ekrani) kullanici oturumundan yakalanamaz:
		// servise son kareyi tekrar ettirir (degraded bayragi); hic kare yoksa hata.
		if s.last != nil && s.lastDisp == d {
			c.Flags = capFlagHinted | capFlagUnchang | capFlagDegraded
			c.W, c.H = s.last.Rect.Dx(), s.last.Rect.Dy()
			return encodeCapOK(c)
		}
		return encodeErr(err.Error())
	}
	if hinted && img == nil {
		if s.last == nil || s.lastDisp != d {
			return encodeErr("no baseline frame")
		}
		c.Flags = capFlagHinted | capFlagUnchang
		c.W, c.H = s.last.Rect.Dx(), s.last.Rect.Dy()
		return encodeCapOK(c)
	}
	if img == nil {
		return encodeErr("empty frame")
	}
	s.last, s.lastDisp = img, d
	c.W, c.H = img.Rect.Dx(), img.Rect.Dy()
	full := image.Rect(img.Rect.Min.X, img.Rect.Min.Y, img.Rect.Max.X, img.Rect.Max.Y)
	rects := []image.Rectangle{full}
	if hinted {
		c.Flags |= capFlagHinted
		rects = rects[:0]
		for _, r := range hint {
			if r = r.Canon().Intersect(full); !r.Empty() {
				rects = append(rects, r)
			}
		}
	}
	for _, r := range rects {
		c.Rects = append(c.Rects, r.Sub(img.Rect.Min))
		c.Pix = append(c.Pix, copyRect(img, r))
	}
	return encodeCapOK(c)
}

func (s *helperServer) input(p []byte) []byte {
	var req helperInputReq
	res := inputResult{State: protocol.InputStateError, Detail: "bad input request"}
	if json.Unmarshal(p, &req) == nil {
		res = s.b.Input(req.Msg, displayRect{x: req.Rect[0], y: req.Rect[1], w: req.Rect[2], h: req.Rect[3]})
	}
	out, _ := json.Marshal(helperInputResp{State: res.State, Code: res.Code, Detail: res.Detail})
	return out
}

func (s *helperServer) clip(p []byte) []byte {
	r := rbuf{b: p}
	op := r.u8()
	text := ""
	if op == clipOpSet {
		n := int(r.u32())
		text = string(r.take(n))
	}
	var b wbuf
	if r.err != nil {
		return encodeErr("bad clipboard request")
	}
	b.u8(0)
	switch op {
	case clipOpSeq:
		b.u32(s.b.ClipSeq())
	case clipOpGet:
		t, err := s.b.ClipGet()
		if err != nil {
			return encodeErr(err.Error())
		}
		b.u32(uint32(len(t)))
		b.WriteString(t)
	case clipOpSet:
		if err := s.b.ClipSet(text); err != nil {
			return encodeErr(err.Error())
		}
	default:
		return encodeErr("bad clipboard op")
	}
	return b.Bytes()
}
