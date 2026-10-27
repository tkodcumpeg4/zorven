// ==============================================================================
// Zorven Desktop — arayuz metinleri (tr / en)
//
// Uc sozlugun anahtar kumesi ayni olmalidir (scripts: node i18n_check.js).
// Kullanim:
//   t('anahtar', {param: deger})           -> metin ({param} yer tutucu)
//   data-i18n="k"                          -> textContent
//   data-i18n-html="k"                     -> innerHTML (yalniz bu dosyadaki guvenilir metin)
//   data-i18n-placeholder="k", data-i18n-title="k"
// Go tarafindan donen metinler (hatalar, ipuclari) Go'da (messages.go) cevrilir.
// ==============================================================================
(function (root) {
  const DICT = {
    tr: {
      'auth.title': "Zorven'e Bağlan",
      'auth.sub': 'Hesabına bağlan; açık tünelleri ve logları izle. Tünel oluşturma ve tüm ayarlar <b>web panelinden</b> yapılır.',
      'auth.tabToken': 'Token ile',
      'auth.tabBrowser': 'Tarayıcıdan',
      'auth.connect': 'Bağlan',
      'auth.tokenHint': "Token'ı web panelinde <b>İstemciler</b> bölümünden alabilirsin.",
      'auth.browserLogin': 'Tarayıcıdan Giriş Yap',
      'auth.codePrompt': 'Tarayıcıda açılan sayfada bu kodu onayla:',
      'auth.waiting': 'Onay bekleniyor…',
      'auth.browserHint': 'Tarayıcıda <b>zorven.app</b> açılır; giriş yapıp kodu onayladığında uygulama otomatik bağlanır.',
      'auth.loginFailed': 'Giriş başarısız.',

      'top.logs': 'Loglar',
      'top.logout': 'Çıkış',

      'conn.subtitle': 'Zorven istemci durumu — tünel & ağ',
      'conn.connect': 'Bağlan',
      'conn.disconnect': 'Bağlantıyı Kes',
      'state.connected': 'Bağlı',
      'state.connecting': 'Bağlanıyor…',
      'state.reconnecting': 'Yeniden Bağlanıyor…',
      'state.stopped': 'Bağlı Değil',
      'state.fatal': 'Bağlanamadı',
      'sess.cli': 'Uzak CLI etkin',
      'sess.screen': 'Ekran paylaşımı etkin',

      'tunnels.title': 'Açık Tüneller',
      'tunnels.loading': 'Yükleniyor…',
      'tunnels.empty': '<div class="empty">Açık tünel yok. Tünelleri <b>web panelinden</b> (zorven.app) oluşturun.</div>',
      'tunnels.localService': 'yerel servis',
      'tunnels.open': 'Aç',

      'nt.title': 'Tam Ağ Katmanı',
      'nt.desc': 'Cihaza sanal ağ arayüzü ekler; yetkili eşlere IP veya <span class="mono">&lt;cihaz&gt;.zorven.internal</span> adıyla her uygulamadan erişirsiniz.',
      'nt.ip': 'IP',
      'nt.pool': 'Havuz',
      'nt.iface': 'Arayüz',
      'nt.dns': 'DNS',
      'nt.on': 'Açık',
      'nt.off': 'Kapalı',
      'nt.noAccess': 'Erişim yok',
      'nt.noService': 'Servis yok',
      'nt.btnOn': 'Aç',
      'nt.btnOff': 'Kapat',
      'nt.opening': 'Açılıyor…',
      'nt.closing': 'Kapatılıyor…',
      'nt.dnsNotRegistered': 'kayıtlı değil',
      'nt.peerOffline': 'çevrimdışı',
      'nt.peerDirect': 'doğrudan',
      'nt.peerRelay': 'relay',
      'nt.errNotConnected': 'Uygulama sunucuya bağlı değil{reason}; önce Bağlan\'a basın.',
      'nt.errSvcNotConnected': 'Zorven servisi çalışıyor ama sunucuya bağlı değil ({reason}). Servisi yeniden başlatın: yönetici PowerShell\'de "Restart-Service Zorven".',
      'nt.errNotOffered': 'Sunucu bu hesap için tam ağ katmanını sunmuyor (sunucuda kapalı olabilir).',
      'nt.errNotOfferedOrWaiting': 'Sunucu bu hesap için tam ağ katmanını sunmuyor veya bağlantı bekleniyor.',
      'nt.install': 'Servisi kur (yönetici onayı)',
      'nt.elevate': 'Yönetici olarak yeniden başlat',

      'hw.title': 'Canlı Donanım',
      'hw.cpu': 'CPU',
      'hw.ram': 'RAM',
      'hw.disk': 'Disk',

      'set.title': 'Ayarlar',
      'set.language': 'Dil / Language',
      'set.langAuto': 'Otomatik',
      'set.autostart': 'Bilgisayar açılınca başlat',
      'set.autostartDesc': 'Oturum açıldığında Zorven arka planda çalışır.',
      'set.hideTray': 'Kapatınca sistem tepsisine gizle',
      'set.hideTrayDesc': 'Pencere kapatılınca uygulama çıkmaz, tepside çalışmaya devam eder.',
      'set.noTerminal': 'Web CLI / Terminali kapat',
      'set.noTerminalDesc': 'Dashboard üzerinden uzak komut satırı erişimini engeller.',
      'set.noScreen': 'Ekran paylaşımını kapat',
      'set.noScreenDesc': 'Dashboard üzerinden masaüstü izleme ve kontrolünü engeller.',
      'set.saved': '✓ Kaydedildi',
      'set.version': 'Sürüm',
      'set.checkUpdate': 'Güncellemeleri denetle',
      'set.checking': 'Denetleniyor…',
      'set.installUpdate': 'Güncelle ve yeniden başlat',
      'set.downloading': 'İndiriliyor…',
      'upd.available': 'Yeni sürüm hazır: v{latest} (şu an v{current})',
      'upd.upToDate': 'En güncel sürümü kullanıyorsunuz (v{current}).',
      'upd.checkFailed': 'Güncelleme denetlenemedi.',
      'upd.installed': 'Güncelleme kuruldu, uygulama yeniden başlatılıyor…',

      'log.title': 'Canlı Loglar',
      'log.methodAll': 'Metot: Hepsi',
      'log.statusAll': 'Durum: Hepsi',
      'log.searchPh': 'path ara…',
      'log.minMsPh': 'min ms',
      'log.live': '● canlı',
      'log.paused': '● duraklatıldı',
      'log.clear': 'Temizle',
      'log.csv': 'CSV',
      'log.count': '{n} istek',
      'log.close': '✕ Kapat',
      'log.colTime': 'Saat',
      'log.colMethod': 'Metot',
      'log.colPath': 'Path',
      'log.colStatus': 'Durum',
      'log.colDuration': 'Süre',
      'log.empty': 'Tünel hazır — gelen istekler burada canlı akacak…',
      'log.csvHeader': 'zaman,metot,path,durum,sure_ms',
      'log.csvFile': 'zorven-loglar.csv',
    },

    en: {
      'auth.title': 'Connect to Zorven',
      'auth.sub': 'Connect to your account to watch open tunnels and logs. Creating tunnels and all settings are done in the <b>web panel</b>.',
      'auth.tabToken': 'With token',
      'auth.tabBrowser': 'From browser',
      'auth.connect': 'Connect',
      'auth.tokenHint': 'You can get the token in the web panel under <b>Clients</b>.',
      'auth.browserLogin': 'Sign in with browser',
      'auth.codePrompt': 'Confirm this code on the page opened in your browser:',
      'auth.waiting': 'Waiting for approval…',
      'auth.browserHint': '<b>zorven.app</b> opens in your browser; once you sign in and confirm the code, the app connects automatically.',
      'auth.loginFailed': 'Sign-in failed.',

      'top.logs': 'Logs',
      'top.logout': 'Sign out',

      'conn.subtitle': 'Zorven client status — tunnel & network',
      'conn.connect': 'Connect',
      'conn.disconnect': 'Disconnect',
      'state.connected': 'Connected',
      'state.connecting': 'Connecting…',
      'state.reconnecting': 'Reconnecting…',
      'state.stopped': 'Not Connected',
      'state.fatal': 'Could Not Connect',
      'sess.cli': 'Remote CLI active',
      'sess.screen': 'Screen sharing active',

      'tunnels.title': 'Open Tunnels',
      'tunnels.loading': 'Loading…',
      'tunnels.empty': '<div class="empty">No open tunnels. Create tunnels in the <b>web panel</b> (zorven.app).</div>',
      'tunnels.localService': 'local service',
      'tunnels.open': 'Open',

      'nt.title': 'Full Network Layer',
      'nt.desc': 'Adds a virtual network interface to the device; you can reach authorized peers from any application by IP or by <span class="mono">&lt;device&gt;.zorven.internal</span>.',
      'nt.ip': 'IP',
      'nt.pool': 'Pool',
      'nt.iface': 'Interface',
      'nt.dns': 'DNS',
      'nt.on': 'On',
      'nt.off': 'Off',
      'nt.noAccess': 'No access',
      'nt.noService': 'No service',
      'nt.btnOn': 'Turn on',
      'nt.btnOff': 'Turn off',
      'nt.opening': 'Turning on…',
      'nt.closing': 'Turning off…',
      'nt.dnsNotRegistered': 'not registered',
      'nt.peerOffline': 'offline',
      'nt.peerDirect': 'direct',
      'nt.peerRelay': 'relay',
      'nt.errNotConnected': 'The app is not connected to the server{reason}; press Connect first.',
      'nt.errSvcNotConnected': 'The Zorven service is running but is not connected to the server ({reason}). Restart the service: in an administrator PowerShell, "Restart-Service Zorven".',
      'nt.errNotOffered': 'The server does not offer the full network layer for this account (it may be turned off on the server).',
      'nt.errNotOfferedOrWaiting': 'The server does not offer the full network layer for this account, or the connection is pending.',
      'nt.install': 'Install service (admin approval)',
      'nt.elevate': 'Restart as administrator',

      'hw.title': 'Live Hardware',
      'hw.cpu': 'CPU',
      'hw.ram': 'RAM',
      'hw.disk': 'Disk',

      'set.title': 'Settings',
      'set.language': 'Dil / Language',
      'set.langAuto': 'Automatic',
      'set.autostart': 'Start when the computer starts',
      'set.autostartDesc': 'Zorven runs in the background after you sign in.',
      'set.hideTray': 'Hide to system tray on close',
      'set.hideTrayDesc': 'Closing the window does not quit the app; it keeps running in the tray.',
      'set.noTerminal': 'Disable Web CLI / Terminal',
      'set.noTerminalDesc': 'Blocks remote command-line access from the dashboard.',
      'set.noScreen': 'Disable screen sharing',
      'set.noScreenDesc': 'Blocks desktop viewing and control from the dashboard.',
      'set.saved': '✓ Saved',
      'set.version': 'Version',
      'set.checkUpdate': 'Check for updates',
      'set.checking': 'Checking…',
      'set.installUpdate': 'Update and restart',
      'set.downloading': 'Downloading…',
      'upd.available': 'New version available: v{latest} (currently v{current})',
      'upd.upToDate': 'You are using the latest version (v{current}).',
      'upd.checkFailed': 'Could not check for updates.',
      'upd.installed': 'Update installed, the app is restarting…',

      'log.title': 'Live Logs',
      'log.methodAll': 'Method: All',
      'log.statusAll': 'Status: All',
      'log.searchPh': 'search path…',
      'log.minMsPh': 'min ms',
      'log.live': '● live',
      'log.paused': '● paused',
      'log.clear': 'Clear',
      'log.csv': 'CSV',
      'log.count': '{n} requests',
      'log.close': '✕ Close',
      'log.colTime': 'Time',
      'log.colMethod': 'Method',
      'log.colPath': 'Path',
      'log.colStatus': 'Status',
      'log.colDuration': 'Duration',
      'log.empty': 'Tunnel ready — incoming requests will stream here live…',
      'log.csvHeader': 'time,method,path,status,duration_ms',
      'log.csvFile': 'zorven-logs.csv',
    },

  };

  const LANGS = ['tr', 'en'];
  let lang = 'en';

  function norm(l) {
    l = String(l || '').toLowerCase().slice(0, 2);
    return LANGS.includes(l) ? l : '';
  }
  function guess() { return norm(root.navigator && root.navigator.language) || 'en'; }

  // t: etkin dilde metin; eksikse Ingilizce, o da yoksa anahtarin kendisi.
  function t(key, params) {
    let s = (DICT[lang] && DICT[lang][key]) || DICT.en[key] || key;
    if (params) s = s.replace(/\{(\w+)\}/g, (m, k) => (params[k] != null ? params[k] : m));
    return s;
  }

  // apply: data-i18n* niteliklerini uygular.
  function apply(scope) {
    const doc = root.document;
    if (!doc) return;
    const r = scope || doc;
    doc.documentElement.lang = lang;
    r.querySelectorAll('[data-i18n]').forEach(el => { el.textContent = t(el.dataset.i18n); });
    r.querySelectorAll('[data-i18n-html]').forEach(el => { el.innerHTML = t(el.dataset.i18nHtml); });
    r.querySelectorAll('[data-i18n-placeholder]').forEach(el => { el.placeholder = t(el.dataset.i18nPlaceholder); });
    r.querySelectorAll('[data-i18n-title]').forEach(el => { el.title = t(el.dataset.i18nTitle); });
  }

  function setLang(l) { lang = norm(l) || 'en'; apply(); return lang; }
  function getLang() { return lang; }

  const api = { DICT, LANGS, t, apply, setLang, getLang, guess };
  root.ZI18n = api;
  root.t = t;
  if (typeof module !== 'undefined' && module.exports) module.exports = api;
})(typeof window !== 'undefined' ? window : globalThis);
