//go:build windows

package agent

import (
	"errors"
	"image"
	"io"
	"os"
	"runtime"
	"sync/atomic"
	"time"

	"golang.org/x/sys/windows"

	"github.com/tkodcumpeg4/zorven/client/i18n"
	"github.com/tkodcumpeg4/zorven/shared/protocol"
)

// Yardimci tarafi: kullanici oturumunda calisir; yakalama (DXGI/kbinani, cokme
// korumasiyla), girdi enjeksiyonu ve pano burada yapilir.

const helperIdleTimeout = 60 * time.Second

// winHelperBackend, gercek yakalayici/girdi/pano uygulamasi.
type winHelperBackend struct {
	cap  Capturer
	hint hintCapturer
	clip winClipboard
}

func (b *winHelperBackend) NumDisplays() int             { return b.cap.NumDisplays() }
func (b *winHelperBackend) Bounds(d int) image.Rectangle { return b.cap.Bounds(d) }
func (b *winHelperBackend) Attach() bool                 { return attachToInputDesktop() }
func (b *winHelperBackend) ClipSeq() uint32              { return b.clip.Seq() }
func (b *winHelperBackend) ClipGet() (string, error)     { return b.clip.Get() }
func (b *winHelperBackend) ClipSet(t string) error       { return b.clip.Set(t) }

func (b *winHelperBackend) Method() string {
	if m, ok := b.cap.(captureMethoder); ok {
		return m.Method()
	}
	return "gdi"
}

func (b *winHelperBackend) CaptureHinted(d int) (*image.RGBA, []image.Rectangle, bool, error) {
	if b.hint != nil {
		return b.hint.CaptureHinted(d)
	}
	img, err := b.cap.Capture(d)
	return img, nil, false, err
}

func (b *winHelperBackend) Input(msg protocol.ScreenInput, rect displayRect) inputResult {
	return applyInput(msg, rect)
}

// RunScreenHelper, "zorven screen-helper" komutunun govdesi. Gecersiz
// bagimsiz degiskenlerle ya da oturum 0'da calismayi reddeder.
func RunScreenHelper(pipeName, token, version string) error {
	if err := validHelperArgs(pipeName, token); err != nil {
		return errors.New(i18n.T("scr.err.args"))
	}
	if inSession0() {
		return errors.New(i18n.T("scr.err.session0"))
	}
	captureVersion = version
	// Masaustu baglama ve girdi enjeksiyonu is parcacigina baglidir.
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	attachToInputDesktop()

	var h windows.Handle
	var err error
	name, _ := windows.UTF16PtrFromString(pipeName)
	for i := 0; i < 20; i++ {
		h, err = windows.CreateFile(name, windows.GENERIC_READ|windows.GENERIC_WRITE, 0, nil, windows.OPEN_EXISTING, 0, 0)
		if err == nil {
			break
		}
		time.Sleep(200 * time.Millisecond)
	}
	if err != nil {
		return err
	}
	pipe := &winPipe{h: h}
	defer pipe.Close()

	if err := dialHandshake(pipe, token, version, uint32(os.Getpid())); err != nil {
		return err
	}

	// Bosta kalma: servis istek yollamazsa (sizdirilmis yardimci) kendiliginden cik.
	var last atomic.Int64
	last.Store(time.Now().UnixNano())
	stop := make(chan struct{})
	defer close(stop)
	go func() {
		t := time.NewTicker(5 * time.Second)
		defer t.Stop()
		for {
			select {
			case <-stop:
				return
			case <-t.C:
				if time.Since(time.Unix(0, last.Load())) > helperIdleTimeout {
					pipe.Close()
					return
				}
			}
		}
	}()

	pc := newPlatCapturer() // kullanici oturumunda: DXGI + koruma + kbinani yedegi
	be := &winHelperBackend{cap: pc}
	be.hint, _ = pc.(hintCapturer)
	defer func() {
		if c, ok := pc.(io.Closer); ok {
			c.Close()
		}
	}()
	err = serveHelper(pipe, be, func() { last.Store(time.Now().UnixNano()) })
	if errors.Is(err, io.EOF) || errors.Is(err, io.ErrClosedPipe) {
		return nil
	}
	return err
}
