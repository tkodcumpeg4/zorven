package agent

import (
	"bytes"
	"errors"
	"strings"
	"testing"

	"github.com/tkodcumpeg4/zorven/shared/protocol"
)

// imgClip: metin + gorsel panosu sahtesi.
type imgClip struct {
	flakyClip
	png      []byte
	imgErr   error
	getCalls int
}

func (f *imgClip) GetImage() ([]byte, error) {
	f.getCalls++
	return f.png, f.imgErr
}
func (f *imgClip) SetImage(p []byte) error { f.png = p; f.seq++; return nil }

type imgSink struct {
	texts []string
	pngs  [][]byte
	notes []string
}

func newImgWatcher(t *testing.T, fc *imgClip) (*clipboardWatcher, *imgSink) {
	t.Helper()
	s := &imgSink{}
	w := newClipboardWatcher(fc, func(x string) { s.texts = append(s.texts, x) })
	w.notice = func(d string) { s.notes = append(s.notes, d) }
	w.enableImages(func(p []byte) { s.pngs = append(s.pngs, p) })
	if !w.imagesEnabled() {
		t.Fatal("gorsel esitleme acilmadi")
	}
	w.SetEnabled(true)
	return w, s
}

func TestClipboardImageSent(t *testing.T) {
	fc := &imgClip{flakyClip: flakyClip{seq: 1}}
	w, s := newImgWatcher(t, fc)
	png := encPNG(t, testImage(6, 4, true))
	fc.seq, fc.png = 2, png
	w.Step()
	if len(s.pngs) != 1 || !bytes.Equal(s.pngs[0], png) || len(s.texts) != 0 {
		t.Fatalf("pngs=%d texts=%v", len(s.pngs), s.texts)
	}
	// Ayni icerik, yeni surum: tekrar gonderilmez.
	fc.seq = 3
	w.Step()
	if len(s.pngs) != 1 {
		t.Fatalf("ayni gorsel tekrar gonderildi: %d", len(s.pngs))
	}
	// Farkli gorsel: gider.
	fc.seq, fc.png = 4, encPNG(t, testImage(3, 3, false))
	w.Step()
	if len(s.pngs) != 2 {
		t.Fatalf("yeni gorsel gitmedi: %d", len(s.pngs))
	}
}

func TestClipboardTextAndImageBoth(t *testing.T) {
	fc := &imgClip{flakyClip: flakyClip{seq: 1}}
	w, s := newImgWatcher(t, fc)
	fc.seq, fc.text, fc.png = 2, "baslik\r\nmetin", encPNG(t, testImage(2, 2, false))
	w.Step()
	if len(s.texts) != 1 || s.texts[0] != "baslik\nmetin" || len(s.pngs) != 1 {
		t.Fatalf("ikisi de gitmeli: %v %d", s.texts, len(s.pngs))
	}
}

func TestClipboardApplyImageNoEcho(t *testing.T) {
	fc := &imgClip{flakyClip: flakyClip{seq: 1}}
	w, s := newImgWatcher(t, fc)
	png := encPNG(t, testImage(5, 5, true))
	if err := w.ApplyImage(png); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(fc.png, png) {
		t.Fatal("pano gorseli yazilmadi")
	}
	w.Step()
	if len(s.pngs) != 0 || len(s.texts) != 0 {
		t.Fatalf("yazilan gorsel panele yansidi: %d", len(s.pngs))
	}
	// Disaridan gelen degisiklik hala gonderilir.
	fc.seq, fc.png = fc.seq+1, encPNG(t, testImage(4, 4, false))
	w.Step()
	if len(s.pngs) != 1 {
		t.Fatalf("sonraki degisiklik gitmeli: %d", len(s.pngs))
	}
}

func TestClipboardApplyImageValidation(t *testing.T) {
	fc := &imgClip{flakyClip: flakyClip{seq: 1}}
	w, _ := newImgWatcher(t, fc)
	if err := w.ApplyImage([]byte("png degil")); err == nil || fc.png != nil {
		t.Fatalf("gecersiz gorsel panoya yazilmamali: %v", err)
	}
	if err := w.ApplyImage(make([]byte, protocol.MaxScreenClipboardImage+1)); err == nil || fc.png != nil {
		t.Fatalf("buyuk gorsel reddedilmeli: %v", err)
	}
}

func TestClipboardImagesOffByDefault(t *testing.T) {
	fc := &imgClip{flakyClip: flakyClip{seq: 1}}
	var sent []string
	w := newClipboardWatcher(fc, func(x string) { sent = append(sent, x) })
	w.SetEnabled(true) // enableImages CAGRILMADI (bayrak pazarlanmadi)
	fc.seq, fc.png, fc.text = 2, encPNG(t, testImage(2, 2, false)), "x"
	w.Step()
	if fc.getCalls != 0 {
		t.Fatal("bayrak yokken GetImage cagrilmamali")
	}
	if len(sent) != 1 {
		t.Fatalf("metin yine gitmeli: %v", sent)
	}
	if err := w.ApplyImage(encPNG(t, testImage(2, 2, false))); err == nil {
		t.Fatal("bayrak yokken gorsel yazilmamali")
	}
}

func TestClipboardImagePermanentErrors(t *testing.T) {
	fc := &imgClip{flakyClip: flakyClip{seq: 1}}
	w, s := newImgWatcher(t, fc)
	fc.seq, fc.text, fc.imgErr = 2, "metin", errClipImageTooLarge
	w.Step()
	if len(s.texts) != 1 || len(s.pngs) != 0 || len(s.notes) != 1 || s.notes[0] != protocol.DegradedClipboardTooLarge {
		t.Fatalf("texts=%v pngs=%d notes=%v", s.texts, len(s.pngs), s.notes)
	}
	w.Step() // ayni surum tekrar denenmez
	if fc.getCalls != 1 {
		t.Fatalf("kalici hata yeniden denendi: %d", fc.getCalls)
	}
	fc.seq, fc.text, fc.imgErr = 3, "baska", errClipImageInvalid
	w.Step()
	if len(s.notes) != 1 || len(s.texts) != 2 {
		t.Fatalf("invalid sessiz gecilmeli: notes=%v texts=%v", s.notes, s.texts)
	}
}

func TestClipboardImageBusyRetries(t *testing.T) {
	fc := &imgClip{flakyClip: flakyClip{seq: 1}}
	w, s := newImgWatcher(t, fc)
	fc.seq, fc.png, fc.imgErr = 2, encPNG(t, testImage(2, 2, false)), errors.New("pano baska bir uygulamada acik")
	w.Step()
	w.Step()
	if len(s.pngs) != 0 {
		t.Fatal("gecici hatada gonderilmemeli")
	}
	fc.imgErr = nil
	w.Step()
	if len(s.pngs) != 1 {
		t.Fatalf("yeniden denemede gitmeli: %d", len(s.pngs))
	}
}

func TestClipboardDisableForgetsImage(t *testing.T) {
	fc := &imgClip{flakyClip: flakyClip{seq: 1}}
	w, s := newImgWatcher(t, fc)
	png := encPNG(t, testImage(2, 2, false))
	fc.seq, fc.png = 2, png
	w.Step()
	w.SetEnabled(false)
	w.SetEnabled(true) // acilista mevcut icerik sizmaz
	fc.seq = 2
	w.Step()
	if len(s.pngs) != 1 {
		t.Fatalf("eski icerik sizdi: %d", len(s.pngs))
	}
	_ = strings.TrimSpace
}
