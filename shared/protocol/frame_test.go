package protocol

import (
	"bytes"
	"errors"
	"math"
	"testing"
)

func TestBodyFrameRoundTrip(t *testing.T) {
	tests := []struct {
		name string
		in   BodyFrame
	}{
		{"istek govdesi", BodyFrame{FrameRequestBody, 1042, 0, false, []byte("merhaba")}},
		{"yanit govdesi EOF", BodyFrame{FrameResponseBody, 7, 3, true, []byte("son parca")}},
		{"bos payload", BodyFrame{FrameResponseBody, 1, 0, true, []byte{}}},
		{"maksimum degerler", BodyFrame{FrameRequestBody, math.MaxUint64, math.MaxUint32, true, []byte{0xFF, 0x00}}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := DecodeBodyFrame(tt.in.Encode())
			if err != nil {
				t.Fatalf("DecodeBodyFrame hata verdi: %v", err)
			}
			if got.FrameType != tt.in.FrameType {
				t.Errorf("FrameType = %d, beklenen %d", got.FrameType, tt.in.FrameType)
			}
			if got.ReqID != tt.in.ReqID {
				t.Errorf("ReqID = %d, beklenen %d", got.ReqID, tt.in.ReqID)
			}
			if got.Seq != tt.in.Seq {
				t.Errorf("Seq = %d, beklenen %d", got.Seq, tt.in.Seq)
			}
			if got.EOF != tt.in.EOF {
				t.Errorf("EOF = %v, beklenen %v", got.EOF, tt.in.EOF)
			}
			if !bytes.Equal(got.Payload, tt.in.Payload) {
				t.Errorf("Payload = %q, beklenen %q", got.Payload, tt.in.Payload)
			}
		})
	}
}

// Baslik duzeni kontratin bir parcasi: alan offsetleri kayarsa iki taraf
// birbirini anlamaz. Bu test tel formatini bayt bayt sabitler.
func TestEncodeWireLayout(t *testing.T) {
	f := BodyFrame{
		FrameType: FrameResponseBody, // 0x02
		ReqID:     0x0102030405060708,
		Seq:       0x090A0B0C,
		EOF:       true,
		Payload:   []byte{0xAA},
	}
	want := []byte{
		0x02,                                           // frameType
		0x01, 0x02, 0x03, 0x04, 0x05, 0x06, 0x07, 0x08, // reqID BE
		0x09, 0x0A, 0x0B, 0x0C, // seq BE
		0x01, // flags: EOF
		0xAA, // payload
	}
	if got := f.Encode(); !bytes.Equal(got, want) {
		t.Errorf("tel formati bozuldu:\n aldim   = % X\n beklenen= % X", got, want)
	}
}

func TestDecodeRejectsShortFrame(t *testing.T) {
	if _, err := DecodeBodyFrame(make([]byte, HeaderSize-1)); !errors.Is(err, ErrShortFrame) {
		t.Errorf("kisa cerceve icin ErrShortFrame bekleniyordu, alinan: %v", err)
	}
}

func TestDecodeRejectsUnknownType(t *testing.T) {
	bad := make([]byte, HeaderSize)
	bad[0] = 99
	if _, err := DecodeBodyFrame(bad); !errors.Is(err, ErrUnknownFrame) {
		t.Errorf("bilinmeyen tip icin ErrUnknownFrame bekleniyordu, alinan: %v", err)
	}
}

func TestPeekType(t *testing.T) {
	if got, err := PeekType([]byte(`{"type":"hello","client_version":"0.1.0"}`)); err != nil || got != TypeHello {
		t.Errorf("PeekType = %q, %v; beklenen %q, nil", got, err, TypeHello)
	}
	if _, err := PeekType([]byte(`{"client_version":"0.1.0"}`)); err == nil {
		t.Error("type alani olmayan mesaj icin hata bekleniyordu")
	}
}

func TestMarshalCatchesMissingType(t *testing.T) {
	// Type alani doldurulmamis mesaj sessizce gecmemeli.
	if _, err := Marshal(Hello{ClientVersion: "0.1.0"}, TypeHello); err == nil {
		t.Error("Type alani bos oldugunda Marshal hata vermeliydi")
	}
	if _, err := Marshal(Hello{Type: TypeHello, ClientVersion: "0.1.0"}, TypeHello); err != nil {
		t.Errorf("gecerli mesajda beklenmeyen hata: %v", err)
	}
}

// REGRESYON: Bir govde cercevesi WebSocket okuma sinirina sigmali.
//
// Gecmis hata: BodyChunkSize 32 KB idi ve HeaderSize (14) eklenince cerceve
// 32782 bayt oluyordu. coder/websocket'in VARSAYILAN okuma siniri 32768
// oldugu icin sunucu "message too big" ile baglantiyi kopariyordu; 32 KB'tan
// buyuk her yanit sessizce 0 bayt donuyordu. Birim testleri WS katmanini
// calistirmadigi icin bunu yakalayamamisti.
//
// Iki koruma birden: (a) cerceve WSReadLimit'e sigar, (b) WSReadLimit
// kutuphane varsayilaninin uzerindedir — yani SetReadLimit cagrisi ZORUNLUDUR.
func TestBodyFrameFitsReadLimit(t *testing.T) {
	const coderDefaultReadLimit = 32768

	frameSize := BodyChunkSize + HeaderSize
	if frameSize > WSReadLimit {
		t.Fatalf("cerceve (%d bayt) WSReadLimit'i (%d) asiyor", frameSize, WSReadLimit)
	}
	if WSReadLimit <= coderDefaultReadLimit {
		t.Fatalf("WSReadLimit (%d), kutuphane varsayilanindan (%d) buyuk olmali; "+
			"aksi halde SetReadLimit cagrisinin bir anlami kalmaz",
			WSReadLimit, coderDefaultReadLimit)
	}
	// Varsayilanin yetmedigini acikca belgele: SetReadLimit unutulursa kirilir.
	if frameSize <= coderDefaultReadLimit {
		t.Errorf("cerceve (%d) kutuphane varsayilanina (%d) siginca bu test "+
			"anlamsizlasir — BodyChunkSize kucultulduyse yorumu guncelleyin",
			frameSize, coderDefaultReadLimit)
	}

	// Gercek bir tam boy cerceve uret ve olc.
	full := BodyFrame{
		FrameType: FrameResponseBody,
		ReqID:     1,
		Payload:   make([]byte, BodyChunkSize),
	}.Encode()
	if len(full) != frameSize {
		t.Errorf("kodlanmis cerceve %d bayt, beklenen %d", len(full), frameSize)
	}
	if len(full) > WSReadLimit {
		t.Errorf("tam boy cerceve (%d) WSReadLimit'i (%d) asiyor", len(full), WSReadLimit)
	}
}
