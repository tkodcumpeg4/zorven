//go:build !windows

package main

import (
	"errors"
	"os"
)

func runElevated(string, string) error {
	return errors.New(tm("net.elevUnsupported"))
}

func findCLIExe() string { return "" }

// processElevated, bu surec TUN acabilecek yetkide mi (Unix: root).
func processElevated() bool { return os.Geteuid() == 0 }
