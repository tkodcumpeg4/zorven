//go:build windows

package localctl

import (
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"sync"
	"syscall"
	"time"

	"golang.org/x/sys/windows"

	"github.com/tkodcumpeg4/zorven/client/config"
)

// Minimal named pipe aktarimi (bagimliliksiz). Bagli tutamaclar senkron
// (non-overlapped) olup protokol istek->yanit seklinde sirali oldugundan yeterlidir.

type pipeAddr string

func (a pipeAddr) Network() string { return "pipe" }
func (a pipeAddr) String() string  { return string(a) }

type pipeConn struct {
	h      windows.Handle
	name   string
	server bool

	mu      sync.Mutex
	closed  bool
	wrote   bool
	timer   *time.Timer
	timeout bool
}

type timeoutErr struct{}

func (timeoutErr) Error() string   { return "i/o timeout" }
func (timeoutErr) Timeout() bool   { return true }
func (timeoutErr) Temporary() bool { return true }

func (p *pipeConn) Read(b []byte) (int, error) {
	if len(b) == 0 {
		return 0, nil
	}
	var n uint32
	err := windows.ReadFile(p.h, b, &n, nil)
	if err != nil {
		switch {
		case errors.Is(err, windows.ERROR_BROKEN_PIPE), errors.Is(err, windows.ERROR_PIPE_NOT_CONNECTED):
			return int(n), io.EOF
		case errors.Is(err, windows.ERROR_OPERATION_ABORTED):
			return int(n), timeoutErr{}
		case errors.Is(err, windows.ERROR_MORE_DATA):
			return int(n), nil
		}
		return int(n), err
	}
	return int(n), nil
}

func (p *pipeConn) Write(b []byte) (int, error) {
	total := 0
	for total < len(b) {
		var n uint32
		if err := windows.WriteFile(p.h, b[total:], &n, nil); err != nil {
			if errors.Is(err, windows.ERROR_OPERATION_ABORTED) {
				return total, timeoutErr{}
			}
			return total, err
		}
		total += int(n)
	}
	p.mu.Lock()
	p.wrote = true
	p.mu.Unlock()
	return total, nil
}

func (p *pipeConn) Close() error {
	p.mu.Lock()
	if p.closed {
		p.mu.Unlock()
		return nil
	}
	p.closed = true
	if p.timer != nil {
		p.timer.Stop()
	}
	wrote := p.wrote
	p.mu.Unlock()
	if p.server {
		if wrote {
			_ = windows.FlushFileBuffers(p.h) // istemci yaniti okuyana kadar bekler
		}
		_ = windows.DisconnectNamedPipe(p.h)
	}
	return windows.CloseHandle(p.h)
}

func (p *pipeConn) LocalAddr() net.Addr  { return pipeAddr(p.name) }
func (p *pipeConn) RemoteAddr() net.Addr { return pipeAddr(p.name) }

// SetDeadline: sure dolunca bekleyen G/C iptal edilir (CancelIoEx).
func (p *pipeConn) SetDeadline(t time.Time) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.timer != nil {
		p.timer.Stop()
		p.timer = nil
	}
	if t.IsZero() || p.closed {
		return nil
	}
	h := p.h
	p.timer = time.AfterFunc(time.Until(t), func() {
		p.mu.Lock()
		closed := p.closed
		p.mu.Unlock()
		if !closed {
			_ = windows.CancelIoEx(h, nil)
		}
	})
	return nil
}
func (p *pipeConn) SetReadDeadline(t time.Time) error  { return p.SetDeadline(t) }
func (p *pipeConn) SetWriteDeadline(t time.Time) error { return p.SetDeadline(t) }

// ---- dinleyici ----

type pipeListener struct {
	name string
	sa   *windows.SecurityAttributes

	mu      sync.Mutex
	pending windows.Handle
	closed  bool
}

func createInstance(name string, sa *windows.SecurityAttributes, first bool) (windows.Handle, error) {
	np, err := windows.UTF16PtrFromString(name)
	if err != nil {
		return 0, err
	}
	flags := uint32(windows.PIPE_ACCESS_DUPLEX)
	if first {
		flags |= windows.FILE_FLAG_FIRST_PIPE_INSTANCE
	}
	return windows.CreateNamedPipe(np, flags,
		windows.PIPE_TYPE_BYTE|windows.PIPE_READMODE_BYTE|windows.PIPE_WAIT|windows.PIPE_REJECT_REMOTE_CLIENTS,
		windows.PIPE_UNLIMITED_INSTANCES, 64*1024, 64*1024, 0, sa)
}

func (l *pipeListener) Accept() (net.Conn, error) {
	l.mu.Lock()
	if l.closed {
		l.mu.Unlock()
		return nil, net.ErrClosed
	}
	h := l.pending
	l.mu.Unlock()

	err := windows.ConnectNamedPipe(h, nil)
	if err != nil && !errors.Is(err, windows.ERROR_PIPE_CONNECTED) {
		l.mu.Lock()
		closed := l.closed
		l.mu.Unlock()
		if closed {
			_ = windows.CloseHandle(h)
			return nil, net.ErrClosed
		}
		return nil, err
	}
	l.mu.Lock()
	if l.closed {
		l.mu.Unlock()
		_ = windows.CloseHandle(h)
		return nil, net.ErrClosed
	}
	next, nerr := createInstance(l.name, l.sa, false)
	if nerr != nil {
		l.mu.Unlock()
		_ = windows.CloseHandle(h)
		return nil, nerr
	}
	l.pending = next
	l.mu.Unlock()
	return &pipeConn{h: h, name: l.name, server: true}, nil
}

func (l *pipeListener) Close() error {
	l.mu.Lock()
	if l.closed {
		l.mu.Unlock()
		return nil
	}
	l.closed = true
	l.mu.Unlock()
	// Bekleyen ConnectNamedPipe'i uyandirmak icin kendimiz baglaniriz.
	if c, err := dialPipe(l.name, time.Second); err == nil {
		_ = c.Close()
	}
	return nil
}

func (l *pipeListener) Addr() net.Addr { return pipeAddr(l.name) }

// ---- istemci ----

func dialPipe(name string, timeout time.Duration) (net.Conn, error) {
	np, err := windows.UTF16PtrFromString(name)
	if err != nil {
		return nil, err
	}
	deadline := time.Now().Add(timeout)
	for {
		// SECURITY_ANONYMOUS: sunucu bizi taklit edemez.
		// GENERIC_WRITE istenmez: FILE_APPEND_DATA (kanalda FILE_CREATE_PIPE_INSTANCE)
		// icerir ve Authenticated Users'a verilmez (bkz. pipeAccessAuthUsers).
		h, err := windows.CreateFile(np, windows.GENERIC_READ|windows.FILE_WRITE_DATA, 0, nil,
			windows.OPEN_EXISTING, windows.SECURITY_SQOS_PRESENT|windows.SECURITY_ANONYMOUS, 0)
		if err == nil {
			return &pipeConn{h: h, name: name}, nil
		}
		if errors.Is(err, windows.ERROR_PIPE_BUSY) && time.Now().Before(deadline) {
			time.Sleep(20 * time.Millisecond)
			continue
		}
		return nil, err
	}
}

func dial(ep Endpoint, timeout time.Duration) (net.Conn, error) {
	c, err := dialPipe(pipeName(ep.Path), timeout)
	if err == nil {
		return c, nil
	}
	// Kanal var ama erisim reddedildi: eski AF_UNIX yoluna DUSME (yaniltici yol
	// hatasi); gercek hatayi tipli olarak don.
	if errors.Is(err, windows.ERROR_ACCESS_DENIED) {
		return nil, fmt.Errorf("%w: %v", ErrAccessDenied, err)
	}
	// Eski ajan (AF_UNIX dosyasi) ile geriye uyum: yalniz kanal YOKSA.
	if absent(err) {
		if _, serr := os.Stat(ep.Path); serr == nil {
			return net.DialTimeout("unix", ep.Path, timeout)
		}
	}
	return nil, err
}

// present: Windows'ta pipe varligi dogrudan sorulamaz; Call denemesi yapilir
// (yoksa absent() ile atlanir).
func present(ep Endpoint) bool { return true }

func absent(err error) bool {
	return errors.Is(err, os.ErrNotExist) || errors.Is(err, windows.ERROR_FILE_NOT_FOUND) ||
		errors.Is(err, windows.ERROR_PATH_NOT_FOUND) || errors.Is(err, syscall.ENOENT)
}

// ---- es dogrulama ----

func tokenSIDs(t windows.Token) (user string, enabled []string, err error) {
	u, err := t.GetTokenUser()
	if err != nil {
		return "", nil, err
	}
	g, err := t.GetTokenGroups()
	if err != nil {
		return "", nil, err
	}
	const denyOnly = 0x10 // SE_GROUP_USE_FOR_DENY_ONLY
	for _, a := range g.AllGroups() {
		if a.Attributes&windows.SE_GROUP_ENABLED != 0 && a.Attributes&denyOnly == 0 {
			enabled = append(enabled, a.Sid.String())
		}
	}
	return u.User.Sid.String(), enabled, nil
}

// peerAccess: istemci surecinin token'ini okuyup SID politikasini uygular ve
// erisim duzeyini doner. Servis kanalinda yetkisiz yerel kullanici salt okunur
// duzeyde kabul edilir (bkz. sidAccess); kullanici ajaninda reddedilir.
func peerAccess(c net.Conn, ep Endpoint) (access, error) {
	pc, ok := c.(*pipeConn)
	if !ok {
		return accessFull, nil // eski AF_UNIX baglantisi: dizin ACL'ine guvenilir (donem uyumu)
	}
	var pid uint32
	if err := windows.GetNamedPipeClientProcessId(pc.h, &pid); err != nil {
		return accessNone, fmt.Errorf("istemci pid okunamadi: %w", err)
	}
	ph, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, pid)
	if err != nil {
		return accessNone, fmt.Errorf("istemci sureci (pid %d) acilamadi: %w", pid, err)
	}
	defer windows.CloseHandle(ph)
	var tok windows.Token
	if err := windows.OpenProcessToken(ph, windows.TOKEN_QUERY, &tok); err != nil {
		return accessNone, fmt.Errorf("istemci token'i (pid %d) acilamadi: %w", pid, err)
	}
	defer tok.Close()
	sid, groups, err := tokenSIDs(tok)
	if err != nil {
		return accessNone, fmt.Errorf("istemci SID'i (pid %d): %w", pid, err)
	}
	self, _, err := tokenSIDs(windows.GetCurrentProcessToken())
	if err != nil {
		return accessNone, err
	}
	var operators []string
	if ep.Service && !sidAllowed(sid, groups, self) {
		// Her baglantida okunur (kucuk dosya); servis SYSTEM oldugundan okuyabilir.
		cfg, _ := config.LoadFile(config.SystemPath())
		operators = cfg.Operators
	}
	acc := sidAccess(sid, groups, self, operators, ep.Service)
	if acc == accessNone {
		return accessNone, fmt.Errorf("SID %s (pid %d) izinli degil", sid, pid)
	}
	return acc, nil
}
