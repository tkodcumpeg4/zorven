package main

// Windows kurulumu / kaldirma / servis kimlik aktarimi metinleri
// ({tr, en, ja, zh}; sema messages.go ile ayni, messages_test.go denetler).

func init() {
	for k, v := range setupMessages {
		dmessages[k] = v
	}
}

var setupMessages = map[string][4]string{
	"setup.title": {"Zorven Kurulum", "Zorven Setup", "", ""},
	"setup.confirm": {
		"Zorven şu klasöre kurulacak:\n%s\n\n- Başlat menüsüne eklenir\n- Arka plan hizmeti olarak (tam izinli) çalışır\n- Komut satırı istemcisi (zorven) PATH'e eklenir\n- Yönetici onayı (UAC) istenir\n\nKurulum devam etsin mi?",
		"Zorven will be installed to:\n%s\n\n- Added to the Start menu\n- Runs as a background service (full permissions)\n- The command-line client (zorven) is added to PATH\n- Administrator approval (UAC) is requested\n\nContinue with the installation?",
		"",
		"",
	},
	"setup.desktopShortcut": {"Masaüstüne de kısayol eklensin mi?", "Also add a desktop shortcut?", "", ""},
	"setup.fail":            {"Kurulum tamamlanamadı.\n\n%s\n\nGünlük: %s", "Setup could not be completed.\n\n%s\n\nLog: %s", "", ""},
	"setup.errDL": {
		"Komut satırı istemcisi indirilemedi veya doğrulanamadı. İnternet bağlantınızı denetleyip yeniden deneyin (hiçbir şey kurulmadı).",
		"The command-line client could not be downloaded or verified. Check your internet connection and try again (nothing was installed).",
		"",
		"",
	},
	"setup.errNoCLI":  {"Sunucuda bu platform için komut satırı istemcisi yok.", "The server has no command-line client for this platform.", "", ""},
	"setup.errFiles":  {"Dosyalar kopyalanamadı. Zorven çalışıyorsa kapatıp yeniden deneyin.", "Files could not be copied. If Zorven is running, close it and try again.", "", ""},
	"setup.errSystem": {"PATH, kısayol veya kayıt defteri girdisi yazılamadı.", "Could not write the PATH, shortcut or registry entry.", "", ""},
	"setup.errService": {
		"Dosyalar kuruldu ancak arka plan hizmeti kurulamadı veya başlatılamadı. Zorven'i açıp Ağ bölümünden \"Onar\" düğmesini kullanın.",
		"Files were installed but the background service could not be installed or started. Open Zorven and use the \"Repair\" button in the Network section.",
		"",
		"",
	},
	"setup.errGeneric": {"Beklenmeyen hata (kod %d).", "Unexpected error (code %d).", "", ""},

	"un.confirm": {
		"Zorven kaldırılsın mı?\n\nArka plan hizmeti, kısayollar, PATH girdisi ve program dosyaları silinir.\n\nAyarlarınız (%s) ve hizmet yapılandırmanız (%s) KORUNUR.",
		"Uninstall Zorven?\n\nThe background service, shortcuts, PATH entry and program files will be removed.\n\nYour settings (%s) and service configuration (%s) are KEPT.",
		"",
		"",
	},
	"un.done": {
		"Zorven kaldırıldı.\n\nKorunan: %s\nKorunan: %s",
		"Zorven was uninstalled.\n\nKept: %s\nKept: %s",
		"",
		"",
	},
	"un.fail":       {"Kaldırma tamamlanamadı: %s", "Uninstall could not be completed: %s", "", ""},
	"un.errService": {"Arka plan hizmeti kaldırılamadı; dosyalar silinmedi.", "The background service could not be removed; files were not deleted.", "", ""},
	"un.errRemove":  {"Program klasörü silinemedi (dosyalar kullanımda olabilir).", "The program folder could not be deleted (files may be in use).", "", ""},

	"svc.starting": {
		"Arka plan hizmeti başlıyor veya kimliğiniz hizmete aktarılıyor; birkaç saniye içinde durum güncellenir.",
		"The background service is starting or your sign-in is being passed to it; the status updates in a few seconds.",
		"",
		"",
	},
	"perm.applyFail": {
		"İzin değişikliği arka plan hizmetine uygulanamadı (yönetici onayı gerekir): %s",
		"The permission change could not be applied to the background service (administrator approval is required): %s",
		"",
		"",
	},
	"svc.handoffFail":  {"Kimlik arka plan hizmetine aktarılamadı: %s", "The sign-in could not be passed to the background service: %s", "", ""},
	"svc.noTakeover":   {"Kurulu Zorven'de bağlantı her zaman arka plan hizmeti üzerinden yürür; masaüstü devralmaz.", "In an installed Zorven the connection always runs through the background service; the desktop app does not take over.", "", ""},
	"svc.repairOK":     {"Onarım için yönetici onayı istendi; hizmet birkaç saniye içinde başlar.", "Administrator approval was requested for the repair; the service starts in a few seconds.", "", ""},
	"svc.notInstalled": {"Zorven bu bilgisayara kurulu değil.", "Zorven is not installed on this computer.", "", ""},

	"upd.cliLock": {
		"Komut satırı istemcisinin sürümü (%s) masaüstü sürümüyle (%s) eşleşmiyor; güncelleme ertelendi.",
		"The command-line client version (%s) does not match the desktop version (%s); the update was postponed.",
		"",
		"",
	},
}
