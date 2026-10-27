//go:build windows

package agent

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"sync"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

// Servis tarafi (SYSTEM, oturum 0): kullanici oturumunda ekran yardimcisini
// baslatir ve ona yalniz SYSTEM + o kullanicinin erisebildigi bir named pipe ile baglanir.

// winPipe, senkron (overlapped olmayan) pipe tutamacini io.ReadWriteCloser yapar.
// Istek/yanit kilit adimli oldugundan ayni tutamacta es zamanli G/C olmaz;
// Close, bekleyen okumayi CancelIoEx ile iptal eder.
type winPipe struct {
	h      windows.Handle
	server bool
	once   sync.Once
}

func (p *winPipe) Read(b []byte) (int, error) {
	for {
		var n uint32
		err := windows.ReadFile(p.h, b, &n, nil)
		if err != nil {
			if errors.Is(err, windows.ERROR_BROKEN_PIPE) || errors.Is(err, windows.ERROR_OPERATION_ABORTED) ||
				errors.Is(err, windows.ERROR_PIPE_NOT_CONNECTED) || errors.Is(err, windows.ERROR_INVALID_HANDLE) {
				return int(n), io.EOF
			}
			return int(n), err
		}
		if n > 0 || len(b) == 0 {
			return int(n), nil
		}
	}
}

func (p *winPipe) Write(b []byte) (int, error) {
	total := 0
	for total < len(b) {
		var n uint32
		if err := windows.WriteFile(p.h, b[total:], &n, nil); err != nil {
			return total, err
		}
		total += int(n)
	}
	return total, nil
}

func (p *winPipe) Close() error {
	p.once.Do(func() {
		_ = windows.CancelIoEx(p.h, nil)
		if p.server {
			_ = windows.DisconnectNamedPipe(p.h)
		}
		_ = windows.CloseHandle(p.h)
	})
	return nil
}

func randHex(nBytes int) (string, error) {
	b := make([]byte, nBytes)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

// winHelperConnector, helperConnector'in Windows uygulamasi.
type winHelperConnector struct {
	mu   sync.Mutex
	proc windows.Handle
	sess uint32
}

const (
	noActiveSession   = 0xFFFFFFFF
	helperSpawnWait   = 15 * time.Second
	helperHelloWait   = 5 * time.Second
	helperKillGrace   = 3 * time.Second
	stillActiveExit   = 259
	swHide            = 0
	startfUseShowWnd  = 0x1
	helperDesktopName = `winsta0\default`
)

func (w *winHelperConnector) Stale() bool {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.proc != 0 && windows.WTSGetActiveConsoleSessionId() != w.sess
}

// Shutdown, onceki yardimci sureci (varsa) kisa bir sure sonra sonlandirir;
// kanal kapandiginda yardimci zaten kendiliginden cikar.
func (w *winHelperConnector) Shutdown() {
	w.mu.Lock()
	h := w.proc
	w.proc = 0
	w.mu.Unlock()
	if h == 0 {
		return
	}
	go func() {
		if ev, _ := windows.WaitForSingleObject(h, uint32(helperKillGrace/time.Millisecond)); ev != windows.WAIT_OBJECT_0 {
			_ = windows.TerminateProcess(h, 1)
		}
		_ = windows.CloseHandle(h)
	}()
}

func (w *winHelperConnector) Connect() (io.ReadWriteCloser, error) {
	w.Shutdown()
	sess := windows.WTSGetActiveConsoleSessionId()
	if sess == noActiveSession || sess == 0 {
		return nil, errNoInteractiveUser
	}
	var tok windows.Token
	if err := windows.WTSQueryUserToken(sess, &tok); err != nil {
		if errors.Is(err, windows.ERROR_NO_TOKEN) {
			return nil, errNoInteractiveUser
		}
		return nil, fmt.Errorf("WTSQueryUserToken: %w", err)
	}
	defer tok.Close()
	tu, err := tok.GetTokenUser()
	if err != nil {
		return nil, fmt.Errorf("GetTokenUser: %w", err)
	}
	sddl, err := helperPipeSDDL(tu.User.Sid.String())
	if err != nil {
		return nil, err
	}
	sd, err := windows.SecurityDescriptorFromString(sddl)
	if err != nil {
		return nil, fmt.Errorf("SDDL: %w", err)
	}
	sa := &windows.SecurityAttributes{Length: uint32(unsafe.Sizeof(windows.SecurityAttributes{})), SecurityDescriptor: sd}

	id, err := randHex(12)
	if err != nil {
		return nil, err
	}
	secret, err := randHex(helperTokenLen / 2)
	if err != nil {
		return nil, err
	}
	name := helperPipePf + id
	namePtr, err := windows.UTF16PtrFromString(name)
	if err != nil {
		return nil, err
	}
	ph, err := windows.CreateNamedPipe(namePtr,
		windows.PIPE_ACCESS_DUPLEX|windows.FILE_FLAG_FIRST_PIPE_INSTANCE,
		windows.PIPE_TYPE_BYTE|windows.PIPE_READMODE_BYTE|windows.PIPE_WAIT|windows.PIPE_REJECT_REMOTE_CLIENTS,
		1, 1<<20, 1<<20, 0, sa)
	if err != nil {
		return nil, fmt.Errorf("CreateNamedPipe: %w", err)
	}
	pipe := &winPipe{h: ph, server: true}
	ok := false
	defer func() {
		if !ok {
			pipe.Close()
		}
	}()

	proc, pid, err := spawnHelperAsUser(tok, name, secret)
	if err != nil {
		return nil, err
	}
	kill := func() {
		_ = windows.TerminateProcess(proc, 1)
		_ = windows.CloseHandle(proc)
	}

	// Baglanti bekle: yardimci erken cikarsa ya da zaman asimina ugrarsa vazgec.
	done := make(chan error, 1)
	go func() {
		err := windows.ConnectNamedPipe(ph, nil)
		if errors.Is(err, windows.ERROR_PIPE_CONNECTED) {
			err = nil
		}
		done <- err
	}()
	deadline := time.After(helperSpawnWait)
	tick := time.NewTicker(250 * time.Millisecond)
	defer tick.Stop()
wait:
	for {
		select {
		case err := <-done:
			if err != nil {
				kill()
				return nil, fmt.Errorf("ConnectNamedPipe: %w", err)
			}
			break wait
		case <-tick.C:
			var code uint32
			if windows.GetExitCodeProcess(proc, &code) == nil && code != stillActiveExit {
				_ = windows.CancelIoEx(ph, nil)
				<-done
				_ = windows.CloseHandle(proc)
				return nil, fmt.Errorf("screen helper exited early (code %d)", code)
			}
		case <-deadline:
			_ = windows.CancelIoEx(ph, nil)
			<-done
			kill()
			return nil, errors.New("screen helper did not connect in time")
		}
	}

	// Karsi uc, az once baslattigimiz surec olmali (baska yerel surec degil).
	var cpid uint32
	if err := windows.GetNamedPipeClientProcessId(ph, &cpid); err != nil || cpid != pid {
		kill()
		return nil, errors.New("unexpected pipe client")
	}
	// Token el sikismasi (zaman asimli).
	t := time.AfterFunc(helperHelloWait, func() { pipe.Close() })
	_, _, herr := acceptHandshake(pipe, secret)
	if !t.Stop() || herr != nil {
		kill()
		if herr == nil {
			herr = errors.New("helper handshake timed out")
		}
		return nil, herr
	}
	w.mu.Lock()
	w.proc, w.sess = proc, sess
	w.mu.Unlock()
	ok = true
	return pipe, nil
}

// spawnHelperAsUser, kullanici tokeniyle winsta0\default masaustunde
// "zorven screen-helper --pipe <ad> --token <gizli>" baslatir.
func spawnHelperAsUser(userTok windows.Token, pipeName, secret string) (windows.Handle, uint32, error) {
	exe, err := os.Executable()
	if err != nil {
		return 0, 0, err
	}
	var dup windows.Token
	if err := windows.DuplicateTokenEx(userTok, windows.MAXIMUM_ALLOWED, nil,
		windows.SecurityImpersonation, windows.TokenPrimary, &dup); err != nil {
		return 0, 0, fmt.Errorf("DuplicateTokenEx: %w", err)
	}
	defer dup.Close()
	var env *uint16
	if err := windows.CreateEnvironmentBlock(&env, dup, false); err != nil {
		return 0, 0, fmt.Errorf("CreateEnvironmentBlock: %w", err)
	}
	defer windows.DestroyEnvironmentBlock(env)

	cmd, err := windows.UTF16PtrFromString(windows.ComposeCommandLine([]string{exe, "screen-helper", "--pipe", pipeName, "--token", secret}))
	if err != nil {
		return 0, 0, err
	}
	desk, _ := windows.UTF16PtrFromString(helperDesktopName)
	si := windows.StartupInfo{Cb: uint32(unsafe.Sizeof(windows.StartupInfo{})), Desktop: desk, Flags: startfUseShowWnd, ShowWindow: swHide}
	var pi windows.ProcessInformation
	if err := windows.CreateProcessAsUser(dup, nil, cmd, nil, nil, false,
		windows.CREATE_UNICODE_ENVIRONMENT|windows.CREATE_NO_WINDOW, env, nil, &si, &pi); err != nil {
		return 0, 0, fmt.Errorf("CreateProcessAsUser: %w", err)
	}
	_ = windows.CloseHandle(pi.Thread)
	return pi.Process, pi.ProcessId, nil
}
