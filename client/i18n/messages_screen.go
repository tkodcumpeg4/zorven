package i18n

// screenMessages: ekran paylasimi (servis modu yardimcisi) metinleri.
var screenMessages = map[string][3]string{
	"scr.err.no_user": {
		"Oturum açmış kullanıcı yok: ekran paylaşımı için bu bilgisayarda bir kullanıcının Windows'ta oturum açmış olması gerekir",
		"No user is logged on: screen sharing needs a user to be signed in to Windows on this computer",
		""},
	"scr.err.helper": {
		"Ekran yardımcısı çalıştırılamadı: %s",
		"Could not run the screen helper: %s",
		""},
	"scr.err.args": {
		"geçersiz ekran yardımcısı bağımsız değişkenleri (bu dahili bir komuttur)",
		"invalid screen helper arguments (this is an internal command)",
		""},
	"scr.err.unsupported": {
		"ekran yardımcısı yalnızca Windows'ta kullanılır",
		"the screen helper is only used on Windows",
		""},
	"scr.err.session0": {
		"ekran yardımcısı oturum 0'da (servis) çalıştırılamaz",
		"the screen helper cannot run in session 0 (service)",
		""},
	"scr.cmd.short": {
		"Dahili: servis modunda kullanıcı oturumunda ekran yakalayıp girdi uygulayan yardımcı",
		"Internal: helper that captures the screen and applies input in the user session when running as a service",
		""},
}

func init() {
	for k, v := range screenMessages {
		messages[k] = v
	}
}
