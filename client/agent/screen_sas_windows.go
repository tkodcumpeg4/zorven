//go:build windows

package agent

import (
	"github.com/tkodcumpeg4/zorven/shared/protocol"
	"golang.org/x/sys/windows"
)

// Ctrl+Alt+Del (guvenli dikkat dizisi). SendInput ile uretilemez; sas.dll
// SendSAS tek yoldur ve YALNIZCA SYSTEM/servis baglaminda ve
// "SoftwareSASGeneration" politikasi acikken calisir. SendSAS bir sonuc
// dondurmedigi icin onkosullari biz denetler.

const sasNeedsServiceDetail = "Ctrl+Alt+Del icin servis olarak calisma gerekli"

var (
	sasDLL      = windows.NewLazySystemDLL("sas.dll")
	procSendSAS = sasDLL.NewProc("SendSAS")
)

// runningAsSystem, surec LocalSystem hesabiyla mi calisiyor (servis).
func runningAsSystem() bool {
	tu, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		return false
	}
	sys, err := windows.CreateWellKnownSid(windows.WinLocalSystemSid)
	if err != nil {
		return false
	}
	return tu.User.Sid.Equals(sys)
}

// sendSAS, Ctrl+Alt+Del uretir; basarisizlik girdi durumu olarak doner.
func sendSAS() inputResult {
	if !runningAsSystem() {
		return inputResult{State: protocol.InputStateError, Detail: sasNeedsServiceDetail}
	}
	if err := procSendSAS.Find(); err != nil {
		return inputResult{State: protocol.InputStateError, Detail: "sas.dll bulunamadi"}
	}
	procSendSAS.Call(0) // AsUser=FALSE: servis oturumundan konsola gonderir
	return inputResult{State: protocol.InputStateOK}
}
