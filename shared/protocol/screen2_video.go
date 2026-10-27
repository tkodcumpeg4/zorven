package protocol

import (
	"encoding/binary"
	"errors"
	"fmt"
)

// H.264 video modu cerceveleri (FrameVideo, FeatureScreen2Video).
//
//	[0]      frameType = FrameVideo (9)
//	[1:9]    streamID  uint64
//	[9:13]   seq       uint32 (kare numarasi)
//	[13]     flags     bit1 keyframe (IDR)
//	[14]     kind      1 config | 2 chunk | 3 stop
//
//	config: w uint16, h uint16, codecLen uint8, codec (or. "avc1.42e01f"),
//	        description (avcC kaydi; kalan baytlar)
//	chunk : timestamp_us uint64, veri (AVCC: 4 bayt uzunluk onekli NAL'lar; SPS/PPS
//	        icermez, onlar config'te) -- WebCodecs EncodedVideoChunk.data
//	stop  : (bos) video durdu, akis karo moduna doner
//
// Her IDR'den once config tekrar gonderilir; boylece sonradan katilan
// izleyici yalniz sonraki IDR ile cozmeye baslayabilir.
const (
	VideoKindConfig uint8 = 1
	VideoKindChunk  uint8 = 2
	VideoKindStop   uint8 = 3

	// VideoMaxChunk, tek chunk verisinin ust siniri (WSReadLimit'in altinda kalir).
	VideoMaxChunk = 900 << 10
	// VideoMaxDesc / VideoMaxCodec, config alanlari siniri.
	VideoMaxDesc  = 2048
	VideoMaxCodec = 32
	// VideoMaxDim, kare boyutu ust siniri.
	VideoMaxDim = 8192

	videoHeaderSize = HeaderSize + 1
)

var (
	ErrVideoShort = errors.New("video: mesaj kisa")
	ErrVideoKind  = errors.New("video: bilinmeyen tur")
	ErrVideoSize  = errors.New("video: boyut siniri asildi")
)

// VideoMessage, cozulmus bir video cercevesi.
type VideoMessage struct {
	StreamID uint64
	Seq      uint32
	Keyframe bool
	Kind     uint8
	// config
	W, H  uint16
	Codec string
	Desc  []byte
	// chunk
	TimestampUs uint64
	Data        []byte
}

// Encode, mesaji tel formatina cevirir.
func (m VideoMessage) Encode() ([]byte, error) {
	var body []byte
	switch m.Kind {
	case VideoKindConfig:
		if m.W == 0 || m.H == 0 || int(m.W) > VideoMaxDim || int(m.H) > VideoMaxDim {
			return nil, fmt.Errorf("%w: boyut %dx%d", ErrVideoSize, m.W, m.H)
		}
		if len(m.Codec) == 0 || len(m.Codec) > VideoMaxCodec || len(m.Desc) > VideoMaxDesc {
			return nil, fmt.Errorf("%w: codec/description", ErrVideoSize)
		}
		body = make([]byte, 0, 5+len(m.Codec)+len(m.Desc))
		body = binary.BigEndian.AppendUint16(body, m.W)
		body = binary.BigEndian.AppendUint16(body, m.H)
		body = append(body, byte(len(m.Codec)))
		body = append(body, m.Codec...)
		body = append(body, m.Desc...)
	case VideoKindChunk:
		if len(m.Data) == 0 || len(m.Data) > VideoMaxChunk {
			return nil, fmt.Errorf("%w: chunk %d bayt", ErrVideoSize, len(m.Data))
		}
		body = make([]byte, 0, 8+len(m.Data))
		body = binary.BigEndian.AppendUint64(body, m.TimestampUs)
		body = append(body, m.Data...)
	case VideoKindStop:
	default:
		return nil, ErrVideoKind
	}
	buf := make([]byte, videoHeaderSize, videoHeaderSize+len(body))
	buf[0] = FrameVideo
	binary.BigEndian.PutUint64(buf[1:9], m.StreamID)
	binary.BigEndian.PutUint32(buf[9:13], m.Seq)
	if m.Keyframe {
		buf[13] |= FlagKeyframe
	}
	buf[14] = m.Kind
	return append(buf, body...), nil
}

// DecodeVideo, FrameVideo mesajini cozer ve dogrular. Data/Desc girdinin alt
// dilimidir (kopya degil).
func DecodeVideo(b []byte) (VideoMessage, error) {
	if len(b) < videoHeaderSize {
		return VideoMessage{}, ErrVideoShort
	}
	if b[0] != FrameVideo {
		return VideoMessage{}, ErrUnknownFrame
	}
	m := VideoMessage{
		StreamID: binary.BigEndian.Uint64(b[1:9]),
		Seq:      binary.BigEndian.Uint32(b[9:13]),
		Keyframe: b[13]&FlagKeyframe != 0,
		Kind:     b[14],
	}
	body := b[videoHeaderSize:]
	switch m.Kind {
	case VideoKindConfig:
		if len(body) < 5 {
			return m, ErrVideoShort
		}
		m.W = binary.BigEndian.Uint16(body[0:2])
		m.H = binary.BigEndian.Uint16(body[2:4])
		cl := int(body[4])
		if m.W == 0 || m.H == 0 || int(m.W) > VideoMaxDim || int(m.H) > VideoMaxDim {
			return m, fmt.Errorf("%w: boyut %dx%d", ErrVideoSize, m.W, m.H)
		}
		if cl == 0 || cl > VideoMaxCodec || len(body) < 5+cl {
			return m, ErrVideoShort
		}
		m.Codec = string(body[5 : 5+cl])
		m.Desc = body[5+cl:]
		if len(m.Desc) > VideoMaxDesc {
			return m, ErrVideoSize
		}
	case VideoKindChunk:
		if len(body) < 9 {
			return m, ErrVideoShort
		}
		if len(body)-8 > VideoMaxChunk {
			return m, ErrVideoSize
		}
		m.TimestampUs = binary.BigEndian.Uint64(body[0:8])
		m.Data = body[8:]
	case VideoKindStop:
	default:
		return m, ErrVideoKind
	}
	return m, nil
}
