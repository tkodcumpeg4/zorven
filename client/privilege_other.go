//go:build !windows

package main

import (
	"errors"
	"os"
)

func isAdmin() bool { return os.Geteuid() == 0 }

func isUACDeclined(error) bool { return false }

// runElevatedWait yalniz Windows'ta anlamlidir; Unix'te sudo kullanilir.
func runElevatedWait(string, string) (uint32, error) {
	return 0, errors.New("not supported on this platform")
}
