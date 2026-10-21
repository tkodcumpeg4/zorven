package protocol

import (
	"encoding/binary"
	"errors"
	"fmt"
)

// Binary cerceve duzeni (api_contract.md §2, big-endian):
//
//	offset  boyut  alan
//	  0       1    frameType   1=request_body, 2=response_body
//	  1       8    reqID       uint64
//	  9       4    seq         uint32
//	 13       1    flags       bit0 = EOF
//	 14       -    payload
//
// Govde JSON+base64 yerine binary tasinir: base64 %33 sisme yaratir ve
// buyuk yanitlarin parca parca akitilmasini engeller.
const (
	HeaderSize = 14

	FrameRequestBody  uint8 = 1
	FrameResponseBody uint8 = 2
	// FrameWSData, yukseltilmis (upgraded) WebSocket baglantisinin ham
	// baytlarini tasir. Cift yonludur: ayni frame tipi hem tarayici->yerel
	// (sunucu->ajan) hem yerel->tarayici (ajan->sunucu) yonunde kullanilir;
	// yon gonderen tarafca bilinir. Akis kontrolu YOK (WS interaktiftir).
	FrameWSData uint8 = 3

	// FrameStreamData, ham TCP tunelinin (FAZ 3 / D2) baytlarini tasir. Cift
	// yonludur (ziyaretci<->yerel); yon gonderen tarafca bilinir. WS gibi akis
	// kontrolu YOK — ham TCP zaten kendi akis kontroluna sahiptir. ReqID = stream id.
	FrameStreamData uint8 = 4

	// FrameDatagram, tek bir UDP datagramini tasir (FAZ 3 / D2). Her cerceve TAM
	// bir datagramdir; sinir korunur, siralama/teslim garantisi yoktur. ReqID = flow id.
	FrameDatagram uint8 = 5

	// FrameScreen, screen2 uzak ekran karesi parcasini tasir (istemci -> sunucu).
	// ReqID = ekran akis kimligi (ScreenOpen.StreamID), Seq = kare numarasi,
	// bayraklar FlagEOF/FlagKeyframe/FlagFirst. Yuk duzeni: bkz. screen2.go ve
	// docs/screen2-protocol.md. Yalnizca FeatureScreen2 pazarlik edilmisse gonderilir.
	FrameScreen uint8 = 6

	// FrameFile, ekran oturumu dosya aktarimi parcasini tasir (cift yonlu).
	// ReqID = aktarim kimligi (FileOpen.XferID), Seq = parca sirasi. Yalnizca
	// FeatureScreenFiles pazarlanmissa gonderilir; akis kontrolu file_ack ile.
	FrameFile uint8 = 7

	FlagEOF uint8 = 1 << 0
	// FlagKeyframe (yalniz FrameScreen): kare tum ekrani tasir; alici once
	// onceki durumu atabilir. FlagFirst: kareyi olusturan ilk mesaj.
	FlagKeyframe uint8 = 1 << 1
	FlagFirst    uint8 = 1 << 2
	// Katman kimligi (yalniz FrameScreen, FeatureScreen2Layers): bayrak baytinin
	// bit3-4'u (0..3). Katmansiz akista 0.
	FlagLayerShift       = 3
	FlagLayerMask  uint8 = 0x3 << FlagLayerShift
)

var (
	ErrShortFrame   = errors.New("cerceve 14 baytlik basliktan kisa")
	ErrUnknownFrame = errors.New("bilinmeyen cerceve tipi")
)

// BodyFrame, tek bir govde parcasi.
type BodyFrame struct {
	FrameType uint8
	ReqID     uint64
	Seq       uint32
	EOF       bool
	Payload   []byte
}

// Encode, cerceveyi tel formatina cevirir.
// Payload kopyalanir; cagiran tamponu yeniden kullanabilir.
func (f BodyFrame) Encode() []byte {
	buf := make([]byte, HeaderSize+len(f.Payload))
	buf[0] = f.FrameType
	binary.BigEndian.PutUint64(buf[1:9], f.ReqID)
	binary.BigEndian.PutUint32(buf[9:13], f.Seq)
	if f.EOF {
		buf[13] |= FlagEOF
	}
	copy(buf[HeaderSize:], f.Payload)
	return buf
}

// DecodeBodyFrame, tel formatindan cerceveyi cozer.
// Donen Payload, girdi diliminin alt dilimidir (kopya degil) — cagiran
// tamponu saklayacaksa kendisi kopyalamalidir.
func DecodeBodyFrame(data []byte) (BodyFrame, error) {
	if len(data) < HeaderSize {
		return BodyFrame{}, fmt.Errorf("%w: %d bayt", ErrShortFrame, len(data))
	}
	ft := data[0]
	if ft != FrameRequestBody && ft != FrameResponseBody && ft != FrameWSData &&
		ft != FrameStreamData && ft != FrameDatagram && ft != FrameScreen && ft != FrameFile && ft != FrameWG {
		return BodyFrame{}, fmt.Errorf("%w: %d", ErrUnknownFrame, ft)
	}
	return BodyFrame{
		FrameType: ft,
		ReqID:     binary.BigEndian.Uint64(data[1:9]),
		Seq:       binary.BigEndian.Uint32(data[9:13]),
		EOF:       data[13]&FlagEOF != 0,
		Payload:   data[HeaderSize:],
	}, nil
}
