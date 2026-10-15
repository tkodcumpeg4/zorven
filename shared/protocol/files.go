package protocol

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"
	"unicode"
	"unicode/utf8"
)

// Ekran oturumu dosya aktarimi (FeatureScreenFiles = "screen_files").
//
// Kontrol mesajlari JSON (TypeFile*), veri FrameFile ikili cerceveleriyle akar.
// Akis kontrolu kredi tabanlidir: gonderen, alicinin file_ack ile bildirdigi
// kumulatif tuketim ile gonderdigi bayt arasindaki fark FileWindowBytes'i
// asmayacak sekilde bekler. Boylece tek aktarim kontrol kanalini (ve ekran
// karelerini) bogamaz. Ayrica gonderen taraf FileRate ile hiz sinirlar.
//
// Akis (yukleme, panel -> uzak makine):
//
//	S->A file_open{put,name,size}   A->S file_open_ack{ok,name}
//	S->A FrameFile...               A->S file_ack...
//	S->A file_end{size,sha256}      A->S file_done{ok|checksum hatasi}
//
// Akis (indirme, uzak makine -> panel):
//
//	S->A file_open{get,name}        A->S file_open_ack{ok,size}
//	A->S FrameFile...               S->A file_ack...
//	A->S file_end{size,sha256}      (sunucu dogrular)
//
// Iki taraf da her an file_abort gonderebilir.
const FeatureScreenFiles = "screen_files"

const (
	FileChunkSize       = 32 << 10  // tek FrameFile yuku
	FileWindowBytes     = 1 << 20   // ucusta en fazla 1 MiB
	FileAckEvery        = 256 << 10 // alici bu kadar tuketince file_ack yollar
	MaxFileXfers        = 3         // es zamanli aktarim (istemci basina)
	MaxFileNameBytes    = 200       // temizlenmis dosya adi UTF-8 bayt siniri
	MaxFileListItems    = 500       // listelenen en fazla dosya
	FileRateBytesPerSec = 24 << 20  // gonderen hiz tavani (ekran karelerini aclik etmesin)
	FileDirPut          = "put"     // panel -> istemci
	FileDirGet          = "get"     // istemci -> panel

	// MaxFileSize, bir dosyanin mutlak ust siniri (2 GiB); istemci daha dusuk
	// tavan secebilir.
	MaxFileSize int64 = 2 << 30
)

// Hata kodlari (file_open_ack / file_done / file_abort .Code).
const (
	FileErrDisabled    = "disabled"    // istemci dosya aktarimini kapatmis
	FileErrBusy        = "busy"        // es zamanli aktarim siniri
	FileErrBadName     = "bad_name"    // gecersiz / yol gecisi iceren ad
	FileErrTooLarge    = "too_large"   // boyut siniri
	FileErrNotFound    = "not_found"   // indirilecek dosya yok / duz dosya degil
	FileErrIO          = "io"          // disk hatasi
	FileErrChecksum    = "checksum"    // sha256 / boyut uyusmazligi
	FileErrCanceled    = "canceled"    // iptal
	FileErrProtocol    = "protocol"    // kredi ihlali, beklenmeyen mesaj
	FileErrChanged     = "changed"     // indirilirken dosya degisti
	FileErrViewOnly    = "view_only"   // yalniz sunucu uretir
	FileErrUnsupported = "unsupported" // yalniz sunucu uretir
)

// FileOpen, aktarimi baslatir (sunucu -> istemci).
type FileOpen struct {
	Type   MessageType `json:"type"`
	XferID uint64      `json:"xfer_id"`
	Dir    string      `json:"dir"`            // FileDirPut | FileDirGet
	Name   string      `json:"name"`           // yalniz dosya adi (yol YOK)
	Size   int64       `json:"size,omitempty"` // put: beklenen toplam bayt
}

// FileOpenAck, file_open sonucu (istemci -> sunucu).
type FileOpenAck struct {
	Type    MessageType `json:"type"`
	XferID  uint64      `json:"xfer_id"`
	OK      bool        `json:"ok"`
	Name    string      `json:"name,omitempty"` // put: kaydedilecek (benzersizlestirilmis) ad
	Size    int64       `json:"size,omitempty"` // get: dosya boyutu
	Code    string      `json:"code,omitempty"`
	Message string      `json:"message,omitempty"`
}

// FileAck, kredi: kumulatif tuketilen bayt (iki yon).
type FileAck struct {
	Type   MessageType `json:"type"`
	XferID uint64      `json:"xfer_id"`
	Bytes  int64       `json:"bytes"`
}

// FileEnd, veri bitti; toplam boyut ve sha256 (hex).
type FileEnd struct {
	Type   MessageType `json:"type"`
	XferID uint64      `json:"xfer_id"`
	Size   int64       `json:"size"`
	SHA256 string      `json:"sha256"`
}

// FileDone, yuklemenin dogrulanmis sonucu (istemci -> sunucu).
type FileDone struct {
	Type    MessageType `json:"type"`
	XferID  uint64      `json:"xfer_id"`
	OK      bool        `json:"ok"`
	Name    string      `json:"name,omitempty"`
	Size    int64       `json:"size,omitempty"`
	SHA256  string      `json:"sha256,omitempty"`
	Code    string      `json:"code,omitempty"`
	Message string      `json:"message,omitempty"`
}

// FileAbort, aktarimi iptal eder (iki yon).
type FileAbort struct {
	Type    MessageType `json:"type"`
	XferID  uint64      `json:"xfer_id"`
	Code    string      `json:"code,omitempty"`
	Message string      `json:"message,omitempty"`
}

// FileList, izinli klasordeki dosyalari ister (sunucu -> istemci).
type FileList struct {
	Type  MessageType `json:"type"`
	ReqID uint64      `json:"req_id"`
}

// FileInfo, listedeki tek dosya.
type FileInfo struct {
	Name    string `json:"name"`
	Size    int64  `json:"size"`
	ModTime int64  `json:"mod_time"` // unix sn
}

// FileListResult, liste yaniti (istemci -> sunucu).
type FileListResult struct {
	Type    MessageType `json:"type"`
	ReqID   uint64      `json:"req_id"`
	Dir     string      `json:"dir,omitempty"` // izinli klasor (goruntuleme icin)
	Files   []FileInfo  `json:"files,omitempty"`
	Code    string      `json:"code,omitempty"`
	Message string      `json:"message,omitempty"`
}

// --- Dosya adi guvenligi -----------------------------------------------------

// ErrBadFileName, gecersiz veya yol gecisi iceren ad.
var ErrBadFileName = errors.New("gecersiz dosya adi")

// windowsReserved, Windows'ta aygit adi olan taban adlar (uzantidan bagimsiz).
var windowsReserved = map[string]bool{"CON": true, "PRN": true, "AUX": true, "NUL": true, "CONIN$": true, "CONOUT$": true}

func reservedWindowsBase(base string) bool {
	up := strings.ToUpper(strings.TrimRight(base, " ."))
	if windowsReserved[up] {
		return true
	}
	return len(up) == 4 && (strings.HasPrefix(up, "COM") || strings.HasPrefix(up, "LPT")) && up[3] >= '1' && up[3] <= '9'
}

// checkPlainName, adin yol/gecis icermedigini dogrular: ayirici, NUL ve kontrol
// karakteri, "." ve ".." reddedilir.
func checkPlainName(name string) error {
	if name == "" || name == "." || name == ".." || !utf8.ValidString(name) {
		return ErrBadFileName
	}
	for _, r := range name {
		if r == '/' || r == '\\' || r == 0 || unicode.IsControl(r) {
			return ErrBadFileName
		}
	}
	return nil
}

// ValidExistingFileName, var olan bir dosyanin adinin (indirme icin) yol gecisi
// icermedigini dogrular; ad DEGISTIRILMEZ.
func ValidExistingFileName(name string) error {
	if err := checkPlainName(name); err != nil {
		return err
	}
	if len(name) > 4*MaxFileNameBytes {
		return ErrBadFileName
	}
	return nil
}

// SanitizeFileName, yuklenecek dosya adini her platformda guvenli hale getirir.
// Yol ayiricilari / NUL / kontrol karakterleri ve "..": REDDEDILIR (gecis
// girisimi). Windows'ta gecersiz karakterler (<>:"|?*) "_" olur, sondaki
// nokta/bosluk atilir, aygit adlari (CON, NUL, COM1...) basina "_" alir, ad
// MaxFileNameBytes'a (uzantiyi koruyarak, rune sinirinda) kisaltilir.
func SanitizeFileName(name string) (string, error) {
	if err := checkPlainName(name); err != nil {
		return "", err
	}
	var b strings.Builder
	for _, r := range name {
		switch r {
		case '<', '>', ':', '"', '|', '?', '*':
			b.WriteRune('_')
		default:
			b.WriteRune(r)
		}
	}
	out := strings.TrimRight(b.String(), " .")
	out = strings.TrimLeft(out, " ")
	if out == "" || out == "." || out == ".." {
		return "", ErrBadFileName
	}
	// Taban ad = ilk noktaya kadar ("CON.tar.gz" da aygit adidir).
	base := out
	if i := strings.IndexByte(out, '.'); i >= 0 {
		base = out[:i]
	}
	if reservedWindowsBase(base) {
		out = "_" + out
	}
	if len(out) > MaxFileNameBytes {
		ext := ""
		if i := strings.LastIndexByte(out, '.'); i > 0 && len(out)-i <= 16 {
			ext = out[i:]
		}
		stem := out[:len(out)-len(ext)]
		limit := MaxFileNameBytes - len(ext)
		for len(stem) > limit {
			_, sz := utf8.DecodeLastRuneInString(stem)
			stem = stem[:len(stem)-sz]
		}
		stem = strings.TrimRight(stem, " .")
		if stem == "" {
			return "", ErrBadFileName
		}
		out = stem + ext
	}
	return out, nil
}

// UniqueFileName, name'e n. cakisma icin " (n)" son eki ekler (uzantidan once):
// "a.txt" -> "a (1).txt". n<=0 ise ad degismez.
func UniqueFileName(name string, n int) string {
	if n <= 0 {
		return name
	}
	ext := ""
	stem := name
	if i := strings.LastIndexByte(name, '.'); i > 0 {
		ext = name[i:]
		stem = name[:i]
	}
	return fmt.Sprintf("%s (%d)%s", stem, n, ext)
}

// --- Akis kontrolu -----------------------------------------------------------

// FileCredit, gonderen tarafin kredi penceresi.
type FileCredit struct {
	mu     sync.Mutex
	window int64
	sent   int64
	acked  int64
	err    error
	wake   chan struct{}
}

// NewFileCredit, window baytlik bir pencere kurar (<=0 ise FileWindowBytes).
func NewFileCredit(window int64) *FileCredit {
	if window <= 0 {
		window = FileWindowBytes
	}
	return &FileCredit{window: window, wake: make(chan struct{}, 1)}
}

func (c *FileCredit) kick() {
	select {
	case c.wake <- struct{}{}:
	default:
	}
}

// Reserve, n bayt gondermeden once cagrilir; kredi yoksa bekler. Hic ucusta
// bayt yokken n pencereden buyuk olsa da gecer. ctx iptal veya Fail ile hata doner.
func (c *FileCredit) Reserve(ctx context.Context, n int64) error {
	for {
		c.mu.Lock()
		if c.err != nil {
			err := c.err
			c.mu.Unlock()
			return err
		}
		if c.sent-c.acked+n <= c.window || c.sent == c.acked {
			c.sent += n
			c.mu.Unlock()
			return nil
		}
		c.mu.Unlock()
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-c.wake:
		}
	}
}

// Ack, kumulatif tuketimi gunceller. Gonderilenden fazla veya geri giden deger
// (protokol ihlali) false doner ve yok sayilir.
func (c *FileCredit) Ack(total int64) bool {
	c.mu.Lock()
	ok := total <= c.sent && total >= c.acked
	if ok {
		c.acked = total
	}
	c.mu.Unlock()
	if ok {
		c.kick()
	}
	return ok
}

// Fail, bekleyenleri err ile uyandirir.
func (c *FileCredit) Fail(err error) {
	c.mu.Lock()
	if c.err == nil {
		c.err = err
	}
	c.mu.Unlock()
	c.kick()
}

// InFlight, gonderilmis ama onaylanmamis bayt.
func (c *FileCredit) InFlight() int64 {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.sent - c.acked
}

// FileAcker, alici tarafin tuketim sayaci: her FileAckEvery baytta bir
// kumulatif deger uretir.
type FileAcker struct {
	total, last int64
}

// Consume, n bayt tuketildi; ack gerekiyorsa (kumulatif, true) doner.
func (a *FileAcker) Consume(n int) (int64, bool) {
	a.total += int64(n)
	if a.total-a.last >= FileAckEvery {
		a.last = a.total
		return a.total, true
	}
	return 0, false
}

// Total, toplam tuketilen bayt.
func (a *FileAcker) Total() int64 { return a.total }

// FileRate, basit jeton kovasi: Wait(n) ortalama hizi tavanda tutar. Tek
// goroutine tarafindan kullanilir.
type FileRate struct {
	perSec float64
	avail  float64
	last   time.Time
	now    func() time.Time
	sleep  func(context.Context, time.Duration) error
}

// NewFileRate, saniyede perSec baytlik sinirlayici (patlama = 1 saniyenin 1/8'i).
func NewFileRate(perSec int64) *FileRate {
	return &FileRate{perSec: float64(perSec), now: time.Now, sleep: sleepCtx}
}

func sleepCtx(ctx context.Context, d time.Duration) error {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}

// Wait, n baytlik gonderim icin gereken sure kadar bekler.
func (r *FileRate) Wait(ctx context.Context, n int) error {
	now := r.now()
	if r.last.IsZero() {
		r.last = now
		r.avail = r.perSec / 8
	}
	r.avail += now.Sub(r.last).Seconds() * r.perSec
	if burst := r.perSec / 8; r.avail > burst {
		r.avail = burst
	}
	r.last = now
	r.avail -= float64(n)
	if r.avail >= 0 {
		return nil
	}
	d := time.Duration(-r.avail / r.perSec * float64(time.Second))
	return r.sleep(ctx, d)
}
