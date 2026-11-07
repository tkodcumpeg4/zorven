package main

// Masaustu gunlugu: slog cikisi dosyaya (boyut sinirli, donen) yazilir. GUI
// surecinde stderr yoktur; hatalar kaybolmasin diye %LOCALAPPDATA%\Zorven\logs
// altina yazilir. Token ASLA gunluge yazilmaz (ajan zaten maskeler).

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"
)

const (
	logFileName = "desktop.log"
	logMaxBytes = 5 << 20 // 5 MB
	logKeep     = 3       // desktop.log + .1 + .2
)

// logDir, gunluk klasoru: Windows'ta %LOCALAPPDATA%\Zorven\logs.
func logDir() string {
	base, err := os.UserCacheDir() // Windows: %LocalAppData%
	if err != nil || base == "" {
		base = os.TempDir()
	}
	return filepath.Join(base, "Zorven", "logs")
}

// rotatingFile, boyut asilinca dosyayi dondurur: desktop.log -> .1 -> .2.
type rotatingFile struct {
	mu   sync.Mutex
	path string
	max  int64
	keep int
	f    *os.File
	size int64
}

func newRotatingFile(path string, max int64, keep int) (*rotatingFile, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, err
	}
	r := &rotatingFile{path: path, max: max, keep: keep}
	if err := r.open(); err != nil {
		return nil, err
	}
	return r, nil
}

func (r *rotatingFile) open() error {
	f, err := os.OpenFile(r.path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return err
	}
	fi, err := f.Stat()
	if err == nil {
		r.size = fi.Size()
	}
	r.f = f
	return nil
}

func (r *rotatingFile) rotate() error {
	if r.f != nil {
		r.f.Close()
		r.f = nil
	}
	// en eskiyi sil, digerlerini bir kaydir.
	for i := r.keep - 1; i >= 1; i-- {
		src := r.path
		if i > 1 {
			src = fmt.Sprintf("%s.%d", r.path, i-1)
		}
		dst := fmt.Sprintf("%s.%d", r.path, i)
		_ = os.Remove(dst)
		_ = os.Rename(src, dst)
	}
	r.size = 0
	return r.open()
}

func (r *rotatingFile) Write(p []byte) (int, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.f == nil {
		return 0, os.ErrClosed
	}
	if r.size+int64(len(p)) > r.max {
		if err := r.rotate(); err != nil {
			return 0, err
		}
	}
	n, err := r.f.Write(p)
	r.size += int64(n)
	return n, err
}

func (r *rotatingFile) Close() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.f == nil {
		return nil
	}
	err := r.f.Close()
	r.f = nil
	return err
}

// errRing, son uyari/hata kayitlarini bellekte tutar (tanilama icin).
type errRing struct {
	mu    sync.Mutex
	items []string
	max   int
}

func (e *errRing) add(s string) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.items = append(e.items, s)
	if len(e.items) > e.max {
		e.items = e.items[len(e.items)-e.max:]
	}
}

func (e *errRing) list() []string {
	e.mu.Lock()
	defer e.mu.Unlock()
	return append([]string(nil), e.items...)
}

// ringHandler, WARN ve ustu kayitlari halkaya da yazar.
type ringHandler struct {
	slog.Handler
	ring *errRing
}

func (h ringHandler) Handle(ctx context.Context, r slog.Record) error {
	if r.Level >= slog.LevelWarn {
		var b strings.Builder
		fmt.Fprintf(&b, "%s %s %s", r.Time.Format(time.RFC3339), r.Level, r.Message)
		r.Attrs(func(a slog.Attr) bool {
			fmt.Fprintf(&b, " %s=%v", a.Key, a.Value)
			return true
		})
		h.ring.add(redactSecrets(b.String()))
	}
	return h.Handler.Handle(ctx, r)
}

func (h ringHandler) WithAttrs(a []slog.Attr) slog.Handler {
	return ringHandler{Handler: h.Handler.WithAttrs(a), ring: h.ring}
}

func (h ringHandler) WithGroup(n string) slog.Handler {
	return ringHandler{Handler: h.Handler.WithGroup(n), ring: h.ring}
}

// redactSecrets, metindeki zrv_live_/rpsh_live_ token'larini maskeler.
func redactSecrets(s string) string {
	for _, p := range []string{"zrv_live_", "rpsh_live_", "zorven_live_"} {
		for {
			i := strings.Index(s, p)
			if i < 0 {
				break
			}
			j := i + len(p)
			for j < len(s) && (s[j] == '_' || s[j] == '-' || (s[j] >= '0' && s[j] <= '9') || (s[j]|0x20 >= 'a' && s[j]|0x20 <= 'z')) {
				j++
			}
			s = s[:i] + "[token]" + s[j:]
		}
	}
	return s
}

// newDesktopLogger, dosyaya yazan slog.Logger ve halka doner. Dosya acilamazsa
// stderr'e duser (log kaybi uygulamayi durdurmaz).
func newDesktopLogger() (*slog.Logger, *errRing, io.Closer) {
	return newDesktopLoggerAt(logDir())
}

func newDesktopLoggerAt(dir string) (*slog.Logger, *errRing, io.Closer) {
	ring := &errRing{max: 20}
	var w io.Writer = os.Stderr
	var closer io.Closer = nopCloser{}
	if rf, err := newRotatingFile(filepath.Join(dir, logFileName), logMaxBytes, logKeep); err == nil {
		w = rf
		closer = rf
	}
	h := ringHandler{Handler: slog.NewTextHandler(w, nil), ring: ring}
	return slog.New(h), ring, closer
}

type nopCloser struct{}

func (nopCloser) Close() error { return nil }

// OpenLogFolder, gunluk klasorunu dosya gezgininde acar.
func (a *App) OpenLogFolder() string {
	dir := logDir()
	_ = os.MkdirAll(dir, 0o700)
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "windows":
		cmd = exec.Command("explorer.exe", dir)
	case "darwin":
		cmd = exec.Command("open", dir)
	default:
		cmd = exec.Command("xdg-open", dir)
	}
	if err := cmd.Start(); err != nil {
		return err.Error()
	}
	go func() { _ = cmd.Wait() }()
	return ""
}

// diagnosticsText, destek icin tanilama metni (surum, OS, durum, son hatalar;
// token yok).
func (a *App) diagnosticsText() string {
	a.mu.Lock()
	st := a.status
	cfg := a.cfg
	notice := a.cfgNotice
	svc := a.svc
	a.mu.Unlock()
	var b strings.Builder
	fmt.Fprintf(&b, "Zorven Desktop %s\n", version)
	fmt.Fprintf(&b, "OS: %s/%s, Go %s\n", runtime.GOOS, runtime.GOARCH, runtime.Version())
	fmt.Fprintf(&b, "Time: %s\n", time.Now().Format(time.RFC3339))
	fmt.Fprintf(&b, "Server: %s\n", cfg.ServerAddr)
	fmt.Fprintf(&b, "Signed in: %v\n", strings.TrimSpace(cfg.Token) != "")
	fmt.Fprintf(&b, "State: %s\n", st.State)
	if st.Message != "" {
		fmt.Fprintf(&b, "Message: %s\n", redactSecrets(st.Message))
	}
	fmt.Fprintf(&b, "Tunnels: %d\n", len(st.Tunnels))
	if svc.Found {
		fmt.Fprintf(&b, "Service/CLI agent: found (service=%v pid=%d version=%s connected=%v state=%s)\n", svc.Service, svc.PID, svc.Version, svc.Connected, svc.State)
	}
	if notice != nil {
		fmt.Fprintf(&b, "Config notice: %s (%s)\n", notice.Kind, redactSecrets(notice.Detail))
	}
	fmt.Fprintf(&b, "Log folder: %s\n", logDir())
	errs := a.errs.list()
	if len(errs) > 0 {
		b.WriteString("Recent warnings/errors:\n")
		for _, e := range errs {
			b.WriteString("  " + e + "\n")
		}
	}
	return b.String()
}

// CopyDiagnostics, tanilama metnini panoya kopyalar ve metni doner.
func (a *App) CopyDiagnostics() string {
	txt := a.diagnosticsText()
	_ = a.CopyClipboard(txt)
	return txt
}
