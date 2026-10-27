package main

// Masaustu uygulamasinin Go tarafindaki kullaniciya gorunen metinleri
// (tepsi menusu, guncelleme/ag katmani/giris hatalari). client/i18n ile ayni
// desen: her anahtarin uc dili (tr, en, ja) yan yana durur; messages_test.go
// ucunun dolu oldugunu ve bicim fiillerinin ayni sirada oldugunu denetler.
// Etkin dil client/i18n'den okunur (SetLanguage i18n.Set cagirir), boylece
// gomulu ajanin mesajlariyla ayni dilde kalir. Arayuz (JS) metinleri ayri:
// frontend/dist/i18n.js.

import (
	"fmt"

	"github.com/tkodcumpeg4/zorven/client/i18n"
)

// tm, anahtarin etkin dildeki metnini doner (args varsa Sprintf).
func tm(key string, args ...any) string { return tml(i18n.Lang(), key, args...) }

// tml, belirli bir dil icin tm.
func tml(lang, key string, args ...any) string {
	m, ok := dmessages[key]
	if !ok {
		return key
	}
	i := 1
	switch lang {
	case i18n.TR:
		i = 0
	case i18n.JA:
		i = 2
	}
	s := m[i]
	if s == "" {
		s = m[1]
	}
	if len(args) > 0 {
		return fmt.Sprintf(s, args...)
	}
	return s
}

// dmessages: anahtar -> {tr, en, ja}.
var dmessages = map[string][3]string{
	// --- tepsi ---
	"tray.tooltip":      {"Zorven - Güvenli Tünel İstemcisi", "Zorven - Secure Tunnel Client", ""},
	"tray.show":         {"Pencereyi göster", "Show window", ""},
	"tray.show.tip":     {"Uygulama penceresini öne getir", "Bring the app window to the front", ""},
	"tray.tunnelNone":   {"Tünel hazır değil", "Tunnel not ready", ""},
	"tray.open":         {"Tarayıcıda aç", "Open in browser", ""},
	"tray.open.tip":     {"Tünel adresini varsayılan tarayıcıda aç", "Open the tunnel address in the default browser", ""},
	"tray.copy":         {"Adresi kopyala", "Copy address", ""},
	"tray.copy.tip":     {"Tünel adresini panoya kopyala", "Copy the tunnel address to the clipboard", ""},
	"tray.session":      {"Uzak Oturum Aktif", "Remote session active", ""},
	"tray.session.tip":  {"Terminal veya ekran paylaşımı bağlı", "A terminal or screen share is connected", ""},
	"tray.sessionN":     {"%d Uzak Oturum Aktif", "%d remote session(s) active", ""},
	"tray.close":        {"✕ Uzak oturumları sonlandır", "✕ End remote sessions", ""},
	"tray.close.tip":    {"Uzak terminal ve ekran paylaşımını hemen kapat", "Immediately close remote terminal and screen sharing", ""},
	"tray.quick":        {"Hızlı Port Paylaş", "Quick port share", ""},
	"tray.quick.tip":    {"Sık kullanılan bir yerel portu hemen tünelle", "Tunnel a common local port right away", ""},
	"tray.port3000.tip": {"localhost:3000 tünelle", "Tunnel localhost:3000", ""},
	"tray.port5173.tip": {"localhost:5173 tünelle", "Tunnel localhost:5173", ""},
	"tray.port8080.tip": {"localhost:8080 tünelle", "Tunnel localhost:8080", ""},
	"tray.connect":      {"Bağlan", "Connect", ""},
	"tray.connect.tip":  {"Tüneli başlat", "Start the tunnel", ""},
	"tray.stop":         {"Bağlantıyı kes", "Disconnect", ""},
	"tray.stop.tip":     {"Tüneli durdur", "Stop the tunnel", ""},
	"tray.quit":         {"Çıkış", "Quit", ""},
	"tray.quit.tip":     {"Uygulamayı tamamen kapat", "Close the application completely", ""},

	// --- baglanti durumu adlari ---
	"state.connected":    {"Bağlı", "Connected", ""},
	"state.connecting":   {"Bağlanıyor…", "Connecting…", ""},
	"state.reconnecting": {"Yeniden bağlanıyor", "Reconnecting", ""},
	"state.stopped":      {"Bağlı değil", "Not connected", ""},
	"state.fatal":        {"Bağlanamadı", "Could not connect", ""},
	"status.none":        {"bağlantı yok", "not connected", ""},
	"status.cut":         {"bağlantı kesildi", "disconnected", ""},

	// --- uygulama / giris ---
	"app.tokenEmpty":   {"Token boş. Lütfen Ayarlar sekmesinden istemci token'ınızı girin.", "Token is empty. Please enter your client token in Settings.", ""},
	"app.badPort":      {"Geçersiz port numarası (1-65535 arasında olmalı)", "Invalid port number (must be between 1 and 65535)", ""},
	"app.noAutostart":  {"Bu platformda otomatik başlatma kullanılamıyor", "Autostart is not available on this platform", ""},
	"app.saveFail":     {"Ayarlar kaydedilemedi: %s", "Could not save settings: %s", ""},
	"app.badLanguage":  {"Desteklenmeyen dil: %s", "Unsupported language: %s", ""},
	"dev.tokenEmpty":   {"Token boş olamaz.", "Token cannot be empty.", ""},
	"dev.tokenPrefix":  {"Token \"zrv_live_\" ile başlamalıdır.", "Token must start with \"zrv_live_\".", ""},
	"dev.unreachable":  {"sunucuya ulaşılamadı: %s", "could not reach the server: %s", ""},
	"dev.badResponse":  {"sunucu yanıtı çözülemedi", "could not parse the server response", ""},
	"dev.denied":       {"Onay reddedildi.", "Approval was denied.", ""},
	"dev.expired":      {"Kodun süresi doldu, tekrar deneyin.", "The code has expired, please try again.", ""},
	"dev.hostFallback": {"masaüstü", "desktop", ""},

	// --- guncelleme ---
	"upd.sigFail":      {"güncelleme imzası alınamadı", "could not fetch the update signature", ""},
	"upd.notApplied":   {"%s; güncelleme uygulanmadı", "%s; update not applied", ""},
	"upd.readFail":     {"sürüm bilgisi okunamadı", "could not read version info", ""},
	"upd.unreachable":  {"sunucuya ulaşılamadı", "could not reach the server", ""},
	"upd.httpFail":     {"sürüm bilgisi alınamadı (HTTP %d)", "could not fetch version info (HTTP %d)", ""},
	"upd.latest":       {"Zaten güncel; manifest sürümü mevcut sürümden büyük değil.", "Already up to date; the manifest version is not newer than the current one.", ""},
	"upd.noPlatform":   {"Bu platform için otomatik güncelleme yok; indirme sayfası açıldı.", "No automatic update for this platform; the download page was opened.", ""},
	"upd.dlFail":       {"Güncelleme indirilemedi.", "Could not download the update.", ""},
	"upd.dlFailHTTP":   {"Güncelleme indirilemedi (HTTP %d).", "Could not download the update (HTTP %d).", ""},
	"upd.noExe":        {"Uygulama yolu bulunamadı.", "Could not find the application path.", ""},
	"upd.applyFail":    {"Güncelleme uygulanamadı (%s); indirme sayfası açıldı.", "Could not apply the update (%s); the download page was opened.", ""},
	"upd.restartHand":  {"Güncelleme kuruldu; uygulamayı elle yeniden başlatın.", "Update installed; please restart the app manually.", ""},
	"upd.tmpFail":      {"Güncelleme geçici dosyaya yazılamadı.", "Could not write the update to a temporary file.", ""},
	"upd.needAdmin":    {"Güncelleme için yönetici onayı gerekiyor (%s); uygulamayı yönetici olarak açıp tekrar deneyin.", "Administrator approval is required for the update (%s); run the app as administrator and try again.", ""},
	"upd.shaMismatch":  {"İndirilen dosya doğrulanamadı (sha256 uyuşmuyor); güncelleme uygulanmadı.", "The downloaded file could not be verified (sha256 mismatch); update not applied.", ""},
	"upd.sizeMismatch": {"İndirilen dosya boyutu beklenenle uyuşmuyor; güncelleme uygulanmadı.", "The downloaded file size does not match what was expected; update not applied.", ""},
	"upd.notExe":       {"İndirilen dosya geçerli bir uygulama değil; güncelleme uygulanmadı.", "The downloaded file is not a valid application; update not applied.", ""},

	// --- tam ag katmani ---
	"net.hintInstallWin":  {"Yonetici PowerShell: zorven service install --token <TOKEN>; zorven service start. Servis kurulunca wintun.dll sunucudan imzali olarak otomatik indirilir.", "Administrator PowerShell: zorven service install --token <TOKEN>; zorven service start. Once the service is installed, wintun.dll is downloaded automatically from the server (signed).", ""},
	"net.hintInstallNix":  {"Terminal: sudo zorven service install --token <TOKEN> && sudo zorven service start; sonra 'sudo zorven net up'. Servis soketine yalniz root erisir.", "Terminal: sudo zorven service install --token <TOKEN> && sudo zorven service start; then 'sudo zorven net up'. Only root can access the service socket.", ""},
	"net.hintElevWin":     {"Servis kontrol soketine yalniz yonetici erisir. Uygulamayi yonetici olarak yeniden baslatin.", "Only an administrator can access the service control socket. Restart the app as administrator.", ""},
	"net.hintElevNix":     {"Servis soketine yalniz root erisir; bu uygulama kullanici yetkisiyle calisir. Terminalden: sudo zorven net up", "Only root can access the service socket; this app runs with user privileges. From a terminal: sudo zorven net up", ""},
	"net.noService":       {"Zorven servisi bulunamadi: tam ag katmani ayricalikli bir servis ister.", "Zorven service not found: the full network layer needs a privileged service.", ""},
	"net.noAccess":        {"Zorven servisi calisiyor ancak bu kullanicinin erisimi yok.", "The Zorven service is running but this user has no access to it.", ""},
	"net.unreachable":     {"Zorven servisine erisilemedi (%s)", "Could not reach the Zorven service (%s)", ""},
	"net.connectFirst":    {"Once baglanin: tam ag katmani bu uygulamanin istemcisiyle acilir.", "Connect first: the full network layer is opened with this app's own client.", ""},
	"net.needAdmin":       {"Tam ag katmani yonetici izni ister. Uygulamayi yonetici olarak yeniden baslatin.", "The full network layer needs administrator permission. Restart the app as administrator.", ""},
	"net.joinHint":        {"Bu cihaz (masaustu istemcisi) aga katilir; sanal ag arayuzu icin yonetici izni gerekir.", "This device (the desktop client) joins the network; the virtual network interface needs administrator permission.", ""},
	"net.unsupported":     {"Calisan Zorven surumu tam ag katmanini desteklemiyor; guncelleyin.", "The running Zorven version does not support the full network layer; please update.", ""},
	"net.installNotWin":   {"Bu platformda servis kurulumu terminalden yapilir.", "On this platform the service is installed from a terminal.", ""},
	"net.noToken":         {"Once uygulamada oturum acin (token yok).", "Sign in to the app first (no token).", ""},
	"net.noCLI":           {"zorven.exe (komut satiri istemcisi) bulunamadi. Once https://zorven.app adresindeki Windows kurulum komutuyla (install.ps1) CLI'yi kurun; kurulum servisi ve wintun.dll'i de hazirlar.", "zorven.exe (the command-line client) was not found. First install the CLI with the Windows install command (install.ps1) from https://zorven.app; it also prepares the service and wintun.dll.", ""},
	"net.badToken":        {"token bicimi gecersiz; once uygulamada oturum acin", "invalid token format; sign in to the app first", ""},
	"net.adminAsked":      {"Yonetici onayi istendi; servis kurulup baslatilinca durum kendiliginden guncellenir.", "Administrator approval was requested; the status updates automatically once the service is installed and started.", ""},
	"net.winOnly":         {"Bu islem yalniz Windows'ta desteklenir.", "This action is only supported on Windows.", ""},
	"net.badCLIPath":      {"zorven.exe yolu gecersiz karakter iceriyor", "the zorven.exe path contains invalid characters", ""},
	"net.badTokenFile":    {"gecici token dosyasi yolu gecersiz", "the temporary token file path is invalid", ""},
	"net.badServer":       {"sunucu adresi gecersiz", "the server address is invalid", ""},
	"net.tokenFileFail":   {"gecici token dosyasi yazilamadi: %v", "could not write the temporary token file: %v", ""},
	"net.adminDenied":     {"yonetici onayi reddedildi", "administrator approval was denied", ""},
	"net.elevUnsupported": {"yukseltilmis calistirma bu platformda desteklenmiyor", "elevated execution is not supported on this platform", ""},
}
