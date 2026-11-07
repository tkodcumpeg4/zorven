//go:build !windows

package main

import "os/exec"

// Kurulum/kaldirma akislari yalniz Windows'ta vardir; macOS/Linux davranisi degismez.
func handleSetupCommands([]string) (bool, int) { return false, 0 }

// runHidden, komutu calistirip cikis kodunu doner (Windows disinda gizleme gerekmez).
func runHidden(exe string, args ...string) (int, error) {
	err := exec.Command(exe, args...).Run()
	if ee, ok := err.(*exec.ExitError); ok {
		return ee.ExitCode(), nil
	}
	if err != nil {
		return -1, err
	}
	return 0, nil
}
