//go:build !windows

package localctl

import (
	"errors"
	"fmt"
	"net"
	"os"
	"time"
)

func dial(ep Endpoint, timeout time.Duration) (net.Conn, error) {
	c, err := net.DialTimeout("unix", ep.Path, timeout)
	if err != nil && errors.Is(err, os.ErrPermission) {
		return nil, fmt.Errorf("%w: %v", ErrAccessDenied, err)
	}
	return c, err
}

// present: uc nokta (soket dosyasi) var mi.
func present(ep Endpoint) bool {
	_, err := os.Stat(ep.Path)
	return err == nil
}

func absent(err error) bool { return errors.Is(err, os.ErrNotExist) }
