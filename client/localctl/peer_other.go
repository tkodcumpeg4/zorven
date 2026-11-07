//go:build !linux && !windows && !darwin

package localctl

import "net"

// Diger Unix: dizin 0700 + soket 0600 korumayi saglar.
func checkPeer(c net.Conn, _ Endpoint) error { return nil }
