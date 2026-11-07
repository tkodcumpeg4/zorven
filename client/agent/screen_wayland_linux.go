//go:build linux

package agent

import (
	"errors"
	"os"
	"strings"

	"github.com/tkodcumpeg4/zorven/client/i18n"
)

// platSessionBlocker, ekran paylasiminin bu oturumda calisamayacagini onceden
// bildirir. Saf Wayland oturumunda (kullanilabilir X ekrani yok) yakalama ve
// girdi X11 uzerinden calisir; siyah kare gostermek yerine acik hata doner.
func platSessionBlocker() error {
	if waylandOnly(os.Getenv("XDG_SESSION_TYPE"), os.Getenv("WAYLAND_DISPLAY"), os.Getenv("DISPLAY")) {
		return errors.New(i18n.T("scr.err.wayland"))
	}
	return nil
}

// waylandOnly: oturum Wayland ve X ekrani (XWayland dahil) yok.
func waylandOnly(sessionType, waylandDisplay, display string) bool {
	if display != "" {
		return false
	}
	return strings.EqualFold(sessionType, "wayland") || waylandDisplay != ""
}
