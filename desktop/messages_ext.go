package main

// messages.go'ya ek anahtarlar (0.3.0): ayar dosyasi, gelismis ayarlar, servis
// modu, guncelleme ilerlemesi, bildirimler ve dinamik tepsi menusu. Sema ve
// denetim messages.go ile aynidir: {tr, en, ja, zh}.

func init() {
	for k, v := range extraMessages {
		dmessages[k] = v
	}
}

var extraMessages = map[string][4]string{
	// --- ayar dosyasi ---
	"cfg.noDir":     {"yapılandırma dizini bulunamadı", "configuration directory not found", "", ""},
	"cfg.mkdirFail": {"yapılandırma dizini oluşturulamadı", "could not create the configuration directory", "", ""},
	"cfg.writeFail": {"ayarlar yazılamadı", "could not write settings", "", ""},
	"cfg.readFail":  {"Ayarlar okunamadı: %v", "Could not read settings: %v", "", ""},
	"cfg.corrupt":   {"Ayar dosyası bozuktu ve varsayılanlara dönüldü. Eski dosya yedeklendi: %s", "The settings file was corrupt, so defaults were loaded. The old file was backed up: %s", "", ""},
	"cfg.decrypt":   {"Kayıtlı oturum bu Windows kullanıcısıyla çözülemedi; yeniden giriş yapın. Eski dosya yedeklendi: %s", "The saved sign-in could not be decrypted for this Windows user; please sign in again. The old file was backed up: %s", "", ""},

	// --- gelismis ayarlar ---
	"adv.badServer": {"Sunucu adresi geçersiz (örn. zorven.app:443).", "The server address is invalid (e.g. zorven.app:443).", "", ""},
	"adv.caRead":    {"CA sertifikası okunamadı: %v", "Could not read the CA certificate: %v", "", ""},
	"adv.caBad":     {"Dosya geçerli bir PEM sertifikası içermiyor.", "The file does not contain a valid PEM certificate.", "", ""},

	// --- servis / CLI birlikte yasama ---
	"svc.active": {"Bu bilgisayarda Zorven hizmeti veya komut satırı istemcisi zaten bağlı; masaüstü ikinci bir bağlantı açmaz. Devralmak için önce onu durdurun.", "The Zorven service or command-line client is already connected on this computer; the desktop app will not open a second connection. Stop it first to take over.", "", ""},
	"svc.badge":  {"Zorven hizmeti üzerinden bağlı", "Connected through the Zorven service", "", ""},

	// --- guncelleme ---
	"upd.cancelled":     {"İndirme iptal edildi.", "The download was cancelled.", "", ""},
	"upd.busy":          {"Güncelleme zaten sürüyor.", "An update is already in progress.", "", ""},
	"upd.helperFail":    {"Yönetici yardımcısı güncellemeyi uygulayamadı (kod %d); mevcut sürüm çalışmaya devam ediyor.", "The administrator helper could not apply the update (code %d); the current version keeps running.", "", ""},
	"upd.helperTimeout": {"Yönetici yardımcısı zamanında bitmedi.", "The administrator helper did not finish in time.", "", ""},

	// --- bildirimler ---
	"note.updateTitle":     {"Zorven güncellemesi var", "Zorven update available", "", ""},
	"note.updateBody":      {"Sürüm %s hazır (şu an %s). Ayarlar'dan güncelleyebilirsiniz.", "Version %s is ready (you have %s). Update from Settings.", "", ""},
	"note.connected":       {"Zorven bağlandı", "Zorven connected", "", ""},
	"note.connectedBody":   {"Tüneller kullanıma hazır.", "Your tunnels are ready.", "", ""},
	"note.reconnected":     {"Zorven yeniden bağlandı", "Zorven reconnected", "", ""},
	"note.reconnectedBody": {"Bağlantı geri geldi.", "The connection is back.", "", ""},
	"note.disconnected":    {"Zorven bağlantısı koptu", "Zorven disconnected", "", ""},

	// --- dinamik tepsi menusu ---
	"tray.update":        {"Güncelleme var (v%s)", "Update available (v%s)", "", ""},
	"tray.update.tip":    {"Ayarlar'da güncellemeyi göster", "Show the update in Settings", "", ""},
	"tray.stopShare":     {"Paylaşımı durdur", "Stop sharing", "", ""},
	"tray.stopShare.tip": {"Paylaşılan portu bırak", "Stop sharing the local port", "", ""},
	"tray.sharePort":     {"Port %d paylaş", "Share port %d", "", ""},
	"tray.sharePort.tip": {"localhost:%d tünelle", "Tunnel localhost:%d", "", ""},
	"tray.sharing":       {"Paylaşılıyor: %s", "Sharing: %s", "", ""},
}
