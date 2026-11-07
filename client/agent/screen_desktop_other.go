//go:build !windows

package agent

func attachToInputDesktop() bool {
	return true
}

// ensureDPIAware yalniz Windows icin anlamlidir.
func ensureDPIAware() {}

// secureDesktopActive yalniz Windows icin anlamlidir.
func secureDesktopActive() bool { return false }
