//go:build !windows

package agent

func attachToInputDesktop() bool {
	return true
}
