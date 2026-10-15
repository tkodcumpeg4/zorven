package protocol

import (
	"encoding/binary"
	"errors"
	"fmt"
)

// screen2 ikili ekran kare formati (surum 1). Ayrintili tablo:
// docs/screen2-protocol.md. Hepsi big-endian.
//
//	[0]      frameType = FrameScreen (6)
//	[1:9]    streamID  uint64 (ScreenOpen.StreamID)
//	[9:13]   seq       uint32 (kare numarasi; ayni karenin parcalari ayni seq)
//	[13]     flags     bit0 EOF (karenin son mesaji), bit1 keyframe, bit2 first
//	[14]     version   uint8 = 1
//	[15:17]  screenW   uint16 (kare tuvalinin genisligi; olceklenmis boyut)
//	[17:19]  screenH   uint16
//	[19:21]  nrects    uint16
//	[21:]    nrects x { x,y,w,h uint16; enc uint8; len uint32; payload }
const (
	Screen2Version = 1

	// Screen2HeaderSize, BodyFrame basligi (14) + screen2 basligi (7).
	Screen2HeaderSize = HeaderSize + 7
	// Screen2RectHeaderSize, dikdortgen basligi: x,y,w,h (8) + enc (1) + len (4).
	Screen2RectHeaderSize = 13

	// Screen2TargetMessage, bir mesajin hedef boyutu; asilirsa yeni mesaja bolunur.
	Screen2TargetMessage = 256 << 10
	// Screen2MaxMessage, kabul edilen mesajin mutlak ust siniri.
	Screen2MaxMessage = 2 << 20
	// Screen2MaxRectPayload, tek dikdortgen yukunun ust siniri.
	Screen2MaxRectPayload = 1 << 20
	// Screen2MaxRects, mesaj basina en fazla dikdortgen.
	Screen2MaxRects = 4096
)

// Dikdortgen kodlamalari.
const (
	Screen2EncJPEG  uint8 = 1 // yuk: JPEG
	Screen2EncPNG   uint8 = 2 // yuk: PNG
	Screen2EncSolid uint8 = 3 // yuk: 4 bayt RGBA, dikdortgeni duz renkle doldur
	// 4 (copy-from-previous) bilerek tanimsiz: ileri surum icin ayrildi.
)

var (
	ErrScreen2Short   = errors.New("screen2: mesaj kisa")
	ErrScreen2Version = errors.New("screen2: desteklenmeyen surum")
	ErrScreen2Bounds  = errors.New("screen2: dikdortgen ekran disinda")
	ErrScreen2Size    = errors.New("screen2: boyut siniri asildi")
	ErrScreen2Enc     = errors.New("screen2: gecersiz kodlama/yuk")
)

// Screen2Rect, ekranin bir dikdortgen yamasi.
type Screen2Rect struct {
	X, Y, W, H uint16
	Enc        uint8
	Payload    []byte
}

// Screen2Message, cozulmus bir screen2 mesaji.
type Screen2Message struct {
	StreamID uint64
	Seq      uint32
	EOF      bool
	Keyframe bool
	First    bool
	Layer    uint8  // katman kimligi (bayrak bit3-4); katmansiz akista 0
	W, H     uint16 // tuval boyutu
	Rects    []Screen2Rect
}

func (r Screen2Rect) wireSize() int { return Screen2RectHeaderSize + len(r.Payload) }

// validate, tek dikdortgenin bicim ve sinir kontrolu.
func (r Screen2Rect) validate(sw, sh uint16) error {
	if r.W == 0 || r.H == 0 {
		return fmt.Errorf("%w: bos dikdortgen", ErrScreen2Bounds)
	}
	if int(r.X)+int(r.W) > int(sw) || int(r.Y)+int(r.H) > int(sh) {
		return fmt.Errorf("%w: %d,%d %dx%d / %dx%d", ErrScreen2Bounds, r.X, r.Y, r.W, r.H, sw, sh)
	}
	if len(r.Payload) > Screen2MaxRectPayload {
		return fmt.Errorf("%w: yuk %d bayt", ErrScreen2Size, len(r.Payload))
	}
	switch r.Enc {
	case Screen2EncJPEG, Screen2EncPNG:
		if len(r.Payload) == 0 {
			return fmt.Errorf("%w: bos yuk", ErrScreen2Enc)
		}
	case Screen2EncSolid:
		if len(r.Payload) != 4 {
			return fmt.Errorf("%w: duz renk yuku 4 bayt olmali", ErrScreen2Enc)
		}
	default:
		return fmt.Errorf("%w: bilinmeyen kodlama %d", ErrScreen2Enc, r.Enc)
	}
	return nil
}

// Encode, mesaji tel formatina cevirir. Sinirlari asan/gecersiz icerik hata verir.
func (m Screen2Message) Encode() ([]byte, error) {
	if m.W == 0 || m.H == 0 {
		return nil, fmt.Errorf("%w: tuval boyutu 0", ErrScreen2Bounds)
	}
	if len(m.Rects) > Screen2MaxRects {
		return nil, fmt.Errorf("%w: %d dikdortgen", ErrScreen2Size, len(m.Rects))
	}
	size := Screen2HeaderSize
	for _, r := range m.Rects {
		if err := r.validate(m.W, m.H); err != nil {
			return nil, err
		}
		size += r.wireSize()
	}
	if size > Screen2MaxMessage {
		return nil, fmt.Errorf("%w: mesaj %d bayt", ErrScreen2Size, size)
	}
	buf := make([]byte, size)
	buf[0] = FrameScreen
	binary.BigEndian.PutUint64(buf[1:9], m.StreamID)
	binary.BigEndian.PutUint32(buf[9:13], m.Seq)
	var fl uint8
	if m.EOF {
		fl |= FlagEOF
	}
	if m.Keyframe {
		fl |= FlagKeyframe
	}
	if m.First {
		fl |= FlagFirst
	}
	fl |= (m.Layer << FlagLayerShift) & FlagLayerMask
	buf[13] = fl
	buf[14] = Screen2Version
	binary.BigEndian.PutUint16(buf[15:17], m.W)
	binary.BigEndian.PutUint16(buf[17:19], m.H)
	binary.BigEndian.PutUint16(buf[19:21], uint16(len(m.Rects)))
	o := Screen2HeaderSize
	for _, r := range m.Rects {
		binary.BigEndian.PutUint16(buf[o:], r.X)
		binary.BigEndian.PutUint16(buf[o+2:], r.Y)
		binary.BigEndian.PutUint16(buf[o+4:], r.W)
		binary.BigEndian.PutUint16(buf[o+6:], r.H)
		buf[o+8] = r.Enc
		binary.BigEndian.PutUint32(buf[o+9:], uint32(len(r.Payload)))
		o += Screen2RectHeaderSize
		o += copy(buf[o:], r.Payload)
	}
	return buf, nil
}

// DecodeScreen2 tel formatindan mesaji cozer ve tum sinirlari dogrular.
// Donen Payload dilimleri girdinin alt dilimidir (kopya degil).
func DecodeScreen2(data []byte) (Screen2Message, error) {
	var m Screen2Message
	if len(data) < Screen2HeaderSize {
		return m, fmt.Errorf("%w: %d bayt", ErrScreen2Short, len(data))
	}
	if len(data) > Screen2MaxMessage {
		return m, fmt.Errorf("%w: mesaj %d bayt", ErrScreen2Size, len(data))
	}
	if data[0] != FrameScreen {
		return m, fmt.Errorf("%w: cerceve tipi %d", ErrUnknownFrame, data[0])
	}
	if data[14] != Screen2Version {
		return m, fmt.Errorf("%w: %d", ErrScreen2Version, data[14])
	}
	m.StreamID = binary.BigEndian.Uint64(data[1:9])
	m.Seq = binary.BigEndian.Uint32(data[9:13])
	fl := data[13]
	m.EOF = fl&FlagEOF != 0
	m.Keyframe = fl&FlagKeyframe != 0
	m.First = fl&FlagFirst != 0
	m.Layer = (fl & FlagLayerMask) >> FlagLayerShift
	m.W = binary.BigEndian.Uint16(data[15:17])
	m.H = binary.BigEndian.Uint16(data[17:19])
	if m.W == 0 || m.H == 0 {
		return m, fmt.Errorf("%w: tuval boyutu 0", ErrScreen2Bounds)
	}
	n := int(binary.BigEndian.Uint16(data[19:21]))
	if n > Screen2MaxRects {
		return m, fmt.Errorf("%w: %d dikdortgen", ErrScreen2Size, n)
	}
	// Her dikdortgen en az baslik kadar yer kaplar: sahte buyuk n ile bellek
	// ayirtmayi engeller.
	if n*Screen2RectHeaderSize > len(data)-Screen2HeaderSize {
		return m, fmt.Errorf("%w: dikdortgen sayisi mesaja sigmiyor", ErrScreen2Short)
	}
	m.Rects = make([]Screen2Rect, 0, n)
	o := Screen2HeaderSize
	for i := 0; i < n; i++ {
		if len(data)-o < Screen2RectHeaderSize {
			return m, fmt.Errorf("%w: dikdortgen %d basligi", ErrScreen2Short, i)
		}
		r := Screen2Rect{
			X:   binary.BigEndian.Uint16(data[o:]),
			Y:   binary.BigEndian.Uint16(data[o+2:]),
			W:   binary.BigEndian.Uint16(data[o+4:]),
			H:   binary.BigEndian.Uint16(data[o+6:]),
			Enc: data[o+8],
		}
		l := binary.BigEndian.Uint32(data[o+9:])
		o += Screen2RectHeaderSize
		if l > Screen2MaxRectPayload {
			return m, fmt.Errorf("%w: yuk %d bayt", ErrScreen2Size, l)
		}
		if uint64(l) > uint64(len(data)-o) {
			return m, fmt.Errorf("%w: dikdortgen %d yuku", ErrScreen2Short, i)
		}
		r.Payload = data[o : o+int(l) : o+int(l)]
		o += int(l)
		if err := r.validate(m.W, m.H); err != nil {
			return m, err
		}
		m.Rects = append(m.Rects, r)
	}
	if o != len(data) {
		return m, fmt.Errorf("%w: %d fazla bayt", ErrScreen2Size, len(data)-o)
	}
	return m, nil
}

// PackScreen2 bir kareyi (dikdortgen listesi) hedef boyutu asmayan mesajlara
// boler. Ilk mesajda FlagFirst, sonda FlagEOF vardir; keyframe tum mesajlarda
// isaretlidir. Tek dikdortgen target'i asabilir (bolunmez); sinir
// Screen2MaxRectPayload'dur. Rect listesi bos ise yine tek (bos) mesaj doner
// (kalp atisi). Bolunme sirasi dikdortgen sirasini korur.
func PackScreen2(streamID uint64, seq uint32, keyframe bool, w, h uint16, rects []Screen2Rect, target int) ([][]byte, error) {
	return PackScreen2Layer(streamID, seq, keyframe, 0, w, h, rects, target)
}

// PackScreen2Layer, PackScreen2 gibidir; tum mesajlara katman kimligini yazar.
func PackScreen2Layer(streamID uint64, seq uint32, keyframe bool, layer uint8, w, h uint16, rects []Screen2Rect, target int) ([][]byte, error) {
	if target <= 0 {
		target = Screen2TargetMessage
	}
	var out [][]byte
	flush := func(part []Screen2Rect) error {
		b, err := Screen2Message{
			StreamID: streamID, Seq: seq, Keyframe: keyframe,
			First: len(out) == 0, Layer: layer, W: w, H: h, Rects: part,
		}.Encode()
		if err != nil {
			return err
		}
		out = append(out, b)
		return nil
	}
	start, size := 0, Screen2HeaderSize
	for i, r := range rects {
		rs := r.wireSize()
		if i > start && (size+rs > target || i-start >= Screen2MaxRects) {
			if err := flush(rects[start:i]); err != nil {
				return nil, err
			}
			start, size = i, Screen2HeaderSize
		}
		size += rs
	}
	if err := flush(rects[start:]); err != nil {
		return nil, err
	}
	out[len(out)-1][13] |= FlagEOF
	return out, nil
}
