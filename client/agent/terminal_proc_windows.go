//go:build windows

package agent

import (
	"os"
	"os/exec"
	"strconv"
	"time"
)

// killProcessTree, kabuk agacini `taskkill /T /F` ile sonlandirir (grace
// Windows'ta kullanilmaz). Ebeveyn hayattayken calismalidir; sonra Kill yedegi.
func killProcessTree(p *os.Process, _ time.Duration) {
	if p == nil || p.Pid <= 0 {
		return
	}
	c := hideConsole(exec.Command("taskkill", "/T", "/F", "/PID", strconv.Itoa(p.Pid)))
	done := make(chan struct{})
	go func() { _ = c.Run(); close(done) }()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		if c.Process != nil {
			_ = c.Process.Kill()
		}
	}
	_ = p.Kill()
}

func signalExitCode(_ *os.ProcessState) int { return 1 }
