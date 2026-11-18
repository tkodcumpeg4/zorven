package agent

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"io"
	"os/exec"
	"sync"
	"time"

	"github.com/tkodcumpeg4/zorven/shared/protocol"
)

// Linux/macOS metin panosu: cgo YOK; sistemin pano araclari alt surec olarak
// calistirilir (macOS: pbcopy/pbpaste; Wayland: wl-copy/wl-paste; X11: xclip ya da
// xsel). Hicbiri yoksa (ya da DISPLAY/WAYLAND_DISPLAY yoksa: servis, ssh) pano
// desteklenmez ve ilan edilmez. Mantik platformdan bagimsizdir; alt surec
// cmdRunner arkasinda oldugu icin sahte ile birim testlenir.

// clipCmdTimeout, tek alt surecin en uzun suresi.
const clipCmdTimeout = 2 * time.Second

// clipCmdPoll, alt surec maliyeti yuzunden Windows'tan (400 ms) seyrek yoklama.
const clipCmdPoll = time.Second

// clipSetHold, Set sonrasi Seq'in yeniden OKUMADAN onbellekten donecegi sure
// (xclip/wl-copy sahipligi arka planda devralir; hemen geri okuma eski icerigi gorebilir).
const clipSetHold = 1500 * time.Millisecond

// errClipCmdExit, aracin sifirdan farkli kodla cikmasi (panoda metin yok vb.).
var errClipCmdExit = errors.New("pano araci hata koduyla cikti")

// cmdRunner, alt surec calistirma yuzeyi.
type cmdRunner interface {
	// Output, stdout'u en fazla limit bayt okur; arac hata koduyla cikarsa errClipCmdExit.
	Output(ctx context.Context, name string, args []string, limit int) ([]byte, error)
	// Input, data'yi stdin'e verip aracin cikisini bekler (arka plana gecen araclar dahil).
	Input(ctx context.Context, name string, args []string, data []byte) error
}

// clipTool, secilen arac takimi.
type clipTool struct {
	name        string
	paste, copy []string // ilk eleman komut adi
}

// detectClipTool, ortama uygun arac takimini secer (yoksa nil). goos/getenv/look
// test icin enjekte edilir.
func detectClipTool(goos string, getenv func(string) string, look func(string) (string, error)) *clipTool {
	has := func(n string) bool { _, err := look(n); return err == nil }
	if goos == "darwin" {
		if has("pbpaste") && has("pbcopy") {
			return &clipTool{"pbcopy", []string{"pbpaste"}, []string{"pbcopy"}}
		}
		return nil
	}
	if goos == "windows" {
		return nil
	}
	if getenv("WAYLAND_DISPLAY") != "" && has("wl-paste") && has("wl-copy") {
		return &clipTool{"wl-clipboard", []string{"wl-paste", "--no-newline"}, []string{"wl-copy"}}
	}
	if getenv("DISPLAY") != "" {
		if has("xclip") {
			return &clipTool{"xclip", []string{"xclip", "-selection", "clipboard", "-o"}, []string{"xclip", "-selection", "clipboard", "-i"}}
		}
		if has("xsel") {
			return &clipTool{"xsel", []string{"xsel", "--clipboard", "--output"}, []string{"xsel", "--clipboard", "--input"}}
		}
	}
	return nil
}

// cmdClipboard, clipboardBackend'in alt surec uygulamasi. Seq, panonun icerik
// ozetinin degisimiyle artar; Get, son Seq okumasinin onbellegini doner.
type cmdClipboard struct {
	run  cmdRunner
	tool clipTool
	now  func() time.Time // yalniz testler

	mu     sync.Mutex
	inited bool
	seq    uint32
	sum    [32]byte
	text   string
	hold   time.Time
}

func newCmdClipboard(run cmdRunner, tool clipTool) *cmdClipboard {
	return &cmdClipboard{run: run, tool: tool, now: time.Now}
}

func (c *cmdClipboard) pollInterval() time.Duration { return clipCmdPoll }

func (c *cmdClipboard) Seq() uint32 {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.now().Before(c.hold) {
		return c.seq
	}
	ctx, cancel := context.WithTimeout(context.Background(), clipCmdTimeout)
	defer cancel()
	out, err := c.run.Output(ctx, c.tool.paste[0], c.tool.paste[1:], protocol.MaxScreenClipboardSize+1)
	if err != nil {
		if !errors.Is(err, errClipCmdExit) {
			return c.seq // okunamadi (zaman asimi/baslatilamadi): degisim yok say
		}
		out = nil // arac "metin yok" diyor
	}
	sum := sha256.Sum256(out)
	if !c.inited {
		c.inited, c.sum, c.text = true, sum, string(out)
		return c.seq
	}
	if sum != c.sum {
		c.sum, c.text = sum, string(out)
		c.seq++
	}
	return c.seq
}

func (c *cmdClipboard) Get() (string, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.text, nil
}

func (c *cmdClipboard) Set(text string) error {
	ctx, cancel := context.WithTimeout(context.Background(), clipCmdTimeout)
	defer cancel()
	if err := c.run.Input(ctx, c.tool.copy[0], c.tool.copy[1:], []byte(text)); err != nil {
		return err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.inited = true
	c.sum, c.text = sha256.Sum256([]byte(text)), text
	c.seq++
	c.hold = c.now().Add(clipSetHold)
	return nil
}

// execRunner, gercek alt surec calistirici.
type execRunner struct{}

func (execRunner) Output(ctx context.Context, name string, args []string, limit int) ([]byte, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.WaitDelay = 300 * time.Millisecond // torunlari boruyu tutan araclar Wait'i asili birakmasin
	pipe, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	// Zaman asiminda boruyu kapat: torunlar yazma ucunu tutsa da okuma takili kalmasin.
	stop := context.AfterFunc(ctx, func() { _ = pipe.Close() })
	defer stop()
	data, _ := io.ReadAll(io.LimitReader(pipe, int64(limit)))
	if len(data) >= limit {
		// Sinir doldu: aracin geri kalanini beklemeden sonlandir.
		_ = pipe.Close()
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		return data, nil
	}
	werr := cmd.Wait()
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	if werr != nil {
		var ee *exec.ExitError
		if errors.As(werr, &ee) {
			return nil, errClipCmdExit
		}
		return nil, werr
	}
	return data, nil
}

func (execRunner) Input(ctx context.Context, name string, args []string, data []byte) error {
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.WaitDelay = 300 * time.Millisecond // torunlari boruyu tutan araclar Wait'i asili birakmasin
	cmd.Stdin = bytes.NewReader(data)
	// Stdout/Stderr nil: /dev/null. Arka plana gecen araclar (xclip, wl-copy) boruyu
	// tutup Wait'i asili birakmaz.
	return cmd.Run()
}
