//go:build !windows

package main

import (
	"errors"
	"os"
	"syscall"
	"time"
)

// currentUserSID: SID kavrami yalniz Windows'ta vardir.
func currentUserSID() string { return "" }

// runElevatedWait: yukseltilmis calistirma yalniz Windows'ta vardir.
func runElevatedWait(string, string, time.Duration) (int, error) {
	return -1, errors.New(tm("net.elevUnsupported"))
}

// waitPIDExit, pid'li surecin bitmesini en cok timeout kadar yoklayarak bekler.
func waitPIDExit(pid int, timeout time.Duration) {
	p, err := os.FindProcess(pid)
	if err != nil {
		return
	}
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if p.Signal(syscall.Signal(0)) != nil {
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
}
