package agent

import (
	"context"
	"crypto/sha256"
	"errors"
	"strings"
	"sync"
	"time"

	"github.com/tkodcumpeg4/zorven/shared/protocol"
)

// Pano (iki yon): metin her platformda, gorsel (PNG) yalniz Windows'ta.
// Platform erisimi clipboardBackend arkasinda (Windows: user32 pano API'leri;
// macOS/Linux: pbcopy/pbpaste, xclip/xsel, wl-clipboard alt surecleri; bulunamazsa
// Hello'da ilan edilmez).
//
// GUVENLIK: pano icerigi parola olabilir. Izleme YALNIZCA sunucu bir kontrolcu
// izleyici oldugunu bildirince (screen_clipboard_watch) acilir; acilista
// panodaki MEVCUT icerik gonderilmez, yalniz sonraki degisiklikler. --no-clipboard
// ile hic ilan edilmez ve gelen mesajlar yok sayilir.

// clipboardBackend, platform pano erisimi.
type clipboardBackend interface {
	// Seq, pano her degistiginde artan sayac (Windows GetClipboardSequenceNumber;
	// digerlerinde icerik ozeti degisimi).
	Seq() uint32
	// Get, metin panosunu okur ("" => metin yok).
	Get() (string, error)
	// Set, metin panosunu yazar.
	Set(text string) error
}

// clipboardImageBackend, gorsel panosu (yalniz Windows). Sinirda PNG konusur:
// DIB/"PNG" bicimi donusumu backend'in isidir (screen_clipboard_dib.go).
type clipboardImageBackend interface {
	// GetImage, panodaki gorseli PNG olarak doner; gorsel yoksa (nil, nil).
	// errClipImageInvalid/errClipImageTooLarge kalici hatadir (yeniden denenmez).
	GetImage() ([]byte, error)
	// SetImage, PNG'yi panoya yazar (DIB + "PNG" bicimi birlikte).
	SetImage(png []byte) error
}

// pollIntervaler, varsayilandan farkli yoklama araligi isteyen backend'ler icin.
type pollIntervaler interface{ pollInterval() time.Duration }

const clipboardPollInterval = 400 * time.Millisecond

// clipboardWatcher, pano degisimini kisip gonderen mantik (birim testli).
type clipboardWatcher struct {
	be   clipboardBackend
	send func(text string)

	// img/sendImg: gorsel panosu. sendImg nil ise gorsel esitleme KAPALI
	// (FeatureScreenClipboardImage pazarlanmadi ya da backend desteklemiyor).
	img     clipboardImageBackend
	sendImg func(png []byte)

	mu      sync.Mutex
	enabled bool
	seen    uint32 // islenmis son sayac
	lastTxt string // son gonderilen/ayarlanan metin (yankiyi onler)
	lastImg [32]byte
	hasImg  bool

	// getFails/failSeq: ayni sayac degeri icin ardisik Get hatalari. Baska bir
	// uygulama panoyu aciksa Get gecici basarisiz olur; sayaci "gorulmus"
	// isaretlemeyip sonraki turda yeniden deneriz (sinirli).
	getFails int
	failSeq  uint32

	// notice, kullaniciya gorunur uyari (or. metin boyut siniri); nil olabilir.
	notice func(detail string)
}

// clipboardGetRetries, ayni pano surumu icin en fazla ardisik Get denemesi.
const clipboardGetRetries = 8

func newClipboardWatcher(be clipboardBackend, send func(string)) *clipboardWatcher {
	return &clipboardWatcher{be: be, send: send}
}

// enableImages, gorsel esitlemeyi acar (backend gorseli destekliyorsa).
func (w *clipboardWatcher) enableImages(send func(png []byte)) {
	if ib, ok := w.be.(clipboardImageBackend); ok && send != nil {
		w.img, w.sendImg = ib, send
	}
}

// imagesEnabled, gorsel esitleme acik mi.
func (w *clipboardWatcher) imagesEnabled() bool { return w.img != nil && w.sendImg != nil }

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
		w.hasImg = false
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

// ApplyImage, panelden gelen PNG'yi panoya yazar (yankilanmaz).
func (w *clipboardWatcher) ApplyImage(png []byte) error {
	if !w.imagesEnabled() {
		return errClipImageInvalid
	}
	if !protocol.ClipImageOK(png) {
		return errClipImageTooLarge
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	if err := w.img.SetImage(png); err != nil {
		return err
	}
	w.seen = w.be.Seq()
	w.lastImg, w.hasImg = sha256.Sum256(png), true
	w.lastTxt = ""
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
	txt, err := w.be.Get()
	var png []byte
	imgTooBig := false
	if err == nil && w.imagesEnabled() {
		png, err = w.img.GetImage()
		if errors.Is(err, errClipImageInvalid) || errors.Is(err, errClipImageTooLarge) {
			// Kalici: bu surum icin gorsel yok sayilir (metin yine gider).
			imgTooBig = errors.Is(err, errClipImageTooLarge)
			err, png = nil, nil
		}
	}
	if err != nil {
		// Gecici hata: "gorulmus" isaretleme, sonraki turda yeniden dene.
		if w.failSeq != seq {
			w.failSeq, w.getFails = seq, 0
		}
		w.getFails++
		if w.getFails >= clipboardGetRetries {
			w.seen = seq // vazgec: sonsuz dongu olmasin
			w.getFails = 0
		}
		w.mu.Unlock()
		return
	}
	w.seen, w.getFails = seq, 0

	var sendTxt string
	tooLarge := imgTooBig
	if txt != "" {
		txt = normalizeClipboard(txt)
		switch {
		case len(txt) > protocol.MaxScreenClipboardSize || !protocol.ClipboardTextOK(txt):
			tooLarge = true
		case txt != w.lastTxt:
			w.lastTxt = txt
			sendTxt = txt
		}
	}
	var sendPNG []byte
	if len(png) > 0 {
		if sum := sha256.Sum256(png); !w.hasImg || sum != w.lastImg {
			w.lastImg, w.hasImg = sum, true
			sendPNG = png
		}
	}
	notice, send, sendImg := w.notice, w.send, w.sendImg
	w.mu.Unlock()
	if tooLarge && notice != nil {
		notice(protocol.DegradedClipboardTooLarge)
	}
	if sendTxt != "" {
		send(sendTxt)
	}
	if sendPNG != nil {
		sendImg(sendPNG)
	}
}

// Run, ctx bitene kadar yoklar.
func (w *clipboardWatcher) Run(ctx context.Context) {
	every := clipboardPollInterval
	if p, ok := w.be.(pollIntervaler); ok {
		every = p.pollInterval()
	}
	tk := time.NewTicker(every)
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
