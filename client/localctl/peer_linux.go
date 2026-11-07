//go:build linux

package localctl

import (
	"fmt"
	"net"
	"os"
	"syscall"
)

// checkPeer: es uid (veya root) zorunlu; dizin/soket izinlerine ek savunma.
func checkPeer(c net.Conn, _ Endpoint) error {
	uc, ok := c.(*net.UnixConn)
	if !ok {
		return nil
	}
	raw, err := uc.SyscallConn()
	if err != nil {
		return err
	}
	var cred *syscall.Ucred
	var gerr error
	if err := raw.Control(func(fd uintptr) {
		cred, gerr = syscall.GetsockoptUcred(int(fd), syscall.SOL_SOCKET, syscall.SO_PEERCRED)
	}); err != nil {
		return err
	}
	if gerr != nil {
		return gerr
	}
	if !uidAllowed(cred.Uid, uint32(os.Geteuid())) {
		return fmt.Errorf("uid %d (pid %d) izinli degil", cred.Uid, cred.Pid)
	}
	return nil
}
