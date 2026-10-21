//go:build !windows

package localctl

import (
	"fmt"
	"net"
	"os"
	"path/filepath"
	"syscall"
)

func listen(ep Endpoint) (net.Listener, error) {
	dir := filepath.Dir(ep.Path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	_ = os.Chmod(dir, 0o700)
	// Eski/olu soketi temizle (baska bir ajan canliysa dinlemeyi reddet).
	if c, err := net.Dial("unix", ep.Path); err == nil {
		_ = c.Close()
		return nil, fmt.Errorf("kontrol soketi baska bir ajanda kullanimda: %s", ep.Path)
	}
	_ = os.Remove(ep.Path)
	old := syscall.Umask(0o177)
	ln, err := net.Listen("unix", ep.Path)
	syscall.Umask(old)
	if err != nil {
		return nil, err
	}
	_ = os.Chmod(ep.Path, 0o600)
	return ln, nil
}
