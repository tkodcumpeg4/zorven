package agent

import (
	"encoding/json"
	"errors"
	"image"
	"io"
	"sync"
	"time"

	"github.com/tkodcumpeg4/zorven/client/i18n"
	"github.com/tkodcumpeg4/zorven/shared/protocol"
)

// Servis tarafi: ekran yardimcisina istemci. Servis (oturum 0) yakalama/girdi/pano
// isteklerini kullanici oturumundaki yardimciya iletir; boru hatti (screen2)
// degismez, helperCapturer Capturer + hintCapturer olarak gorunur.

// errNoInteractiveUser: aktif konsol oturumunda oturum acmis kullanici yok.
var errNoInteractiveUser = errors.New("no interactive user logged on")

// helperConnector, yardimci sureci baslatip guvenli kanali kurar (Windows'ta
// gercek; testte sahte).
type helperConnector interface {
	// Connect, yardimciyi (yoksa) baslatir ve el sikismasi bitmis bir kanal doner.
	Connect() (io.ReadWriteCloser, error)
	// Stale, baglanan yardimcinin oturumu artik aktif konsol degilse true.
	Stale() bool
	// Shutdown, yardimci sureci sonlandirir (kanal kapandiktan sonra).
	Shutdown()
}

const (
	helperCallTimeout = 10 * time.Second
	helperInfoTTL     = 2 * time.Second
	helperBackoffMin  = time.Second
	helperBackoffMax  = 30 * time.Second
	helperNoUserRetry = 2 * time.Second
)

type helperClient struct {
	conn    helperConnector
	now     func() time.Time
	timeout time.Duration

	mu      sync.Mutex // istekleri sirala + durumu koru
	link    io.ReadWriteCloser
	nextTry time.Time
	backoff time.Duration
	lastErr error

	canvas     *image.RGBA
	canvasDisp int

	cmu     sync.Mutex
	nDisp   int
	bounds  []image.Rectangle
	infoAt  time.Time
	method  string
	degrade bool
}

func newHelperClient(conn helperConnector) *helperClient {
	return &helperClient{conn: conn, now: time.Now, timeout: helperCallTimeout, canvasDisp: -1}
}

// Err, kullaniciya gosterilecek (cevrilmis) son hata; yoksa nil.
func (c *helperClient) Err() error {
	c.mu.Lock()
	err := c.lastErr
	c.mu.Unlock()
	if err == nil {
		return nil
	}
	if errors.Is(err, errNoInteractiveUser) {
		return errors.New(i18n.T("scr.err.no_user"))
	}
	return errors.New(i18n.T("scr.err.helper", err.Error()))
}

// dropLocked, kanali kapatir (mu tutulurken).
func (c *helperClient) dropLocked() {
	if c.link != nil {
		_ = c.link.Close()
		c.link = nil
	}
	c.canvas, c.canvasDisp = nil, -1
}

func (c *helperClient) failConnectLocked(err error) {
	c.lastErr = err
	if errors.Is(err, errNoInteractiveUser) {
		c.backoff = helperNoUserRetry
	} else if c.backoff = c.backoff * 2; c.backoff < helperBackoffMin {
		c.backoff = helperBackoffMin
	} else if c.backoff > helperBackoffMax {
		c.backoff = helperBackoffMax
	}
	c.nextTry = c.now().Add(c.backoff)
}

// ensureLocked, canli kanali saglar (gerekirse yardimciyi baslatir).
func (c *helperClient) ensureLocked() error {
	if c.link != nil && c.conn.Stale() {
		c.dropLocked()
		c.conn.Shutdown()
	}
	if c.link != nil {
		return nil
	}
	if c.now().Before(c.nextTry) && c.lastErr != nil {
		return c.lastErr
	}
	l, err := c.conn.Connect()
	if err != nil {
		c.failConnectLocked(err)
		return err
	}
	c.link, c.lastErr, c.backoff = l, nil, 0
	return nil
}

// call, kilit adimli tek istek/yanit. Kanal bozuksa bir kez yeniden baglanip dener.
func (c *helperClient) call(typ byte, payload []byte, want byte) ([]byte, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	var lastErr error
	for attempt := 0; attempt < 2; attempt++ {
		if err := c.ensureLocked(); err != nil {
			return nil, err
		}
		link := c.link
		t := time.AfterFunc(c.timeout, func() { _ = link.Close() }) // takili yardimciya karsi
		err := writeFrame(link, typ, payload)
		var rt byte
		var rp []byte
		if err == nil {
			rt, rp, err = readFrame(link)
		}
		t.Stop()
		if err == nil && rt != want {
			err = errHelperFrame
		}
		if err == nil {
			return rp, nil
		}
		lastErr = err
		c.dropLocked()
		c.conn.Shutdown()
	}
	c.failConnectLocked(lastErr)
	return nil, lastErr
}

func (c *helperClient) setInfo(n int, bounds []image.Rectangle) {
	c.cmu.Lock()
	c.nDisp, c.bounds, c.infoAt = n, bounds, c.now()
	c.cmu.Unlock()
}

func (c *helperClient) refreshInfo() error {
	p, err := c.call(hmInfoReq, nil, hmInfoResp)
	if err != nil {
		return err
	}
	r := rbuf{b: p}
	if r.u8() != 0 {
		return errors.New(r.str())
	}
	n := int(r.u16())
	bs := make([]image.Rectangle, 0, n)
	for i := 0; i < n; i++ {
		bs = append(bs, r.rect())
	}
	if r.err != nil {
		return r.err
	}
	c.setInfo(n, bs)
	return nil
}

func (c *helperClient) infoFresh() bool {
	c.cmu.Lock()
	defer c.cmu.Unlock()
	return !c.infoAt.IsZero() && c.now().Sub(c.infoAt) < helperInfoTTL
}

// NumDisplays, monitor sayisi; yardimciya ulasilamazsa 0 (Err nedeni verir).
func (c *helperClient) NumDisplays() int {
	if !c.infoFresh() {
		if err := c.refreshInfo(); err != nil {
			return 0
		}
	}
	c.cmu.Lock()
	defer c.cmu.Unlock()
	return c.nDisp
}

func (c *helperClient) Bounds(d int) image.Rectangle {
	if !c.infoFresh() {
		_ = c.refreshInfo()
	}
	c.cmu.Lock()
	defer c.cmu.Unlock()
	if d < 0 || d >= len(c.bounds) {
		return image.Rectangle{}
	}
	return c.bounds[d]
}

// CaptureHinted: hintCapturer sozlesmesi. Yardimci yalniz degisen bolgeleri
// yollar; servis kalici tuvalde birlestirir ve cagirana KOPYA verir.
func (c *helperClient) CaptureHinted(d int) (*image.RGBA, []image.Rectangle, bool, error) {
	var req wbuf
	req.i32(d)
	p, err := c.call(hmCapReq, req.Bytes(), hmCapResp)
	if err != nil {
		return nil, nil, false, err
	}
	cr, remote, err := decodeCapResp(p)
	if err == nil && remote != "" {
		return nil, nil, false, errors.New(remote)
	}
	if err != nil {
		c.resetLink()
		return nil, nil, false, err
	}
	c.cmu.Lock()
	c.method, c.degrade = cr.Method, cr.Flags&capFlagDegraded != 0
	if d >= 0 && d < len(c.bounds) {
		c.bounds[d] = cr.Bounds
	}
	if cr.Displays != c.nDisp {
		c.infoAt = time.Time{} // sayi degisti: sonraki sorguda tazele
	}
	c.nDisp = cr.Displays
	c.cmu.Unlock()

	c.mu.Lock()
	defer c.mu.Unlock()
	unchanged := cr.Flags&capFlagUnchang != 0
	hinted := cr.Flags&capFlagHinted != 0
	if c.canvas == nil || c.canvasDisp != d || c.canvas.Rect.Dx() != cr.W || c.canvas.Rect.Dy() != cr.H {
		// Taban kare yoksa yalniz tam kare kabul edilir.
		if unchanged || hinted || len(cr.Rects) != 1 || cr.Rects[0] != image.Rect(0, 0, cr.W, cr.H) {
			// Senkron bozuldu: yardimciyi yeniden baslat (ilk kare tam gelir).
			c.dropLocked()
			c.conn.Shutdown()
			return nil, nil, false, errors.New("helper frame out of sync")
		}
		c.canvas, c.canvasDisp = image.NewRGBA(image.Rect(0, 0, cr.W, cr.H)), d
	}
	for i, rc := range cr.Rects {
		pasteRect(c.canvas, rc, cr.Pix[i])
	}
	snap := &image.RGBA{Pix: append([]byte(nil), c.canvas.Pix...), Stride: c.canvas.Stride, Rect: c.canvas.Rect}
	if unchanged {
		return nil, nil, true, nil
	}
	if hinted {
		return snap, cr.Rects, true, nil
	}
	return snap, nil, false, nil
}

func (c *helperClient) resetLink() {
	c.mu.Lock()
	c.dropLocked()
	c.mu.Unlock()
	c.conn.Shutdown()
}

// Capture: tam kare (ipucu gerekmez).
func (c *helperClient) Capture(d int) (*image.RGBA, error) {
	img, _, hinted, err := c.CaptureHinted(d)
	if err != nil {
		return nil, err
	}
	if img == nil && hinted { // degisim yok: tuvalin kopyasi
		c.mu.Lock()
		defer c.mu.Unlock()
		if c.canvas == nil {
			return nil, errors.New("no frame")
		}
		return &image.RGBA{Pix: append([]byte(nil), c.canvas.Pix...), Stride: c.canvas.Stride, Rect: c.canvas.Rect}, nil
	}
	return img, nil
}

// Method, canli olcum paneli icin yontem adi ("helper/dxgi" gibi).
func (c *helperClient) Method() string {
	c.cmu.Lock()
	defer c.cmu.Unlock()
	if c.method == "" {
		return "helper"
	}
	return "helper/" + c.method
}

// Degraded, son karede guvenli masaustu nedeniyle son kare tekrarlandi mi.
func (c *helperClient) Degraded() bool {
	c.cmu.Lock()
	defer c.cmu.Unlock()
	return c.degrade
}

// Input, girdiyi kullanici oturumundaki yardimciya enjekte ettirir.
func (c *helperClient) Input(msg protocol.ScreenInput, rect displayRect) inputResult {
	b, _ := json.Marshal(helperInputReq{Msg: msg, Rect: [4]int{rect.x, rect.y, rect.w, rect.h}})
	p, err := c.call(hmInputReq, b, hmInputResp)
	if err != nil {
		if errors.Is(err, errNoInteractiveUser) {
			return inputResult{State: protocol.InputStateNoSession, Detail: "no interactive user"}
		}
		return inputResult{State: protocol.InputStateError, Detail: err.Error()}
	}
	var r helperInputResp
	if json.Unmarshal(p, &r) != nil {
		return inputResult{State: protocol.InputStateError, Detail: "bad helper reply"}
	}
	return inputResult{State: r.State, Code: r.Code, Detail: r.Detail}
}

// clip, pano istegi.
func (c *helperClient) clip(op byte, text string) (seq uint32, got string, err error) {
	var b wbuf
	b.u8(op)
	if op == clipOpSet {
		b.u32(uint32(len(text)))
		b.WriteString(text)
	}
	p, err := c.call(hmClipReq, b.Bytes(), hmClipResp)
	if err != nil {
		return 0, "", err
	}
	r := rbuf{b: p}
	if r.u8() != 0 {
		return 0, "", errors.New(r.str())
	}
	switch op {
	case clipOpSeq:
		seq = r.u32()
	case clipOpGet:
		got = string(r.take(int(r.u32())))
	}
	return seq, got, r.err
}

// helperClipboard, clipboardBackend'in yardimci uzerinden uygulamasi.
type helperClipboard struct{ c *helperClient }

func (h helperClipboard) Seq() uint32 { s, _, _ := h.c.clip(clipOpSeq, ""); return s }
func (h helperClipboard) Get() (string, error) {
	_, t, err := h.c.clip(clipOpGet, "")
	return t, err
}
func (h helperClipboard) Set(text string) error { _, _, err := h.c.clip(clipOpSet, text); return err }

// Close, yardimciyi kapatir (kanal + surec).
func (c *helperClient) Close() error {
	c.mu.Lock()
	if c.link != nil {
		_ = writeFrame(c.link, hmShutdown, nil)
	}
	c.dropLocked()
	c.mu.Unlock()
	c.conn.Shutdown()
	return nil
}

// helperCapturer, boru hattinin kullandigi Capturer; hepsini yardimciya devreder.
type helperCapturer struct{ c *helperClient }

func (h helperCapturer) NumDisplays() int                   { return h.c.NumDisplays() }
func (h helperCapturer) Bounds(d int) image.Rectangle       { return h.c.Bounds(d) }
func (h helperCapturer) Capture(d int) (*image.RGBA, error) { return h.c.Capture(d) }
func (h helperCapturer) Method() string                     { return h.c.Method() }
func (h helperCapturer) CaptureHinted(d int) (*image.RGBA, []image.Rectangle, bool, error) {
	return h.c.CaptureHinted(d)
}
