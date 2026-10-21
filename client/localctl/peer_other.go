//go:build !linux && !windows

package localctl

import "net"

// Linux disi Unix: dizin 0700 + soket 0600 korumayi saglar.
func checkPeer(c net.Conn) error { return nil }
