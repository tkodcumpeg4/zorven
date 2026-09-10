//go:build windows

package agent

import (
	"syscall"
)

var (
	modUser32Screen      = syscall.NewLazyDLL("user32.dll")
	procOpenInputDesktop = modUser32Screen.NewProc("OpenInputDesktop")
	procSetThreadDesktop = modUser32Screen.NewProc("SetThreadDesktop")
)

// attachToInputDesktop, calisan thread'i kullanicinin aktif masaustune (Default) baglar.
// Windows'ta arka plan islemleri veya CLI araclari farkli bir masaustunde calisiyor
// olabilir; aktif girdi masaustune gecilmezse BitBlt "The handle is invalid" hatasiyla
// basarisiz olur.
func attachToInputDesktop() bool {
	hInputDesk, _, _ := procOpenInputDesktop.Call(0, 0, 0x01FF) // GENERIC_ALL
	if hInputDesk == 0 {
		return false
	}
	ret, _, _ := procSetThreadDesktop.Call(hInputDesk)
	return ret != 0
}
