//go:build windows

package agent

import (
	"fmt"
	"os"
	"strings"
	"unsafe"

	"golang.org/x/sys/windows"
)

// verifyAuthenticode, dosyanin Authenticode imzasini WinVerifyTrust ile dogrular
// (iptal denetimi kapali: cevrimdisi makinede yanlis red olmasin; asil kimlik
// guvencesi imzali manifestteki sha256'dir). Bu ek bir katmandir; yerel guven
// deposu sorunlari icin ZORVEN_SKIP_AUTHENTICODE=1 ile atlanabilir.
func verifyAuthenticode(path string) error {
	if os.Getenv("ZORVEN_SKIP_AUTHENTICODE") == "1" {
		return nil
	}
	p16, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return err
	}
	fi := &windows.WinTrustFileInfo{Size: uint32(unsafe.Sizeof(windows.WinTrustFileInfo{})), FilePath: p16}
	data := &windows.WinTrustData{
		Size:                            uint32(unsafe.Sizeof(windows.WinTrustData{})),
		UIChoice:                        windows.WTD_UI_NONE,
		RevocationChecks:                windows.WTD_REVOKE_NONE,
		UnionChoice:                     windows.WTD_CHOICE_FILE,
		StateAction:                     windows.WTD_STATEACTION_VERIFY,
		FileOrCatalogOrBlobOrSgnrOrCert: unsafe.Pointer(fi),
	}
	verr := windows.WinVerifyTrustEx(windows.InvalidHWND, &windows.WINTRUST_ACTION_GENERIC_VERIFY_V2, data)
	data.StateAction = windows.WTD_STATEACTION_CLOSE
	_ = windows.WinVerifyTrustEx(windows.InvalidHWND, &windows.WINTRUST_ACTION_GENERIC_VERIFY_V2, data)
	if verr != nil {
		return fmt.Errorf("%s (gecici atlama: ZORVEN_SKIP_AUTHENTICODE=1)", strings.TrimSpace(verr.Error()))
	}
	return nil
}
