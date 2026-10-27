//go:build windows

package main

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"

	"golang.org/x/sys/windows"
)

// runElevated, exe'yi UAC ("runas") ile yonetici olarak baslatir; beklemez.
func runElevated(exe, params string) error {
	verb, _ := windows.UTF16PtrFromString("runas")
	file, err := windows.UTF16PtrFromString(exe)
	if err != nil {
		return err
	}
	var args *uint16
	if params != "" {
		if args, err = windows.UTF16PtrFromString(params); err != nil {
			return err
		}
	}
	if err := windows.ShellExecute(0, verb, file, args, nil, windows.SW_HIDE); err != nil {
		if errors.Is(err, windows.ERROR_CANCELLED) {
			return errors.New(tm("net.adminDenied"))
		}
		return err
	}
	return nil
}

// findCLIExe, komut satiri istemcisini (zorven.exe) arar: Program Files, bu
// uygulamanin yani, PATH.
func findCLIExe() string {
	var cands []string
	if pf := os.Getenv("ProgramFiles"); pf != "" {
		cands = append(cands, filepath.Join(pf, "Zorven", "zorven.exe"))
	}
	if self, err := os.Executable(); err == nil {
		cands = append(cands, filepath.Join(filepath.Dir(self), "zorven.exe"))
	}
	for _, c := range cands {
		if fi, err := os.Stat(c); err == nil && !fi.IsDir() {
			return c
		}
	}
	if p, err := exec.LookPath("zorven.exe"); err == nil {
		return p
	}
	return ""
}

// processElevated, masaustu sureci yonetici (yukseltilmis token) olarak mi calisiyor.
func processElevated() bool { return windows.GetCurrentProcessToken().IsElevated() }
