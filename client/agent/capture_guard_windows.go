//go:build windows

package agent

import (
	"log/slog"
	"os"
	"path/filepath"
	"sync"
	"time"

	"golang.org/x/sys/windows"
)

// captureVersion, sentinel kaydina yazilan istemci surumu (Agent.Run ayarlar).
var captureVersion = "dev"

var (
	procGuardOnce sync.Once
	procGuard     *dxgiGuard
	procGuardOK   bool
	procGuardWhy  string
)

// guardDir, sentinel dizini. Servis disinda (ve yardimci surecte) kullanici
// onbellek dizini; erisilemezse gecici dizin.
func guardDir() string {
	if d, err := os.UserCacheDir(); err == nil && d != "" {
		return filepath.Join(d, "zorven")
	}
	return filepath.Join(os.TempDir(), "zorven")
}

func pidAlive(pid int) bool {
	h, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, uint32(pid))
	if err != nil {
		// Erisim reddi surecin VAR oldugunu gosterir.
		return err == windows.ERROR_ACCESS_DENIED
	}
	defer windows.CloseHandle(h)
	var code uint32
	if windows.GetExitCodeProcess(h, &code) != nil {
		return true
	}
	return code == 259 // STILL_ACTIVE
}

// processGuard, surec-genel korumayi bir kez degerlendirir.
func processGuard() (*dxgiGuard, bool, string) {
	procGuardOnce.Do(func() {
		procGuard = newDXGIGuard(guardDir(), captureVersion, os.Getpid(), time.Now, pidAlive)
		procGuardOK, procGuardWhy = procGuard.Check()
		if !procGuardOK {
			slog.Warn("DXGI ekran yakalama atlandi (kbinani kullaniliyor)", "neden", procGuardWhy)
		}
	})
	return procGuard, procGuardOK, procGuardWhy
}

// winAtLeast8: Desktop Duplication Windows 8 (6.2) ve uzeri ister.
func winAtLeast8() bool {
	v := windows.RtlGetVersion()
	return v.MajorVersion > 6 || (v.MajorVersion == 6 && v.MinorVersion >= 2)
}

// shutdownCaptureGuard, temiz kapanista sentinel'i siler.
func shutdownCaptureGuard() {
	if procGuard != nil {
		procGuard.Clear()
	}
}
