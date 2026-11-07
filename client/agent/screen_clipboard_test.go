package agent

import (
	"errors"
	"strings"
	"testing"

	"github.com/tkodcumpeg4/zorven/shared/protocol"
)

// flakyClip: ilk n Get cagrisi basarisiz olur.
type flakyClip struct {
	seq   uint32
	text  string
	fails int
}

func (f *flakyClip) Seq() uint32 { return f.seq }
func (f *flakyClip) Get() (string, error) {
	if f.fails > 0 {
		f.fails--
		return "", errors.New("pano baska uygulamada acik")
	}
	return f.text, nil
}
func (f *flakyClip) Set(t string) error { f.text = t; f.seq++; return nil }

func TestClipboardGetFailureRetriesNextRound(t *testing.T) {
	fc := &flakyClip{seq: 1}
	var sent []string
	w := newClipboardWatcher(fc, func(s string) { sent = append(sent, s) })
	w.SetEnabled(true)
	fc.seq, fc.text, fc.fails = 2, "kopya", 2
	w.Step()
	w.Step()
	if len(sent) != 0 {
		t.Fatalf("Get hatasinda gonderilmemeli: %v", sent)
	}
	w.Step() // 3. tur basarili
	if len(sent) != 1 || sent[0] != "kopya" {
		t.Fatalf("gecici hata sonrasi yeniden denenip gonderilmeli: %v", sent)
	}
}

func TestClipboardGetFailureGivesUp(t *testing.T) {
	fc := &flakyClip{seq: 1}
	n := 0
	w := newClipboardWatcher(fc, func(string) { n++ })
	w.SetEnabled(true)
	fc.seq, fc.text, fc.fails = 2, "x", 1000
	for i := 0; i < clipboardGetRetries+3; i++ {
		w.Step()
	}
	fc.fails = 0
	w.Step()
	if n != 0 {
		t.Fatal("vazgecilen surum gonderilmemeli (sonsuz dongu yok)")
	}
}

func TestClipboardTooLargeNotifies(t *testing.T) {
	fc := &flakyClip{seq: 1}
	var sent []string
	var notices []string
	w := newClipboardWatcher(fc, func(s string) { sent = append(sent, s) })
	w.notice = func(d string) { notices = append(notices, d) }
	w.SetEnabled(true)
	fc.seq, fc.text = 2, strings.Repeat("a", protocol.MaxScreenClipboardSize+1)
	w.Step()
	if len(sent) != 0 || len(notices) != 1 || notices[0] != protocol.DegradedClipboardTooLarge {
		t.Fatalf("sent=%v notices=%v", len(sent), notices)
	}
	w.Step() // ayni surum tekrar bildirilmemeli
	if len(notices) != 1 {
		t.Fatalf("tekrar bildirim: %v", notices)
	}
}
