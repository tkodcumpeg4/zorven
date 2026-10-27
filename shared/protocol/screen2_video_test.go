package protocol

import (
	"bytes"
	"testing"
)

func TestVideoRoundTrip(t *testing.T) {
	cfg := VideoMessage{StreamID: 7, Seq: 3, Keyframe: true, Kind: VideoKindConfig, W: 1280, H: 720,
		Codec: "avc1.42e01f", Desc: []byte{1, 0x42, 0xe0, 0x1f, 0xff}}
	b, err := cfg.Encode()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := DecodeBodyFrame(b); err != nil {
		t.Fatalf("FrameVideo govde cercevesi olarak taninmadi: %v", err)
	}
	got, err := DecodeVideo(b)
	if err != nil || got.W != 1280 || got.H != 720 || got.Codec != cfg.Codec || !bytes.Equal(got.Desc, cfg.Desc) || !got.Keyframe || got.StreamID != 7 {
		t.Fatalf("config: %+v %v", got, err)
	}
	ch := VideoMessage{StreamID: 7, Seq: 4, Kind: VideoKindChunk, TimestampUs: 123456789, Data: []byte{0, 0, 0, 2, 0x65, 1}}
	b, _ = ch.Encode()
	got, err = DecodeVideo(b)
	if err != nil || got.TimestampUs != 123456789 || !bytes.Equal(got.Data, ch.Data) || got.Keyframe {
		t.Fatalf("chunk: %+v %v", got, err)
	}
	b, _ = VideoMessage{StreamID: 7, Kind: VideoKindStop}.Encode()
	if got, err = DecodeVideo(b); err != nil || got.Kind != VideoKindStop {
		t.Fatalf("stop: %+v %v", got, err)
	}
}

func TestVideoLimitsAndGarbage(t *testing.T) {
	if _, err := (VideoMessage{Kind: VideoKindChunk, Data: make([]byte, VideoMaxChunk+1)}).Encode(); err == nil {
		t.Fatal("asiri buyuk chunk kabul edildi")
	}
	if _, err := (VideoMessage{Kind: VideoKindChunk}).Encode(); err == nil {
		t.Fatal("bos chunk kabul edildi")
	}
	if _, err := (VideoMessage{Kind: VideoKindConfig, W: 0, H: 10, Codec: "x"}).Encode(); err == nil {
		t.Fatal("sifir boyut kabul edildi")
	}
	if _, err := (VideoMessage{Kind: 9}).Encode(); err == nil {
		t.Fatal("bilinmeyen tur kabul edildi")
	}
	if videoHeaderSize+8+VideoMaxChunk > WSReadLimit {
		t.Fatal("chunk WSReadLimit'i asiyor")
	}
	for _, bad := range [][]byte{nil, {FrameVideo}, make([]byte, 14), append(make([]byte, 14), 77)} {
		if _, err := DecodeVideo(bad); err == nil {
			t.Fatalf("bozuk mesaj kabul edildi: %v", bad)
		}
	}
	good, _ := VideoMessage{Kind: VideoKindConfig, W: 64, H: 64, Codec: "avc1.42e01f"}.Encode()
	for n := 15; n < len(good); n++ {
		if _, err := DecodeVideo(good[:n]); err == nil {
			t.Fatalf("kesik config kabul edildi (%d/%d)", n, len(good))
		}
	}
	if !ValidVideoMode("auto") || ValidVideoMode("x") {
		t.Fatal("ValidVideoMode")
	}
}
