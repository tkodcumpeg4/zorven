package i18n

// v3ScreenMessages: 0.3.0 uzak ekran metinleri.
var v3ScreenMessages = map[string][4]string{
	"scr.err.wayland": {
		"Wayland oturumu algılandı: ekran paylaşımı henüz Wayland'i desteklemiyor. X11 (Xorg) oturumuyla giriş yapın",
		"A Wayland session was detected: screen sharing does not support Wayland yet. Sign in with an X11 (Xorg) session",
		"",
		""},
	"scr.err.capture": {
		"ekran yakalanamadı: %s",
		"screen capture failed: %s",
		"", ""},
	"scr.err.capture_gave_up": {
		"ekran yakalanamadı (yeniden denemeler tükendi): %s",
		"screen capture failed (retries exhausted): %s",
		"", ""},
	"scr.err.encode": {
		"kare kodlanamadı: %s",
		"could not encode the frame: %s",
		"", ""},
}

func init() {
	for k, v := range v3ScreenMessages {
		messages[k] = v
	}
}
