//go:build !windows

package agent

import (
	"os"
	"os/exec"
	"syscall"
	"time"
)

// killProcessTree, kabuk ve tum alt sureclerini sonlandirir. go-pty kabugu
// Setsid ile baslatir (oturum lideri => pgid == pid); bu yuzden kill(-pid)
// butun grubu kapsar. Once SIGHUP, grace sonra hala duran varsa SIGKILL.
// Engellemez: SIGKILL gecikmeli goroutine'de gonderilir.
func killProcessTree(p *os.Process, grace time.Duration) {
	if p == nil || p.Pid <= 0 {
		return
	}
	pgid := p.Pid
	if err := syscall.Kill(-pgid, syscall.SIGHUP); err != nil {
		// Grup yoksa tek surece dus.
		_ = p.Signal(syscall.SIGHUP)
	}
	go func() {
		t := time.NewTimer(grace)
		defer t.Stop()
		<-t.C
		_ = syscall.Kill(-pgid, syscall.SIGKILL)
		_ = p.Kill()
	}()
}

// signalExitCode, sinyalle olen sureci 128+sinyal olarak raporlar.
func signalExitCode(ps *os.ProcessState) int {
	if ws, ok := ps.Sys().(syscall.WaitStatus); ok && ws.Signaled() {
		return 128 + int(ws.Signal())
	}
	return 1
}

// setNewProcessGroup, exec.Cmd'yi kendi surec grubunda baslatir (testler ve
// PTY'siz kullanimlar icin; PTY yolu zaten Setsid kullanir).
func setNewProcessGroup(c *exec.Cmd) {
	if c.SysProcAttr == nil {
		c.SysProcAttr = &syscall.SysProcAttr{}
	}
	c.SysProcAttr.Setpgid = true
}
