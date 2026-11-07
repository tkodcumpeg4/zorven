package i18n

// v3CLIMessages: 0.3.0 CLI isleri (status/doctor/logs/login/logout/config,
// canli ayar uygulama, forward, servis ince ayarlari). {tr, en, ja, zh}.
var v3CLIMessages = map[string][4]string{
	// ---- ortak
	"v3.flag.json":          {"makine-okur JSON çıktısı ver", "print machine-readable JSON", "", ""},
	"v3.flag.yes":           {"onay sormadan devam et", "do not ask for confirmation", "", ""},
	"v3.flag.tokenfile":     {"istemci token'ını dosyadan oku ('-' = stdin; komut satırında görünmez)", "read the client token from a file ('-' = stdin; keeps it off the command line)", "", ""},
	"v3.flag.apitokenfile":  {"API token'ı dosyadan oku ('-' = stdin; komut satırında görünmez)", "read the API token from a file ('-' = stdin; keeps it off the command line)", "", ""},
	"v3.flag.apitokenstdin": {"API token'ı standart girdiden oku", "read the API token from standard input", "", ""},
	"v3.err.tokenfile.read": {"token dosyası okunamadı", "could not read the token file", "", ""},
	"v3.warn.plaintext": {
		"UYARI: --insecure düz metin ws:// kullanır, TLS YOKTUR: token ve tüm trafik ağda AÇIKTAN gider. Yalnızca yerel geliştirme için kullanın.",
		"WARNING: --insecure uses plaintext ws:// with NO TLS: the token and all traffic travel IN THE CLEAR. Use it for local development only.",
		"",
		"",
	},
	"v3.warn.target_dropped": {
		"Uyarı: izin bayrakları çalışan istemciye iletildi; hedef (%s) EKLENMEDİ — çalışan bir istemciye canlı tünel eklenemez.",
		"Warning: the permission flags were sent to the running client; the target (%s) was NOT added — tunnels cannot be added to a running client.",
		"",
		"",
	},
	"v3.err.agent_running": {
		"Bu cihazda zaten bir Zorven istemcisi çalışıyor (%s); ikinci bir bağlantı sunucuda 409 ile reddedilir.\n  Durumu görmek için:   zorven status\n  İzinleri değiştirmek: zorven enable|disable terminal|screen\n  Yeni tünel için önce mevcut istemciyi durdurun (ön plandaysa Ctrl+C, servisse: zorven down) ve komutu yeniden çalıştırın.\n  Tünelleri panelden de yönetebilirsiniz.",
		"A Zorven client is already running on this device (%s); a second connection would be rejected by the server with 409.\n  See its state:        zorven status\n  Change permissions:   zorven enable|disable terminal|screen\n  For a new tunnel, first stop the running client (Ctrl+C if foreground, 'zorven down' if a service) and run the command again.\n  You can also manage tunnels from the dashboard.",
		"",
		"",
	},
	"v3.help.completion": {
		"Kabuk tamamlama için: zorven completion bash|zsh|fish|powershell --help (kurulum yönergelerini gösterir).",
		"Shell completion: zorven completion bash|zsh|fish|powershell --help (shows install instructions).",
		"",
		"",
	},

	// ---- status
	"v3.status.long": {
		"Yapılandırmayı, sistem servisini ve çalışan istemciyi (bağlantı durumu, tüneller, izinler, son kopma nedenleri, ağ/TUN) tek ekranda gösterir.\nÇıkış kodu: 0 = çalışıyor ve bağlı, 1 = hiçbir şey çalışmıyor, 2 = çalışıyor ama sunucuya bağlı değil.",
		"Shows the configuration, the system service and the running client (connection state, tunnels, permissions, recent disconnect reasons, network/TUN) on one screen.\nExit code: 0 = running and connected, 1 = nothing is running, 2 = running but not connected to the server.",
		"",
		"",
	},
	"v3.status.svc.unknown":  {"bilinmiyor", "unknown", "", ""},
	"v3.status.insecure.on":  {"AÇIK (düz metin ws://, TLS yok)", "ON (plaintext ws://, no TLS)", "", ""},
	"v3.status.agent":        {"İstemci:", "Client:", "", ""},
	"v3.status.agent.none":   {"çalışan istemci yok", "no running client", "", ""},
	"v3.status.agent.run":    {"çalışıyor (%s, pid %d, sürüm %s)", "running (%s, pid %d, version %s)", "", ""},
	"v3.status.conn":         {"Bağlantı:", "Connection:", "", ""},
	"v3.status.connected":    {"bağlı", "connected", "", ""},
	"v3.status.disconnected": {"bağlı değil", "not connected", "", ""},
	"v3.status.oldagent":     {"bilinmiyor (çalışan istemci eski sürüm; güncelleyip yeniden başlatın)", "unknown (the running client is an older version; update and restart it)", "", ""},
	"v3.status.uptime":       {"Çalışma süresi:", "Uptime:", "", ""},
	"v3.status.reconnects":   {"Yeniden bağlanma:", "Reconnects:", "", ""},
	"v3.status.lasterr":      {"Son hata:", "Last error:", "", ""},
	"v3.status.tunnels":      {"Tüneller:", "Tunnels:", "", ""},
	"v3.status.disc":         {"Son kopmalar:", "Recent disconnects:", "", ""},
	"nodes.lbl.node":         {"Düğüm:", "Node:", "", ""},
	"nodes.lbl.list":         {"Düğümler:", "Nodes:", "", ""},
	"nodes.pinned":           {"sabit sunucu: düğüm listesi kullanılmıyor", "pinned server: node list is not used", "", ""},
	"nodes.none":             {"düğüm listesi yok (tek sunucu)", "no node list (single server)", "", ""},
	"nodes.row":              {"%s %s  bölge=%s  öncelik=%d  %s", "%s %s  region=%s  priority=%d  %s", "", ""},
	"nodes.healthy":          {"sağlıklı", "healthy", "", ""},
	"nodes.unhealthy":        {"sağlıksız", "unhealthy", "", ""},
	"nodes.doctor":           {"%s (%d düğüm)", "%s (%d nodes)", "", ""},
	"v3.tbl.name":            {"Ad", "Name", "", ""},
	"v3.tbl.target":          {"Hedef", "Target", "", ""},
	"v3.tbl.requests":        {"İstek", "Requests", "", ""},
	"v3.tbl.errors":          {"Hata", "Errors", "", ""},
	"v3.tbl.traffic":         {"Trafik (giren / çıkan)", "Traffic (in / out)", "", ""},

	// ---- config
	"v3.cfg.sysfile":      {"Sistem dosyası:", "System file:", "", ""},
	"v3.cfg.active":       {"Etkin dosya:", "Active file:", "", ""},
	"v3.cfg.get.short":    {"Bir ayarın etkin değerini yazdırır", "Print the effective value of a setting", "", ""},
	"v3.cfg.set.short":    {"Bir ayarı yazar (yalnızca hedef dosyaya; --system ile sistem dosyası)", "Set a setting (target file only; --system for the system file)", "", ""},
	"v3.cfg.unset.short":  {"Bir ayarı varsayılana döndürür", "Reset a setting to its default", "", ""},
	"v3.cfg.err.key":      {"bilinmeyen ayar anahtarı %q (geçerli: %s)", "unknown setting key %q (valid: %s)", "", ""},
	"v3.cfg.err.lang":     {"desteklenmeyen dil %q (tr, en, ja, zh)", "unsupported language %q (tr, en, ja, zh)", "", ""},
	"v3.cfg.saved":        {"✓ Kaydedildi: %s", "✓ Saved: %s", "", ""},
	"v3.cfg.live.ok":      {"✓ Çalışan istemciye canlı uygulandı.", "✓ Applied live to the running client.", "", ""},
	"v3.cfg.live.restart": {"Çalışan bir istemci var ama canlı uygulayamıyor (eski sürüm ya da farklı kapsam); değişiklik yeniden başlatınca geçerli olur.", "A client is running but cannot apply this live (older version or different scope); the change takes effect after a restart.", "", ""},
	"v3.cfg.live.later":   {"Bu ayar çalışan istemciye canlı uygulanamaz; istemci yeniden başlatılınca uygulanır.", "This setting cannot be applied live to the running client; it is applied when the client restarts.", "", ""},
	"v3.cfg.live.fail":    {"Çalışan istemciye uygulanamadı: %v (yeniden başlatınca geçerli olur)", "Could not apply to the running client: %v (takes effect after a restart)", "", ""},

	// ---- login / logout
	"v3.login.short":        {"API token'ı bu cihaza kaydeder (connect/mcp/net/apps için)", "Save an API token on this device (for connect/mcp/net/apps)", "", ""},
	"v3.login.long":         {"API token'ı (zrv_api_...) yalnızca sahibinin okuyabildiği (0600) bir dosyaya yazar; sonraki komutlarda ZORVEN_API_TOKEN gerekmez. Cihaz (istemci) token'ı için 'zorven authtoken' kullanın.", "Writes the API token (zrv_api_...) to an owner-only (0600) file so later commands do not need ZORVEN_API_TOKEN. For the device (client) token use 'zorven authtoken'.", "", ""},
	"v3.login.err.notty":    {"API token vermek için --api-token-file <dosya> ya da --api-token-stdin kullanın (terminal yok)", "provide the API token with --api-token-file <file> or --api-token-stdin (no terminal)", "", ""},
	"v3.login.prompt":       {"API token (girdi görünmez): ", "API token (input hidden): ", "", ""},
	"v3.login.err.format":   {"API token 'zrv_api_' ile başlamalıdır", "the API token must start with 'zrv_api_'", "", ""},
	"v3.login.saved":        {"✓ API token kaydedildi: %s", "✓ API token saved: %s", "", ""},
	"v3.logout.short":       {"Kayıtlı cihaz token'ını ve API token'ı siler", "Remove the saved device token and API token", "", ""},
	"v3.logout.long":        {"Kullanıcı ayar dosyasındaki cihaz token'ını ve kayıtlı API token'ı siler. Sistem servisinin (sistem ayar dosyası) token'ına dokunmaz.", "Removes the device token from the user config file and the saved API token. The system service token (system config file) is left alone.", "", ""},
	"v3.logout.nothing":     {"Kayıtlı token yok.", "No saved token.", "", ""},
	"v3.logout.confirm":     {"Kayıtlı token'lar silinsin mi?", "Remove the saved tokens?", "", ""},
	"v3.logout.err.confirm": {"onay için terminal gerekli; --yes ile onaylayın", "a terminal is needed to confirm; pass --yes", "", ""},
	"v3.logout.device":      {"✓ Cihaz token'ı silindi.", "✓ Device token removed.", "", ""},
	"v3.logout.api":         {"✓ API token silindi.", "✓ API token removed.", "", ""},
	"v3.logout.sys":         {"Not: sistem servisi kendi token'ını (%s) kullanmaya devam eder. Kaldırmak için: zorven down --remove --purge", "Note: the system service keeps using its own token (%s). To remove it: zorven down --remove --purge", "", ""},

	// ---- logs
	"v3.logs.short":       {"Servis/istemci günlüğünün son satırlarını gösterir", "Show the last lines of the service/client log", "", ""},
	"v3.logs.long":        {"Servisin günlük dosyasını (agent.log) okur. -f ile yeni satırları canlı izler. Ön planda çalışan istemci günlüğünü terminale yazar.", "Reads the service log file (agent.log). -f follows new lines live. A foreground client logs to its terminal.", "", ""},
	"v3.logs.flag.follow": {"yeni satırları canlı izle", "follow new lines live", "", ""},
	"v3.logs.flag.lines":  {"gösterilecek son satır sayısı", "number of trailing lines to show", "", ""},
	"v3.logs.none":        {"Günlük dosyası yok: %s\n(Servis hiç çalışmadı ya da istemci ön planda çalışıyor.)", "No log file: %s\n(The service never ran, or the client runs in the foreground.)", "", ""},
	"v3.logs.journal":     {"İpucu (Linux): systemd günlüğü için: journalctl -u zorven -f", "Tip (Linux): for the systemd journal: journalctl -u zorven -f", "", ""},
	"v3.logs.perm":        {"günlük dosyası okunamadı (yetki): %s — sudo ile çalıştırın", "cannot read the log file (permission): %s — run with sudo", "", ""},

	// ---- doctor
	"v3.doctor.short":     {"Kurulumu ve bağlantıyı denetler (ayar, token, DNS, TLS, saat, servis, TUN)", "Check the setup and connectivity (config, token, DNS, TLS, clock, service, TUN)", "", ""},
	"v3.doctor.long":      {"Ayar dosyası, token biçimi, sunucu DNS çözümü, TCP+TLS el sıkışması, sunucu saat farkı, yerel istemci, servis durumu ve TUN ayrıcalığını denetler. Herhangi bir denetim başarısızsa çıkış kodu 1'dir.", "Checks the config file, token format, server DNS resolution, TCP+TLS handshake, server clock skew, the local client, the service state and TUN privileges. Exit code is 1 if any check fails.", "", ""},
	"v3.doctor.pass":      {"Tüm kritik denetimler geçti.", "All critical checks passed.", "", ""},
	"v3.doctor.failed":    {"Bazı denetimler başarısız oldu.", "Some checks failed.", "", ""},
	"v3.doc.config":       {"Ayar dosyası", "Config file", "", ""},
	"v3.doc.token":        {"Cihaz token'ı", "Device token", "", ""},
	"v3.doc.token.none":   {"token yok (zorven authtoken <TOKEN>)", "no token (zorven authtoken <TOKEN>)", "", ""},
	"v3.doc.dns":          {"DNS çözümü (%s)", "DNS resolution (%s)", "", ""},
	"v3.doc.tls":          {"TCP + TLS el sıkışması", "TCP + TLS handshake", "", ""},
	"v3.doc.tls.plain":    {"TCP bağlandı; --insecure/ayar nedeniyle TLS yok (düz metin)", "TCP connected; no TLS because of --insecure/config (plaintext)", "", ""},
	"v3.doc.cert.days":    {"sertifikanın bitmesine %d gün", "certificate expires in %d days", "", ""},
	"v3.doc.clock":        {"Sunucu saat farkı", "Server clock skew", "", ""},
	"v3.doc.clock.ok":     {"fark %v", "skew %v", "", ""},
	"v3.doc.clock.skew":   {"saat farkı %v — sistem saatinizi düzeltin (imza/token doğrulaması bozulabilir)", "clock skew %v — fix your system clock (signature/token validation may break)", "", ""},
	"v3.doc.clock.nodate": {"sunucu Date başlığı okunamadı", "could not read the server Date header", "", ""},
	"v3.doc.skipdns":      {"DNS başarısız olduğu için atlandı", "skipped because DNS failed", "", ""},
	"v3.doc.skiptls":      {"TLS başarısız olduğu için atlandı", "skipped because TLS failed", "", ""},
	"v3.doc.agent":        {"Yerel istemci (localctl)", "Local client (localctl)", "", ""},
	"v3.doc.service":      {"Sistem servisi", "System service", "", ""},
	"v3.doc.tun":          {"TUN ayrıcalığı (tam ağ katmanı)", "TUN privileges (full network layer)", "", ""},
	"v3.doc.tun.nodev":    {"/dev/net/tun yok", "/dev/net/tun is missing", "", ""},
	"v3.doc.tun.root":     {"root olarak çalışıyor", "running as root", "", ""},
	"v3.doc.tun.admin":    {"yönetici olarak çalışıyor", "running as administrator", "", ""},
	"v3.doc.tun.nopriv":   {"yetki yok; TUN servis olarak (root/yönetici) açılır: sudo zorven up", "no privilege; TUN runs via the service (root/administrator): sudo zorven up", "", ""},

	// ---- ayarlar arayuzu (canli uygulama, onay)
	"v3.set.live.ok":          {" (çalışan istemciye canlı uygulandı)", " (applied live to the running client)", "", ""},
	"v3.set.live.fail":        {" (çalışan istemciye uygulanamadı: %v)", " (could not apply to the running client: %v)", "", ""},
	"v3.set.confirm.insecure": {"Düz metin ws:// açılsın mı? Token ve trafik şifrelenmeden gider. Onay: y, iptal: başka tuş.", "Enable plaintext ws://? The token and traffic go unencrypted. Confirm: y, cancel: any other key.", "", ""},
	"v3.set.help.confirm":     {"y onayla · diğer tuş iptal", "y confirm · any other key cancels", "", ""},

	// ---- forward
	"v3.fw.flag.skipverify":     {"sunucu TLS sertifikasını doğrulama (yalnızca geliştirme; --insecure'den farklıdır)", "do not verify the server TLS certificate (development only; unlike --insecure)", "", ""},
	"v3.fw.insecure_deprecated": {"Uyarı: forward'da --insecure kullanımdan kalkıyor; sertifika doğrulamayı atlamak için --skip-verify kullanın.", "Warning: --insecure on forward is deprecated; use --skip-verify to skip certificate verification.", "", ""},
	"v3.fw.cfg_insecure":        {"Uyarı: ayar dosyasında insecure açık; sunucu sertifikası doğrulanmayacak (yalnızca geliştirme).", "Warning: insecure is on in the config file; the server certificate will not be verified (development only).", "", ""},
	"v3.fw.err.dial":            {"Bağlantı kurulamadı (%s): %v", "Could not connect (%s): %v", "", ""},
	"v3.fw.err.badca":           {"CA dosyasında geçerli sertifika yok: %s", "no valid certificate in the CA file: %s", "", ""},

	// ---- servis
	"v3.svc.updated":        {"✓ Zorven servisi güncellendi (zaten kuruluydu; ayarlar ve birim yenilendi).", "✓ Zorven service updated (it was already installed; settings and unit refreshed).", "", ""},
	"v3.svc.flag.purge":     {"sistem ayar dosyasını (token dahil) da sil", "also delete the system config file (including the token)", "", ""},
	"v3.svc.config.remains": {"Not: ayar dosyası (token içerir) silinmedi: %s — tamamen silmek için --purge kullanın.", "Note: the config file (contains the token) was not deleted: %s — use --purge to delete it too.", "", ""},
	"v3.svc.purged":         {"✓ Ayar dosyası silindi: %s", "✓ Config file deleted: %s", "", ""},
	"v3.svc.purge.err":      {"Ayar dosyası silinemedi (%s): %v", "Could not delete the config file (%s): %v", "", ""},

	// ---- zorven access / net
	"v3.net.serving": {"Zorven Net: %s (%s) -> %s", "Zorven Net: %s (%s) -> %s", "Zorven Net: %s (%s) -> %s", "Zorven Net: %s (%s) -> %s"},
}

// v3Overrides: mevcut anahtarlarin 0.3.0'da duzeltilen metinleri (--insecure
// aslinda sertifika atlamak degil, TLS'siz duz ws:// demektir).
var v3Overrides = map[string][4]string{
	"main.flag.insecure": {"TLS yerine düz ws:// kullan — token açıktan gider! (yalnızca yerel geliştirme; sertifika atlamak için forward'da --skip-verify)", "use plain ws:// instead of TLS — the token travels in the clear! (local development only; to skip certificate checks on forward use --skip-verify)", "", ""},
	"net.flag.insecure":  {"TLS yerine ws:// (token açıktan gider; yalnızca yerel geliştirme)", "ws:// instead of TLS (the token travels in the clear; local development only)", "", ""},
	"set.insecure":       {"Düz metin ws:// (TLS yok)", "Plain ws:// (no TLS)", "", ""},
	"set.hint.insecure":  {"Token ve trafik şifrelenmeden gider; yalnızca yerel geliştirme sunucusu için, üretimde kapalı tutun.", "The token and traffic go unencrypted; only for a local development server, keep it off in production.", "", ""},
	"set.ok.insecure":    {"Düz metin ws://: %s", "Plain ws://: %s", "", ""},
	"conn2.fw.flag.port": {"dinlenecek yerel port (verilmezse boş bir port seçilir ve yazdırılır)", "local port to listen on (if omitted a free port is chosen and printed)", "", ""},
	"conn.409hint":       {"İpucu: çalışan bir istemci var. Durum: zorven status — izinler: zorven enable terminal | zorven enable screen", "Tip: a client is already running. State: zorven status — permissions: zorven enable terminal | zorven enable screen", "", ""},
}

func init() {
	for k, v := range v3CLIMessages {
		messages[k] = v
	}
	for k, v := range v3Overrides {
		messages[k] = v
	}
}
