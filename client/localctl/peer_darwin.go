//go:build darwin

package localctl

import (
	"fmt"
	"net"
	"os"

	"golang.org/x/sys/unix"
)

// checkPeer: LOCAL_PEERCRED ile es uid denetimi (es uid veya root).
func checkPeer(c net.Conn, _ Endpoint) error {
	uc, ok := c.(*net.UnixConn)
	if !ok {
		return nil
	}
	raw, err := uc.SyscallConn()
	if err != nil {
		return err
	}
	var cred *unix.Xucred
	var gerr error
	if err := raw.Control(func(fd uintptr) {
		cred, gerr = unix.GetsockoptXucred(int(fd), unix.SOL_LOCAL, unix.LOCAL_PEERCRED)
	}); err != nil {
		return err
	}
	if gerr != nil {
		return gerr
	}
	if !uidAllowed(cred.Uid, uint32(os.Geteuid())) {
		return fmt.Errorf("uid %d izinli degil", cred.Uid)
	}
	return nil
}
