//go:build windows

package config

import (
	"fmt"
	"strings"

	"golang.org/x/sys/windows"
)

// restrictFile, Windows'ta dosyaya korumali (miras almayan) bir DACL uygular.
// 0600 izni Windows'ta etkisizdir: %ProgramData% altindaki dosyalar varsayilan
// olarak Users grubuna okunur miras alir ve cihaz token'i yerel her kullaniciya
// acik kalirdi. Sistem dosyasi: yalniz SYSTEM + Administrators; kullanici
// dosyasi: ek olarak sahibi olan kullanici.
func restrictFile(path string, system bool) error {
	sddl := "D:P(A;;FA;;;SY)(A;;FA;;;BA)"
	if !system {
		u, err := windows.GetCurrentProcessToken().GetTokenUser()
		if err != nil {
			return err
		}
		sddl += fmt.Sprintf("(A;;FA;;;%s)", u.User.Sid.String())
	}
	sd, err := windows.SecurityDescriptorFromString(sddl)
	if err != nil {
		return err
	}
	dacl, _, err := sd.DACL()
	if err != nil {
		return err
	}
	if err := windows.SetNamedSecurityInfo(path, windows.SE_FILE_OBJECT,
		windows.DACL_SECURITY_INFORMATION|windows.PROTECTED_DACL_SECURITY_INFORMATION,
		nil, nil, dacl, nil); err != nil {
		return fmt.Errorf("dosya ACL'i ayarlanamadi: %w", err)
	}
	return nil
}

func isSystemPath(path string) bool { return strings.EqualFold(path, SystemPath()) }
