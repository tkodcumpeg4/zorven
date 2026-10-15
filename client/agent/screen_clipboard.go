package agent

import (
	"context"
	"strings"
	"sync"
	"time"

	"github.com/tkodcumpeg4/zorven/shared/protocol"
)

// Metin panosu (iki yon). Platform erisimi clipboardBackend arkasinda
// (Windows: user32 pano API'leri; digerlerinde yok -> Hello'da ilan edilmez).
//
// GUVENLIK: pano icerigi parola olabilir. Izleme YALNIZCA sunucu bir kontrolcu
// izleyici oldugunu bildirince (screen_clipboard_watch) acilir; acilista
// panodaki MEVCUT icerik gonderilmez, yalniz sonraki degisiklikler. --no-clipboard
// ile hic ilan edilmez ve gelen mesajlar yok sayilir.

// clipboardBackend, platform pano erisimi.
type clipboardBackend interface {
	// Seq, pano her degistiginde artan sayac (Windows GetClipboardSequenceNumber).
	Seq() uint32
	// Get, metin panosunu okur ("" => metin yok).
	Get() (string, error)
	// Set, metin panosunu yazar.
	Set(text string) error
}

const clipboardPollInterval = 400 * time.Millisecond

// clipboardWatcher, pano degisimini kisip gonderen mantik (birim testli).
type clipboardWatcher struct {
	be   clipboardBackend
	send func(text string)

	mu      sync.Mutex
	enabled bool
	seen    uint32 // islenmis son sayac
	lastTxt string // son gonderilen/ayarlanan metin (yankiyi onler)
}

func newClipboardWatcher(be clipboardBackend, send func(string)) *clipboardWatcher {
	return &clipboardWatcher{be: be, send: send}
}

// SetEnabled, izlemeyi acar/kapatir. Acilista mevcut sayac "gorulmus" sayilir:
// eski pano icerigi sizmaz.
func (w *clipboardWatcher) SetEnabled(on bool) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if on && !w.enabled {
		w.seen = w.be.Seq()
	}
	w.enabled = on
	if !on {
		w.lastTxt = ""
	}
}

// Apply, panelden gelen metni panoya yazar. Yazma sonrasi sayac "gorulmus"
// isaretlenir; boylece yazdigimiz metin panele geri yansimaz.
func (w *clipboardWatcher) Apply(text string) error {
	if !protocol.ClipboardTextOK(text) {
		return errClipboardTooLarge
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	if err := w.be.Set(text); err != nil {
		return err
	}
	w.seen = w.be.Seq()
	w.lastTxt = normalizeClipboard(text)
	return nil
}

// Step, tek yoklama turu.
func (w *clipboardWatcher) Step() {
	w.mu.Lock()
	if !w.enabled {
		w.mu.Unlock()
		return
	}
	seq := w.be.Seq()
	if seq == w.seen {
		w.mu.Unlock()
		return
	}
	w.seen = seq
	txt, err := w.be.Get()
	if err != nil || txt == "" {
		w.mu.Unlock()
		return
	}
	txt = normalizeClipboard(txt)
	if len(txt) > protocol.MaxScreenClipboardSize || txt == w.lastTxt || !protocol.ClipboardTextOK(txt) {
		w.mu.Unlock()
		return
	}
	w.lastTxt = txt
	w.mu.Unlock()
	w.send(txt)
}

// Run, ctx bitene kadar yoklar.
func (w *clipboardWatcher) Run(ctx context.Context) {
	tk := time.NewTicker(clipboardPollInterval)
	defer tk.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-tk.C:
			w.Step()
		}
	}
}

// normalizeClipboard, satir sonlarini LF'ye indirir (panel tarafi icin).
func normalizeClipboard(s string) string {
	return strings.ReplaceAll(s, "\r\n", "\n")
}
