//go:build windows

package localctl

import (
	"fmt"
	"net"
	"unsafe"

	"golang.org/x/sys/windows"
)

// Windows: named pipe + acik DACL. Servis (SYSTEM) icin yalniz SYSTEM +
// Administrators; kullanici ajani icin ek olarak mevcut kullanicinin SID'i
// (onceki dizin-DACL politikasi ile ayni). Baglanan istemci ayrica
// checkPeer'de token SID'i ile dogrulanir. Uzak istemciler reddedilir.
func listen(ep Endpoint) (net.Listener, error) {
	sddl := "D:P(A;;GA;;;SY)(A;;GA;;;BA)"
	if ep.Service {
		// Servis kanali: oturum acmis her yerel kullanici baglanabilir (okuma/yazma,
		// FILE_GENERIC_READ|WRITE); yetki baglanan surecin token SID'inden gelir
		// (peerAccess): yonetici/SYSTEM tam, operator SID'leri masaustu islemleri,
		// digerleri yalniz suzulmus durum. Uzak istemciler PIPE_REJECT_REMOTE_CLIENTS
		// ile reddedilir.
		sddl += fmt.Sprintf("(A;;0x%x;;;AU)", pipeAccessAuthUsers)
	} else {
		u, err := windows.GetCurrentProcessToken().GetTokenUser()
		if err != nil {
			return nil, err
		}
		sddl += fmt.Sprintf("(A;;GA;;;%s)", u.User.Sid.String())
	}
	sd, err := windows.SecurityDescriptorFromString(sddl)
	if err != nil {
		return nil, err
	}
	sa := &windows.SecurityAttributes{SecurityDescriptor: sd}
	sa.Length = uint32(unsafe.Sizeof(*sa))
	name := pipeName(ep.Path)
	h, err := createInstance(name, sa, true)
	if err != nil {
		if err == windows.ERROR_ACCESS_DENIED {
			return nil, fmt.Errorf("kontrol kanali baska bir ajanda kullanimda: %s", name)
		}
		return nil, err
	}
	return &pipeListener{name: name, sa: sa, pending: h}, nil
}
