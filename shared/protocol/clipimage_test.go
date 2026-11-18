package protocol

import (
	"bytes"
	"encoding/binary"
	"testing"
)

func fakePNG(w, h uint32, extra int) []byte {
	b := append([]byte{}, pngSig...)
	b = append(b, 0, 0, 0, 13)
	b = append(b, "IHDR"...)
	b = binary.BigEndian.AppendUint32(b, w)
	b = binary.BigEndian.AppendUint32(b, h)
	b = append(b, 8, 6, 0, 0, 0, 0, 0, 0, 0)
	return append(b, make([]byte, extra)...)
}

func TestClipImageInfo(t *testing.T) {
	if w, h, err := ClipImageInfo(fakePNG(10, 20, 0)); err != nil || w != 10 || h != 20 {
		t.Fatal(w, h, err)
	}
	for name, b := range map[string][]byte{
		"kisa":    {1, 2, 3},
		"imzasiz": append([]byte("xxxxxxxx"), make([]byte, 40)...),
		"sifir":   fakePNG(0, 5, 0),
		"genis":   fakePNG(MaxClipImageDim+1, 1, 0),
		"piksel":  fakePNG(8000, 8000, 0),
		"buyuk":   fakePNG(2, 2, MaxScreenClipboardImage),
		"ihdrsiz": append(append([]byte{}, pngSig...), make([]byte, 40)...),
	} {
		if ClipImageOK(b) {
			t.Errorf("%s kabul edilmemeli", name)
		}
	}
	if !ClipImageOK(fakePNG(2, 2, MaxScreenClipboardImage-33)) {
		t.Error("tam sinirdaki gorsel kabul edilmeli")
	}
}

func TestSplitAssemble(t *testing.T) {
	png := fakePNG(4, 4, ClipImageChunk*2+123)
	parts := SplitClipImage(png)
	if len(parts) != 3 {
		t.Fatalf("parca=%d", len(parts))
	}
	var a ClipImageAssembler
	var got []byte
	for i, p := range parts {
		out, done, err := a.Add(uint32(i), i == len(parts)-1, p)
		if err != nil || done != (i == len(parts)-1) {
			t.Fatal(i, done, err)
		}
		got = out
	}
	if !bytes.Equal(got, png) {
		t.Fatal("birlestirilen farkli")
	}
}

func TestAssemblerRejects(t *testing.T) {
	var a ClipImageAssembler
	png := fakePNG(4, 4, 10)
	if _, _, err := a.Add(0, false, png[:20]); err != nil {
		t.Fatal(err)
	}
	if _, _, err := a.Add(2, true, png[20:]); err == nil {
		t.Fatal("sira atlama reddedilmeli")
	}
	if _, _, err := a.Add(1, true, png[20:]); err == nil {
		t.Fatal("baslamamis aktarim reddedilmeli")
	}
	if out, done, err := a.Add(0, true, png); err != nil || !done || !bytes.Equal(out, png) {
		t.Fatal(done, err)
	}
	if _, _, err := a.Add(0, true, []byte("merhaba dunya merhaba dunya merhaba dunya")); err == nil {
		t.Fatal("png olmayan reddedilmeli")
	}
	big := make([]byte, ClipImageChunk)
	var err error
	for i := 0; i <= MaxScreenClipboardImage/ClipImageChunk+1 && err == nil; i++ {
		_, _, err = a.Add(uint32(i), false, big)
	}
	if err == nil {
		t.Fatal("toplam sinir asimi reddedilmeli")
	}
}

func TestScreenInfoValid(t *testing.T) {
	if !(ScreenInfo{Displays: 2, Display: 1}).Valid() || (ScreenInfo{Displays: 2, Display: 2}).Valid() || (ScreenInfo{}).Valid() {
		t.Fatal("ScreenInfo.Valid hatali")
	}
}
