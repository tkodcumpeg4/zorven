//go:build windows

package agent

import (
	"log/slog"
	"os/exec"
	"syscall"
)

// hideConsole, alt sureci konsol penceresi acmadan baslatir. Masaustu (GUI)
// uygulamasinda gomulu ajan powershell/reg/netsh/ffmpeg gibi konsol programlarini
// calistirdiginda ekranda yanip sonen terminal pencereleri cikmasin.
func hideConsole(c *exec.Cmd) *exec.Cmd {
	const createNoWindow = 0x08000000
	if c.SysProcAttr == nil {
		c.SysProcAttr = &syscall.SysProcAttr{}
	}
	c.SysProcAttr.HideWindow = true
	c.SysProcAttr.CreationFlags |= createNoWindow
	// Tani: hangi programin calistigi masaustu gunlugunde gorunsun (konsol
	// penceresi sikayetlerinde kaynagi bulmak icin; argumanlar yazilmaz).
	slog.Info("alt surec", "program", c.Path)
	return c
}
