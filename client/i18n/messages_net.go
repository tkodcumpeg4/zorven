package i18n

// netMessages: net/share/connect/forward/mcp komutlarinin metinleri.
var netMessages = map[string][3]string{
	// ---- ortak bayraklar (netConnFlags)
	"net.flag.server":   {"Zorven sunucusu (host:port)", "Zorven server (host:port)", ""},
	"net.flag.apitoken": {"API token (ZORVEN_API_TOKEN ile de verilebilir)", "API token (can also be set via ZORVEN_API_TOKEN)", ""},
	"net.flag.insecure": {"TLS yerine ws:// (yalnızca yerel geliştirme)", "ws:// instead of TLS (local development only)", ""},
	"net.flag.cacert":   {"kendi kendine imzalı sunucu sertifikası dosyası", "self-signed server certificate file", ""},
	"net.err.token": {
		"API token gerekli: --api-token veya ZORVEN_API_TOKEN (panel > API & Güvenlik, net:read/net:write veya net:connect kapsamı)",
		"API token required: --api-token or ZORVEN_API_TOKEN (panel > API & Security, net:read/net:write or net:connect scope)",
		""},
	"net.err.agenttoken": {"bu bir istemci (ajan) token'ı; bu komut bir API token ister", "this is a client (agent) token; this command needs an API token", ""},

	// ---- zorven share
	"net.share.short": {
		"Bu cihazın bir portunu seçili kişi/grup/cihazla uçtan uca şifreli paylaşır (yönetici hakkı gerekmez)",
		"Shares a port of this device end-to-end encrypted with selected users/groups/devices (no admin rights needed)",
		""},
	"net.share.long": {
		"Yerel bir portu WireGuard ile YALNIZ belirttiğiniz alıcılara açar. Sunucu içeriği göremez.\n" +
			"Paylaşım bu komut çalıştığı sürece geçerlidir (Ctrl+C ile biter); --persist ile kalıcı olur\n" +
			"(ajan çalıştıkça sunulur). Alıcı: zorven access <ad> --local <port>.\n\n" +
			"Alıcı biçimleri: ali@firma.com (kullanıcı), user:<id>, group:everyone, group:role:admin, device:<ad>",
		"Opens a local port over WireGuard to ONLY the recipients you specify. The server cannot see the content.\n" +
			"The share lasts while this command runs (ends with Ctrl+C); with --persist it becomes permanent\n" +
			"(served as long as the agent runs). Recipient: zorven access <name> --local <port>.\n\n" +
			"Recipient formats: ali@company.com (user), user:<id>, group:everyone, group:role:admin, device:<name>",
		""},
	"net.share.err.port":     {"geçersiz port %q", "invalid port %q", ""},
	"net.share.err.to":       {"--to gerekli: paylaşımın kime açılacağını belirtin", "--to is required: specify who the share is opened to", ""},
	"net.share.err.expires":  {"geçersiz --expires %q (en az 1m, örnek 24h)", "invalid --expires %q (minimum 1m, e.g. 24h)", ""},
	"net.share.err.notoken":  {"istemci token'ı bulunamadı: zorven authtoken <TOKEN>", "client token not found: zorven authtoken <TOKEN>", ""},
	"net.share.connecting":   {"Zorven sunucusuna bağlanılıyor (%s)...", "Connecting to the Zorven server (%s)...", ""},
	"net.share.connerr":      {"Bağlantı hatası: %s", "Connection error: %s", ""},
	"net.share.flag.to":      {"alıcı (tekrarlanabilir): kullanıcı, grup veya cihaz", "recipient (repeatable): user, group or device", ""},
	"net.share.flag.udp":     {"UDP portunu paylaş (varsayılan TCP)", "share a UDP port (default TCP)", ""},
	"net.share.flag.name":    {"paylaşım adı (varsayılan <cihaz>-<port>)", "share name (default <device>-<port>)", ""},
	"net.share.flag.expires": {"paylaşımın ömrü (örnek 24h)", "share lifetime (e.g. 24h)", ""},
	"net.share.flag.persist": {
		"komut/ajan bitince paylaşımı silme (çalışan servis üzerinden oluşturulan paylaşımlar zaten kalıcıdır)",
		"keep the share after the command/agent exits (shares created via a running service are already permanent)",
		""},
	"net.share.flag.token": {"istemci token'ı (zrv_live_...)", "client token (zrv_live_...)", ""},
	"net.path.direct":      {"  yol: %s doğrudan (%s)", "  path: %s direct (%s)", ""},
	"net.path.relay":       {"  yol: %s relay üzerinden", "  path: %s via relay", ""},

	// ---- zorven access
	"net.access.short": {
		"Size açılan bir paylaşıma yerel 127.0.0.1 portu üzerinden bağlanır (WireGuard, yönetici hakkı gerekmez)",
		"Connects to a share opened to you via a local 127.0.0.1 port (WireGuard, no admin rights needed)",
		""},
	"net.access.long": {
		"API token ile geçici bir ağ düğümü olarak katılır ve yerel bir portu paylaşıma yönlendirir.\n" +
			"UDP paylaşımları için yerel UDP soketi açılır.",
		"Joins as a temporary network node using an API token and forwards a local port to the share.\n" +
			"For UDP shares a local UDP socket is opened.",
		""},
	"net.access.err.bind":    {"--bind yalnızca loopback olabilir (yerel dinleyicide kimlik doğrulama yok)", "--bind can only be a loopback address (the local listener has no authentication)", ""},
	"net.access.err.session": {"ağ oturumu kurulamadı", "could not establish the network session", ""},
	"net.access.err.listen":  {"dinlenemedi (%s)", "could not listen (%s)", ""},
	"net.access.quit":        {"Çıkmak için Ctrl+C.", "Press Ctrl+C to quit.", ""},
	"net.access.flag.local":  {"yerel port (varsayılan: paylaşım portu)", "local port (default: the share's port)", ""},
	"net.access.flag.bind":   {"yerel dinleme adresi (yalnızca loopback)", "local listen address (loopback only)", ""},

	// ---- tablolar
	"net.tbl.peer":      {"EŞ (BU CİHAZ)", "PEER (THIS DEVICE)", ""},
	"net.tbl.peer2":     {"EŞ", "PEER", ""},
	"net.tbl.status":    {"DURUM", "STATUS", ""},
	"net.tbl.path":      {"YOL", "PATH", ""},
	"net.tbl.node":      {"DÜĞÜM", "NODE", ""},
	"net.tbl.kind":      {"TÜR", "TYPE", ""},
	"net.tbl.share":     {"PAYLAŞIM", "SHARE", ""},
	"net.tbl.device":    {"CİHAZ", "DEVICE", ""},
	"net.tbl.relation":  {"İLİŞKİ", "RELATION", ""},
	"net.tbl.owner":     {"SAHİP", "OWNER", ""},
	"net.online":        {"çevrimiçi", "online", ""},
	"net.offline":       {"çevrimdışı", "offline", ""},
	"net.path.l.direct": {"doğrudan", "direct", ""},
	"net.path.l.relay":  {"relay üzerinden", "via relay", ""},
	"net.path.l.mixed":  {"karışık", "mixed", ""},
	"net.status.short":  {"Yerel Mod B (TUN) durumunu, düğümleri (IP, çevrimiçi) ve paylaşımları listeler", "Lists the local Mode B (TUN) state, nodes (IP, online) and shares", ""},
	"net.status.long": {
		"Çalışan istemciden tam ağ katmanı durumunu (IP, arayüz, eşler, yol direct/relay, RTT, DNS) yazar.\n" +
			"API token verilirse (ZORVEN_API_TOKEN) sunucudaki düğüm ve paylaşım listesi de gösterilir.",
		"Prints the full network layer state from the running client (IP, interface, peers, path direct/relay, RTT, DNS).\n" +
			"If an API token is given (ZORVEN_API_TOKEN), the server's node and share lists are shown too.",
		""},
	"net.status.hint":   {"(sunucu düğüm/paylaşım listesi için API token verin: --api-token veya ZORVEN_API_TOKEN)", "(to see the server node/share list, provide an API token: --api-token or ZORVEN_API_TOKEN)", ""},
	"net.status.server": {"sunucu %d: %s", "server %d: %s", ""},
	"net.root.short":    {"Zorven Net (WireGuard) komutları: up/down (tam ağ), status", "Zorven Net (WireGuard) commands: up/down (full network), status", ""},

	// ---- net up / down / status (TUN)
	"net.tun.install": {
		"Tam ağ katmanı (Mod B) ayrıcalıklı bir Zorven servisi ister:\n" +
			"  Linux/macOS : sudo zorven service install --token <TOKEN> && sudo zorven service start   (sonra: sudo zorven net up)\n" +
			"  Windows     : yönetici PowerShell: zorven service install --token <TOKEN>; zorven service start; zorven net up\n" +
			"  (kullanıcı modunda çalışan istemci/masaüstü uygulaması TUN açamaz; Mod A port paylaşımı etkilenmez)",
		"The full network layer (Mode B) needs a privileged Zorven service:\n" +
			"  Linux/macOS : sudo zorven service install --token <TOKEN> && sudo zorven service start   (then: sudo zorven net up)\n" +
			"  Windows     : admin PowerShell: zorven service install --token <TOKEN>; zorven service start; zorven net up\n" +
			"  (a client/desktop app running in user mode cannot open a TUN; Mode A port sharing is unaffected)",
		""},
	"net.tun.err.unreachable": {"çalışan Zorven istemcisine erişilemedi: %v\n%s", "could not reach the running Zorven client: %v\n%s", ""},
	"net.tun.err.perm":        {"Zorven servisi çalışıyor görünüyor ancak kontrol soketine erişim izniniz yok.\n%s", "The Zorven service appears to be running but you have no permission to access its control socket.\n%s", ""},
	"net.tun.err.none":        {"çalışan bir Zorven istemcisi/servisi bulunamadı.\n%s", "no running Zorven client/service found.\n%s", ""},
	"net.tun.err.badresp":     {"çalışan istemci beklenmeyen yanıt verdi", "the running client returned an unexpected response", ""},
	"net.tun.hint.win":        {"Servis soketine yalnızca SYSTEM/Administrators erişir: komutu yönetici PowerShell'den çalıştırın.", "Only SYSTEM/Administrators can access the service socket: run the command from an admin PowerShell.", ""},
	"net.tun.hint.unix":       {"Servis soketine yalnızca root erişir: komutun başına sudo ekleyin (sudo zorven net up).", "Only root can access the service socket: prefix the command with sudo (sudo zorven net up).", ""},
	"net.tun.err.old":         {"çalışan istemci tam ağ katmanını desteklemiyor (eski sürüm); 'zorven update' ile güncelleyip yeniden başlatın", "the running client does not support the full network layer (old version); update with 'zorven update' and restart it", ""},
	"net.tun.err.nonet":       {"çalışan istemcide Zorven Net etkin değil (sunucu wg_net sunmuyor veya istemci --no-net ile başladı)", "Zorven Net is not enabled in the running client (the server does not offer wg_net or the client was started with --no-net)", ""},
	"net.tun.on":              {"Mod B (tam ağ katmanı): AÇIK", "Mode B (full network layer): ON", ""},
	"net.tun.off":             {"Mod B (tam ağ katmanı): KAPALI", "Mode B (full network layer): OFF", ""},
	"net.tun.l.device":        {"cihaz:", "device:", ""},
	"net.tun.l.iface":         {"arayüz:", "interface:", ""},
	"net.tun.l.filter":        {"filtre:", "filter:", ""},
	"net.tun.iface":           {"%s   IP %s   havuz %s   MTU %d", "%s   IP %s   pool %s   MTU %d", ""},
	"net.tun.dns.ok":          {"*.zorven.internal -> %s (%s)   cevaplanan sorgu: %d", "*.zorven.internal -> %s (%s)   queries answered: %d", ""},
	"net.tun.dns.fail":        {"OS çözücüsüne kaydedilemedi: %s", "could not be registered with the OS resolver: %s", ""},
	"net.tun.dns.fail2":       {"(adları çözemezsiniz; IP ile erişim çalışır)", "(names will not resolve; access by IP still works)", ""},
	"net.tun.dropped":         {"düşürülen gelen %d, giden %d", "dropped inbound %d, outbound %d", ""},
	"net.tun.off.wait":        {"  (sunucuya bağlantı/kayıt bekleniyor veya sunucu Mod B sunmuyor)", "  (waiting for connection/registration to the server, or the server does not offer Mode B)", ""},
	"net.tun.off.nosrv":       {"  (sunucu bu sürümde Mod B'yi sunmuyor)", "  (the server does not offer Mode B in this version)", ""},
	"net.tun.off.hint":        {"  (açmak için: zorven net up)", "  (to enable: zorven net up)", ""},
	"net.tun.modea":           {"  Mod A IP: %s", "  Mode A IP: %s", "  Mode A IP: %s"},
	"net.up.short":            {"Tam ağ katmanını (Mod B, OS TUN) açar: sanal arayüz, IP, rota ve *.zorven.internal DNS", "Enables the full network layer (Mode B, OS TUN): virtual interface, IP, routes and *.zorven.internal DNS", ""},
	"net.up.long": {
		"Çalışan Zorven servisine bu cihazda bir TUN arayüzü (zorven0) açmasını söyler. Sonra\n" +
			"yetkili eşler aynı sanal ağda IP veya <cihaz>.zorven.internal adıyla (ssh, rdp, ping...) erişir.\n" +
			"Erişim kuralları panelden (Zorven Net > Tam ağ kuralları) tanımlanır; kural yoksa gelen trafik\n" +
			"düşürülür (default deny). Bu cihazdan başlatılan bağlantıların dönüşü her zaman kabul edilir.\n\n" +
			"Ayrıcalık gerekir: servis olarak kurun (sudo zorven service install) ve komutu sudo/yönetici ile çalıştırın.\n" +
			"100.64.0.0/10 aralığı yerel bir arayüzle (örneğin Tailscale) çakışıyorsa Mod B başlamaz;\n" +
			"panelden başka bir ağ aralığı seçin.",
		"Tells the running Zorven service to open a TUN interface (zorven0) on this device. Then\n" +
			"authorized peers reach it in the same virtual network by IP or <device>.zorven.internal (ssh, rdp, ping...).\n" +
			"Access rules are defined in the panel (Zorven Net > Full network rules); with no rules, inbound traffic\n" +
			"is dropped (default deny). Return traffic of connections started from this device is always accepted.\n\n" +
			"Privileges are required: install as a service (sudo zorven service install) and run the command with sudo/admin.\n" +
			"If 100.64.0.0/10 overlaps a local interface (e.g. Tailscale), Mode B will not start;\n" +
			"choose a different network range in the panel.",
		""},
	"net.down.short": {"Tam ağ katmanını kapatır (TUN arayüzü, rota ve DNS kaydı kaldırılır; Mod A port paylaşımı sürer)", "Disables the full network layer (removes the TUN interface, routes and DNS entry; Mode A port sharing continues)", ""},

	// ---- sharectl
	"share.err.old":      {"çalışan istemci bu işlemi desteklemiyor (eski sürüm); 'zorven update' ile güncelleyip yeniden başlatın", "the running client does not support this operation (old version); update with 'zorven update' and restart it", ""},
	"share.err.nonet":    {"çalışan istemcide Zorven Net etkin değil (sunucu wg_net sunmuyor veya istemci --no-net ile başladı)", "Zorven Net is not enabled in the running client (the server does not offer wg_net or the client was started with --no-net)", ""},
	"share.err.denied":   {"çalışan istemci bu kullanıcıya yerel erişim vermedi (%v); istemciyle aynı kullanıcı/sudo ile deneyin", "the running client did not grant local access to this user (%v); try with the same user as the client or with sudo", ""},
	"share.warn.unreach": {"Uyarı: çalışan istemciye erişilemedi (%v); yerel kontrol kanalı atlandı.", "Warning: could not reach the running client (%v); skipping the local control channel.", ""},
	"share.created":      {"Paylaşım oluşturuldu: %s", "Share created: %s", ""},
	"share.l.id":         {"id:", "id:", "ID:"},
	"share.l.port":       {"port:", "port:", ""},
	"share.l.to":         {"alıcılar:", "recipients:", ""},
	"share.l.expires":    {"bitiş:", "expires:", ""},
	"share.l.persist":    {"kalıcılık:", "persistence:", ""},
	"share.kind.fg":      {"ön plan istemci", "foreground client", ""},
	"share.kind.service": {"arka plan servisi", "background service", ""},
	"share.persistent":   {"kalıcı (%s yeniden başlasa da sürer; silmek için: zorven share rm %s)", "permanent (survives restarts of the %s; to remove: zorven share rm %s)", ""},
	"share.temporary":    {"%s çalıştığı sürece (kalıcı yapmak için --persist)", "while the %s runs (use --persist to make it permanent)", ""},
	"share.connect":      {"Bağlanmak için (alıcı): zorven access %s:%d --local %d", "To connect (recipient): zorven access %s:%d --local %d", ""},
	"share.err.noinfo":   {"çalışan istemci paylaşım bilgisi döndürmedi", "the running client returned no share information", ""},
	"share.err.noagent":  {"çalışan istemci bulunamadı; paylaşımlar yalnızca çalışan bir istemci (zorven / zorven service start) üzerinden yönetilir veya panelden (Paylaşımlar) silinir", "no running client found; shares are managed only through a running client (zorven / zorven service start) or removed from the panel (Shares)", ""},
	"share.rm.short":     {"Çalışan istemcinin sunduğu bir paylaşımı siler", "Deletes a share served by the running client", ""},
	"share.rm.done":      {"Paylaşım silindi: %s", "Share deleted: %s", ""},
	"share.ls.short":     {"Çalışan istemcinin sunduğu paylaşımları listeler", "Lists the shares served by the running client", ""},
	"share.ls.none":      {"Paylaşım yok.", "No shares.", ""},
	"share.ls.peers":     {"%d eş", "%d peers", ""},
	"share.tbl.name":     {"AD", "NAME", ""},
	"share.tbl.to":       {"ALICILAR", "RECIPIENTS", ""},
	"share.tbl.connect":  {"BAĞLANTI", "CONNECT", ""},
	"share.err.access":   {"geçersiz --access %q (biçim: paylaşım[:port]=yerel_port)", "invalid --access %q (format: share[:port]=local_port)", ""},
	"share.access.open":  {"Çalışan istemcide erişim açıldı: %s -> %s (istemci çalıştığı sürece)", "Access opened in the running client: %s -> %s (while the client runs)", ""},
	"share.conflict": {
		"Bu token ile başka bir istemci bağlı ama yerel kontrol kanalından ulaşılamadı " +
			"(istemci eski sürüm veya başka kullanıcıda olabilir). İstemciyi 'zorven update' ile güncelleyip yeniden başlatın, " +
			"aynı kullanıcı/sudo ile deneyin ya da paylaşımı panelden (Paylaşımlar) oluşturun.",
		"Another client is connected with this token but it could not be reached over the local control channel " +
			"(it may be an old version or run as another user). Update the client with 'zorven update' and restart it, " +
			"try with the same user/sudo, or create the share from the panel (Shares).",
		""},

	// ---- connect
	"conn2.short": {"Özel ağa bağlanır: yerel SOCKS5 proxy açar (Zorven Network)", "Connects to the private network: opens a local SOCKS5 proxy (Zorven Network)", ""},
	"conn2.long": {
		"Kurumun özel kaynaklarına (ör. db.internal:5432) yerel bir SOCKS5 proxy\n" +
			"üzerinden erişim sağlar. Kaynaklar internete açık değildir.\n\n" +
			"API token gerekir (panel > API & Güvenlik, en az tunnels:read kapsamı):\n" +
			"  ZORVEN_API_TOKEN=... zorven connect\n\n" +
			"Ardından uygulamanızda SOCKS5 proxy olarak 127.0.0.1:1080 kullanın.",
		"Provides access to your organization's private resources (e.g. db.internal:5432) through a\n" +
			"local SOCKS5 proxy. The resources are not exposed to the internet.\n\n" +
			"An API token is required (panel > API & Security, at least the tunnels:read scope):\n" +
			"  ZORVEN_API_TOKEN=... zorven connect\n\n" +
			"Then use 127.0.0.1:1080 as the SOCKS5 proxy in your application.",
		""},
	"conn2.err.token":      {"API token gerekli: --api-token veya ZORVEN_API_TOKEN", "API token required: --api-token or ZORVEN_API_TOKEN", ""},
	"conn2.err.agenttoken": {"bu bir istemci (ajan) token'ı; zorven connect bir API token ister", "this is a client (agent) token; zorven connect needs an API token", ""},
	"conn2.err.loopback":   {"%s loopback değil: LAN'a açmak için --gateway ve --allow-from kullanın", "%s is not a loopback address: to expose it to the LAN use --gateway and --allow-from", ""},
	"conn2.err.gateway":    {"--gateway için --allow-from zorunlu (ör. --allow-from 192.168.2.0/24)", "--allow-from is required with --gateway (e.g. --allow-from 192.168.2.0/24)", ""},
	"conn2.running":        {"Zorven Network: SOCKS5 proxy %s adresinde (sunucu: %s)", "Zorven Network: SOCKS5 proxy at %s (server: %s)", ""},
	"conn2.flag.listen":    {"yerel SOCKS5 dinleme adresi", "local SOCKS5 listen address", ""},
	"conn2.flag.gateway":   {"ağ geçidi modu (site-to-site): LAN'daki makinelere proxy aç", "gateway mode (site-to-site): serve the proxy to machines on the LAN", ""},
	"conn2.flag.allow":     {"ağ geçidi modunda izinli istemci aralıkları, virgülle (ör. 192.168.2.0/24)", "allowed client ranges in gateway mode, comma-separated (e.g. 192.168.2.0/24)", ""},
	"conn2.err.allow":      {"geçersiz --allow-from aralığı %q (ör. 192.168.2.0/24)", "invalid --allow-from range %q (e.g. 192.168.2.0/24)", ""},

	// ---- forward
	"conn2.fw.short": {"SNI-modu bir TCP tüneline yerel port açar (ziyaretçi tarafı)", "Opens a local port to an SNI-mode TCP tunnel (visitor side)", ""},
	"conn2.fw.long": {
		"SNI-modu bir ham TCP tünelini yerel bir porta bağlar. Örnek:\n" +
			"  zorven forward mc.ornek.com --port 25565\n" +
			"Ardından uygulamanızı 127.0.0.1:25565 adresine bağlayın.",
		"Binds an SNI-mode raw TCP tunnel to a local port. Example:\n" +
			"  zorven forward mc.example.com --port 25565\n" +
			"Then point your application at 127.0.0.1:25565.",
		""},
	"conn2.fw.err.host":      {"host gerekli (ör. mc.ornek.com)", "host required (e.g. mc.example.com)", ""},
	"conn2.fw.err.udp":       {"UDP forward henüz desteklenmiyor; UDP için rezerve-port modunu kullanın", "UDP forward is not supported yet; use reserved-port mode for UDP", ""},
	"conn2.fw.err.listen":    {"yerel port açılamadı (%s)", "could not open local port (%s)", ""},
	"conn2.fw.active":        {"Zorven forward etkin:", "Zorven forward active:", ""},
	"conn2.fw.local":         {"  Yerel  : %s", "  Local  : %s", ""},
	"conn2.fw.tunnel":        {"  Tünel  : %s (TLS-SNI -> %s)", "  Tunnel : %s (TLS-SNI -> %s)", ""},
	"conn2.fw.connect":       {"  Uygulamanızı %s adresine bağlayın. Durdurmak için Ctrl+C.", "  Point your application at %s. Press Ctrl+C to stop.", ""},
	"conn2.fw.flag.server":   {"tünel sunucusu (host:port, varsayılan zorven.app:443)", "tunnel server (host:port, default zorven.app:443)", ""},
	"conn2.fw.flag.port":     {"dinlenecek yerel port", "local port to listen on", ""},
	"conn2.fw.flag.listen":   {"dinlenecek yerel adres (ör. 0.0.0.0:25565); boş ise 127.0.0.1:<port>", "local address to listen on (e.g. 0.0.0.0:25565); if empty, 127.0.0.1:<port>", ""},
	"conn2.fw.flag.udp":      {"UDP (henüz desteklenmiyor)", "UDP (not supported yet)", ""},
	"conn2.fw.flag.insecure": {"sunucu TLS sertifikasını doğrulama (yalnızca geliştirme)", "do not verify the server TLS certificate (development only)", ""},

	// ---- mcp (yalniz cobra yardimi ve insan okuyan stderr/hata metinleri)
	"mcp.short": {"Zorven'i MCP sunucusu olarak çalıştırır (stdio; Claude/Cursor için)", "Runs Zorven as an MCP server (stdio; for Claude/Cursor)", ""},
	"mcp.long": {
		"Zorven araçlarını (cihaz, tünel, alan adı, istek logu, metrik, web kapısı) Model Context\n" +
			"Protocol üzerinden sunar. Silme araçları yalnızca token'da tunnels:delete / hostnames:delete kapsamı varsa listelenir.\n\n" +
			"API token gerekir (panel > API Tokenları). Salt okuma için *:read kapsamları yeterlidir;\n" +
			"token'da olmayan kapsamların araçları listelenmez.\n\n" +
			"  ZORVEN_API_TOKEN=zrv_api_... zorven mcp\n\n" +
			"Sunucu adresi `zorven` yapılandırmasından (varsayılan zorven.app) alınır; --server/--url ile değişir.",
		"Exposes Zorven tools (devices, tunnels, hostnames, request log, metrics, web gate) over the Model Context\n" +
			"Protocol. Delete tools are listed only if the token has the tunnels:delete / hostnames:delete scope.\n\n" +
			"An API token is required (panel > API Tokens). The *:read scopes are enough for read-only use;\n" +
			"tools for scopes the token does not have are not listed.\n\n" +
			"  ZORVEN_API_TOKEN=zrv_api_... zorven mcp\n\n" +
			"The server address comes from the `zorven` configuration (default zorven.app); override it with --server/--url.",
		""},
	"mcp.err.token":      {"API token gerekli: --api-token veya ZORVEN_API_TOKEN (panel > API Tokenları)", "API token required: --api-token or ZORVEN_API_TOKEN (panel > API Tokens)", ""},
	"mcp.err.agenttoken": {"bu bir istemci (ajan) token'ı; zorven mcp bir API token (zrv_api_...) ister", "this is a client (agent) token; zorven mcp needs an API token (zrv_api_...)", ""},
	"mcp.err.unreach":    {"%s adresine ulaşılamadı", "could not reach %s", ""},
	"mcp.err.unauth":     {"API token geçersiz veya süresi dolmuş (%s)", "API token is invalid or expired (%s)", ""},
	"mcp.err.status":     {"sunucu beklenmeyen yanıt verdi: HTTP %d", "the server returned an unexpected response: HTTP %d", ""},
	"mcp.err.decode":     {"sunucu yanıtı çözülemedi", "could not decode the server response", ""},
	"mcp.stdio":          {"zorven mcp: %s (stdio), kapsamlar: %s", "zorven mcp: %s (stdio), scopes: %s", ""},
	"mcp.http":           {"zorven mcp: http://%s (yalnızca yerel). Çıkmak için Ctrl+C.", "zorven mcp: http://%s (local only). Press Ctrl+C to quit.", ""},
	"mcp.err.loopback":   {"%s loopback değil: yerel MCP HTTP yalnızca 127.0.0.1/localhost üzerinde açılabilir", "%s is not a loopback address: local MCP HTTP can only listen on 127.0.0.1/localhost", ""},
	"mcp.err.url":        {"--url http:// veya https:// ile başlamalı", "--url must start with http:// or https://", ""},
	"mcp.flag.server":    {"Zorven sunucusu (host:port; varsayılan yapılandırmadan)", "Zorven server (host:port; default from configuration)", ""},
	"mcp.flag.url":       {"API taban adresi (ör. https://zorven.app); --server'ı ezer", "API base URL (e.g. https://zorven.app); overrides --server", ""},
	"mcp.flag.insecure":  {"https yerine http (yalnızca yerel geliştirme)", "http instead of https (local development only)", ""},
	"mcp.flag.project":   {"varsayılan proje (slug veya kimlik)", "default project (slug or id)", ""},
	"mcp.flag.http":      {"stdio yerine yerel streamable HTTP dinle (yalnızca loopback, ör. 127.0.0.1:8765)", "listen on local streamable HTTP instead of stdio (loopback only, e.g. 127.0.0.1:8765)", ""},
}

func init() {
	for k, v := range netMessages {
		messages[k] = v
	}
}
