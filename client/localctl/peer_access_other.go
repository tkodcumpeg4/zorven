//go:build !windows

package localctl

import "net"

// peerAccess: Windows disinda kabul edilen es her zaman tam yetkilidir
// (uid denetimi checkPeer'de; dizin/soket izinleri ek savunma).
func peerAccess(c net.Conn, ep Endpoint) (access, error) {
	if err := checkPeer(c, ep); err != nil {
		return accessNone, err
	}
	return accessFull, nil
}
