//go:build windows

package localctl

import (
	"fmt"
	"net"
	"os"
	"path/filepath"

	"golang.org/x/sys/windows"
)

// Windows: AF_UNIX soketi; dizin DACL'i korumali (miras yok). Servis (SYSTEM)
// icin yalniz SYSTEM + Administrators; kullanici ajani icin ek olarak mevcut
// kullanicinin SID'i. Karar: servis kontrolu yonetici (elevated) gerektirir.
func listen(ep Endpoint) (net.Listener, error) {
	dir := filepath.Dir(ep.Path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	sddl := "D:PAI(A;OICI;FA;;;SY)(A;OICI;FA;;;BA)"
	if !ep.Service {
		u, err := windows.GetCurrentProcessToken().GetTokenUser()
		if err != nil {
			return nil, err
		}
		sddl += fmt.Sprintf("(A;OICI;FA;;;%s)", u.User.Sid.String())
	}
	sd, err := windows.SecurityDescriptorFromString(sddl)
	if err != nil {
		return nil, err
	}
	dacl, _, err := sd.DACL()
	if err != nil {
		return nil, err
	}
	if err := windows.SetNamedSecurityInfo(dir, windows.SE_FILE_OBJECT,
		windows.DACL_SECURITY_INFORMATION|windows.PROTECTED_DACL_SECURITY_INFORMATION,
		nil, nil, dacl, nil); err != nil {
		return nil, fmt.Errorf("dizin ACL'i ayarlanamadi: %w", err)
	}
	if c, err := net.Dial("unix", ep.Path); err == nil {
		_ = c.Close()
		return nil, fmt.Errorf("kontrol soketi baska bir ajanda kullanimda: %s", ep.Path)
	}
	_ = os.Remove(ep.Path)
	return net.Listen("unix", ep.Path)
}

// Kimlik dogrulama dizin ACL'ine dayanir (erisemeyen baglanamaz).
func checkPeer(c net.Conn) error { return nil }
