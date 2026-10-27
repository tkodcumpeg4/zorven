package i18n

import "golang.org/x/sys/windows"

// osLanguage, Windows kullanici arayuzu dilini doner (or. "tr-TR").
func osLanguage() string {
	langs, err := windows.GetUserPreferredUILanguages(windows.MUI_LANGUAGE_NAME)
	if err != nil || len(langs) == 0 {
		return ""
	}
	return langs[0]
}
