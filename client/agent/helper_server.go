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

	// lastImg: monitor basina son TAM goruntu (tum oturumlar paylasir).
	lastImg map[int]*image.RGBA
	// sess: istemci oturumu basina kirli bolge birikimi. Yakalayicinin (DXGI)
	// ipucu "onceki yakalamadan beri" anlamindadir; birden cok oturum ayni
	// yakalayiciyi cektiginde bir oturumun tukettigi ipucu digerlerine
	// KAYBOLMASIN diye her oturum kendi birikimini tutar.
	sess map[uint32]*helperSessState
}

// helperSessState, bir istemci oturumunun helper tarafindaki durumu.
type helperSessState struct {
	disp    int
	full    bool              // oturumun canvas'i taban gerektiriyor: tam kare gonder
	pending []image.Rectangle // gonderilmemis degisen bolgeler (kaynak koordinati)
}

// helperPendingMax: birikim bu sayiyi asarsa tam kareye dus.
const helperPendingMax = 2048

// Cap istegi bayraklari (hmCapReq, oturum anahtarindan sonra).
const capReqResync byte = 1 << 0 // oturumun canvas'i bozuldu: tam kare gonder

func serveHelper(rw io.ReadWriter, b helperBackend, onActivity func()) error {
	s := &helperServer{b: b, onActivity: onActivity,
		lastImg: map[int]*image.RGBA{}, sess: map[uint32]*helperSessState{}}
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
			// Eski servis yalniz monitoru yollar: anahtar 0, bayrak yok.
			var key uint32
			var flags byte
			if r.err == nil && len(r.b) >= 5 {
				key, flags = r.u32(), r.u8()
			}
			respTyp, resp = hmCapResp, s.capture(d, key, flags, r.err)
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

func (s *helperServer) capture(d int, key uint32, flags byte, perr error) []byte {
	if perr != nil {
		return encodeErr("bad request")
	}
	st := s.sess[key]
	if st == nil {
		if len(s.sess) > 64 { // sizinti korumasi
			s.sess = map[uint32]*helperSessState{}
		}
		st = &helperSessState{disp: d, full: true}
		s.sess[key] = st
	}
	if flags&capReqResync != 0 || st.disp != d {
		st.disp, st.full, st.pending = d, true, nil
	}

	img, hint, hinted, err := s.b.CaptureHinted(d)
	if err != nil {
		// Masaustu degismis olabilir (UAC, kilit): yeniden baglan ve bir kez daha dene.
		s.b.Attach()
		img, hint, hinted, err = s.b.CaptureHinted(d)
	}
	c := capResp{Bounds: s.b.Bounds(d), Displays: s.b.NumDisplays(), Method: s.b.Method()}
	degraded := false
	switch {
	case err != nil:
		// Guvenli masaustu (UAC/kilit ekrani) kullanici oturumundan yakalanamaz:
		// son kare tekrarlanir (degraded bayragi); hic kare yoksa hata.
		if s.lastImg[d] == nil {
			return encodeErr(err.Error())
		}
		degraded = true
	case hinted && img == nil:
		if s.lastImg[d] == nil {
			return encodeErr("no baseline frame")
		}
	case img == nil:
		return encodeErr("empty frame")
	default:
		s.lastImg[d] = img
		// Bu monitoru izleyen TUM oturumlarin birikimine ekle.
		for _, o := range s.sess {
			if o.disp != d || o.full {
				continue
			}
			if !hinted {
				o.full, o.pending = true, nil
				continue
			}
			o.pending = append(o.pending, hint...)
			if len(o.pending) > helperPendingMax {
				o.full, o.pending = true, nil
			}
		}
		if !hinted { // st de dahil (yukarida full oldu)
			st.full = true
		}
	}

	last := s.lastImg[d]
	c.W, c.H = last.Rect.Dx(), last.Rect.Dy()
	if degraded {
		c.Flags |= capFlagDegraded
	}
	full := image.Rect(last.Rect.Min.X, last.Rect.Min.Y, last.Rect.Max.X, last.Rect.Max.Y)
	if st.full {
		st.full, st.pending = false, nil
		c.Rects = []image.Rectangle{full.Sub(last.Rect.Min)}
		c.Pix = [][]byte{copyRect(last, full)}
		return encodeCapOK(c) // hintsiz tam kare
	}
	c.Flags |= capFlagHinted
	var rects []image.Rectangle
	for _, r := range st.pending {
		if r = r.Canon().Intersect(full); !r.Empty() {
			rects = append(rects, r)
		}
	}
	st.pending = nil
	if len(rects) == 0 {
		c.Flags |= capFlagUnchang
		return encodeCapOK(c)
	}
	for _, r := range rects {
		c.Rects = append(c.Rects, r.Sub(last.Rect.Min))
		c.Pix = append(c.Pix, copyRect(last, r))
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
