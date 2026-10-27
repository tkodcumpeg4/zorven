// ==============================================================================
// Zorven Desktop — v3: sadece Tüneller + Loglar (ayarlar/tünel config web'de)
// ==============================================================================
const api = () => window.go?.main?.App;
const $ = (id) => document.getElementById(id);

let currentStatus = null;
let logWin = null;         // ayrı log penceresi referansı
let totalReq = 0;

const STATES = {
  connected:    { cls: 'badge-connected',  key: 'state.connected' },
  connecting:   { cls: 'badge-connecting', key: 'state.connecting' },
  reconnecting: { cls: 'badge-connecting', key: 'state.reconnecting' },
  stopped:      { cls: 'badge-stopped',    key: 'state.stopped' },
  fatal:        { cls: 'badge-stopped',    key: 'state.fatal' },
};

// ---------------------------------------------------------------- AUTH GATE
function showAuth() { $('auth').hidden = false; $('main').hidden = true; }
function showMain() { $('auth').hidden = true; $('main').hidden = false; }

// auth sekmeleri
document.querySelectorAll('.auth-tab').forEach(t => t.addEventListener('click', () => {
  document.querySelectorAll('.auth-tab').forEach(x => x.classList.toggle('is-active', x === t));
  $('authToken').hidden = t.dataset.pane !== 'token';
  $('authBrowser').hidden = t.dataset.pane !== 'browser';
}));

$('btnTokenConnect').addEventListener('click', async () => {
  const err = await api().SetToken($('tokenInput').value);
  if (err) { $('tokenErr').textContent = err; $('tokenErr').hidden = false; }
  else { $('tokenErr').hidden = true; showMain(); }
});

$('btnBrowserLogin').addEventListener('click', async () => {
  $('browserErr').hidden = true;
  $('btnBrowserLogin').disabled = true;
  const res = JSON.parse(await api().StartDeviceLogin());
  if (res.error) {
    $('browserErr').textContent = res.error; $('browserErr').hidden = false;
    $('btnBrowserLogin').disabled = false;
    return;
  }
  $('userCode').textContent = res.user_code;
  $('browserWaiting').hidden = false;
  $('btnBrowserLogin').hidden = true;
});

// ---------------------------------------------------------------- STATUS / TÜNELLER
function renderStatus(s) {
  if (!s) return;
  currentStatus = s;
  const meta = STATES[s.state] ?? STATES.stopped;
  const badge = $('connBadge');
  badge.className = 'badge ' + meta.cls;
  badge.textContent = t(meta.key);

  // Kalici hata / yeniden deneme nedeni (409, 429, sertifika...) kullaniciya
  // gorunur kalsin; yalnizca rozet "Bağlanamadı" nedenini anlatmiyordu.
  const cm = $('connMsg');
  if (cm && (s.state === 'fatal' || s.state === 'reconnecting') && s.message) {
    cm.textContent = s.message; cm.hidden = false; cm.dataset.sticky = '1';
  } else if (cm && cm.dataset.sticky) {
    cm.hidden = true; delete cm.dataset.sticky;
  }

  // Bağlan / Bağlantıyı Kes butonu (duruma göre)
  const live = s.state === 'connected' || s.state === 'connecting' || s.state === 'reconnecting';
  const btn = $('btnConn');
  if (btn) {
    btn.textContent = live ? t('conn.disconnect') : t('conn.connect');
    btn.className = live ? 'btn btn-secondary' : 'btn btn-primary';
    btn.style.color = live ? 'var(--danger)' : '';
    btn.style.borderColor = live ? 'rgba(255,92,92,.4)' : '';
  }

  // Aktif uzak oturum göstergesi: panelden biri CLI (terminal) veya ekran
  // paylaşımı açtığında kullanıcı bunu anlık görsün.
  const ta = s.terminal_active || 0, sa = s.screen_active || 0;
  const asEl = $('activeSessions');
  if (asEl) {
    const parts = [];
    if (ta > 0) parts.push('<span class="as-item as-cli"><svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><path d="m4 17 6-6-6-6"/><path d="M12 19h8"/></svg>' + esc(t('sess.cli')) + '' + (ta > 1 ? ' (' + ta + ')' : '') + '</span>');
    if (sa > 0) parts.push('<span class="as-item as-screen"><svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><rect width="18" height="12" x="3" y="4" rx="2"/><path d="M7 20h10M12 16v4"/></svg>' + esc(t('sess.screen')) + '' + (sa > 1 ? ' (' + sa + ')' : '') + '</span>');
    asEl.innerHTML = parts.join('');
    asEl.hidden = parts.length === 0;
  }

  const tunnels = s.tunnels || [];
  const list = $('tunnelList');
  if (tunnels.length && tunnels[0].hostnames) {
    const rows = [];
    for (const tn of tunnels) {
      for (const h of (tn.hostnames || [])) {
        rows.push(`<div class="tunnel-row">
          <div style="min-width:0">
            <a class="tunnel-url" href="#" data-url="https://${h}">https://${h}</a>
            <div class="tunnel-target">→ ${esc(tn.target || t('tunnels.localService'))}</div>
          </div>
          <button class="btn-ghost" data-open="https://${h}">${esc(t('tunnels.open'))}</button>
        </div>`);
      }
    }
    list.innerHTML = rows.join('') || t('tunnels.empty');
    list.querySelectorAll('[data-open]').forEach(b => b.addEventListener('click', () => api().OpenBrowser(b.dataset.open)));
  } else {
    list.innerHTML = t('tunnels.empty');
  }
}

function renderTelemetry(m) {
  if (!m) return;
  const set = (id, p, txt) => {
    const bar = $(id + 'Bar'), val = $(id + 'Val');
    const v = Math.max(0, Math.min(100, p || 0));
    if (bar) { bar.style.width = v + '%'; bar.classList.toggle('hot', v >= 85); }
    if (val) val.textContent = txt;
  };
  set('cpu', m.cpu_percent, Math.round(m.cpu_percent || 0) + '%');
  set('ram', m.memory_percent, Math.round(m.memory_percent || 0) + '%');
  set('disk', m.disk_percent, Math.round(m.disk_percent || 0) + '%');
}

// ---------------------------------------------------------------- LOG PANELİ (tam pencere)
let logRows = [], logLive = true, logCount = 0;
function esc(x){ return String(x==null?'':x).replace(/[&<>"]/g, c => ({'&':'&amp;','<':'&lt;','>':'&gt;','"':'&quot;'}[c])); }
function lpMatch(r){
  const m=$('lfMethod').value, st=$('lfStatus').value, q=$('lfQ').value, d=+$('lfDur').value;
  if (m && r.method !== m) return false;
  if (st && String(r.status).charAt(0) !== st) return false;
  if (q && !(r.path||'').includes(q)) return false;
  if (d && (r.duration_ms||0) < d) return false;
  return true;
}
function lpCls(c){ return c>=500?'lp-s5':c>=400?'lp-s4':c>=300?'lp-s3':'lp-s2'; }
function lpRender(){
  const vis = logRows.filter(lpMatch).slice(0, 800);
  $('lfEmpty').style.display = vis.length ? 'none' : 'block';
  $('lfBody').innerHTML = vis.map(r => '<tr><td style="color:var(--fg-subtle)">' + esc((r.ts||'').slice(11,19)) +
    '</td><td class="lp-m">' + esc(r.method||'GET') + '</td><td class="path" title="' + esc(r.path) + '">' + esc(r.path||'/') +
    '</td><td class="' + lpCls(r.status) + '">' + (r.status||200) + '</td><td style="color:var(--fg-muted)">' + (r.duration_ms||0) + 'ms</td></tr>').join('');
  $('lfCount').textContent = t('log.count', { n: logCount });
}
function pushLog(r){
  if (!r) return;
  logCount++; logRows.unshift(r); if (logRows.length > 3000) logRows.pop();
  if (!$('logPanel').hidden){ if (logLive) lpRender(); else $('lfCount').textContent = t('log.count', { n: logCount }); }
}
function openLogPanel(){ $('logPanel').hidden = false; try { api().ResizeWindow(1120, 760); } catch {} lpRender(); }
function closeLogPanel(){ $('logPanel').hidden = true; try { api().ResizeWindow(460, 820); } catch {} }
$('lfClose').addEventListener('click', closeLogPanel);
$('lfLive').addEventListener('click', () => { logLive = !logLive; $('lfLive').classList.toggle('paused', !logLive); $('lfLive').textContent = t(logLive ? 'log.live' : 'log.paused'); if (logLive) lpRender(); });
$('lfClear').addEventListener('click', () => { logRows = []; logCount = 0; lpRender(); });
['lfMethod','lfStatus','lfQ','lfDur'].forEach(id => $(id).addEventListener('input', lpRender));
$('lfExport').addEventListener('click', () => {
  const NL = String.fromCharCode(10);
  const vis = logRows.filter(lpMatch);
  const head = t('log.csvHeader');
  const lines = vis.map(r => [r.ts, r.method, '"'+(r.path||'').replace(/"/g,'""')+'"', r.status, r.duration_ms].join(','));
  const csv = head + NL + lines.join(NL);
  const a = document.createElement('a'); a.href = URL.createObjectURL(new Blob([csv],{type:'text/csv'})); a.download = t('log.csvFile'); a.click();
});

// ---------------------------------------------------------------- BUTONLAR
$('btnLogs').addEventListener('click', openLogPanel);
$('btnLogout').addEventListener('click', () => { api().Logout(); showAuth(); });

// Bağlan / Bağlantıyı Kes
$('btnConn').addEventListener('click', async () => {
  const live = currentStatus && ['connected', 'connecting', 'reconnecting'].includes(currentStatus.state);
  if (live) { api().Disconnect(); return; }
  renderStatus({ state: 'connecting' });
  const err = await api().Connect();
  if (err) { const m = $('connMsg'); m.textContent = err; m.hidden = false; setTimeout(() => m.hidden = true, 4000); }
});

// ---------------------------------------------------------------- AYARLAR
async function loadSettings() {
  try {
    const cfg = await api().GetConfig();
    $('setNoTerminal').checked = !!cfg.no_terminal;
    $('setNoScreen').checked = !!cfg.no_screen;
    $('setHideTray').checked = cfg.hide_to_tray !== false; // varsayilan açık
    $('setAutostart').checked = await api().GetAutostart();
  } catch { /* api hazir degil */ }
}
let _savedTimer;
function flashSaved() {
  const el = $('setSaved'); el.hidden = false;
  clearTimeout(_savedTimer); _savedTimer = setTimeout(() => el.hidden = true, 1800);
}
async function saveBehavior() {
  const err = await api().UpdateBehavior($('setNoTerminal').checked, $('setNoScreen').checked, $('setHideTray').checked);
  if (!err) flashSaved();
}
$('setNoTerminal').addEventListener('change', saveBehavior);
$('setNoScreen').addEventListener('change', saveBehavior);
$('setHideTray').addEventListener('change', saveBehavior);

// --- Surum + guncelleme ---
function updMsg(text, isErr) {
  const el = $('updMsg');
  el.textContent = text; el.hidden = !text;
  el.style.color = isErr ? 'var(--danger)' : '';
}
async function loadVersion() {
  try { $('verText').textContent = 'v' + (await api().GetVersion()); } catch { /* api hazir degil */ }
}
$('btnCheckUpdate').addEventListener('click', async () => {
  const btn = $('btnCheckUpdate');
  btn.disabled = true; btn.textContent = t('set.checking');
  $('btnInstallUpdate').hidden = true;
  try {
    const u = await api().CheckForUpdate();
    if (u.error) updMsg(u.error, true);
    else if (u.available) {
      updMsg(t('upd.available', { latest: u.latest, current: u.current }), false);
      $('btnInstallUpdate').hidden = false;
    } else updMsg(t('upd.upToDate', { current: u.current }), false);
  } catch { updMsg(t('upd.checkFailed'), true); }
  btn.disabled = false; btn.textContent = t('set.checkUpdate');
});
$('btnInstallUpdate').addEventListener('click', async () => {
  const btn = $('btnInstallUpdate');
  btn.disabled = true; btn.textContent = t('set.downloading');
  const err = await api().InstallUpdate();
  if (err) { updMsg(err, true); btn.disabled = false; btn.textContent = t('set.installUpdate'); }
  else updMsg(t('upd.installed'), false);
});
$('setAutostart').addEventListener('change', async (e) => {
  const err = await api().SetAutostart(e.target.checked);
  if (err) e.target.checked = !e.target.checked; else flashSaved();
});

// ---------------------------------------------------------------- TAM AĞ KATMANI (Mod B)
let ntTimer = null;
let ntBusy = false;
let ntState = null;

function ntRender(v) {
  ntState = v;
  const badge = $('ntBadge');
  const btn = $('btnNtToggle');
  // Yonetici olarak calisan masaustu tam ag katmanini kendi istemcisinde acar
  // (embedded); servis aranmaz. Eskiden yalniz service_found bakildigindan kart
  // 'Servis yok' gosterip Aç/Kapat dugmesini devre disi birakiyordu.
  const usable = (v.service_found || v.embedded) && !v.need_elevation;
  if (v.active) { badge.className = 'badge badge-connected'; badge.textContent = t('nt.on'); }
  else if (!usable) { badge.className = 'badge badge-stopped'; badge.textContent = t(v.need_elevation ? 'nt.noAccess' : 'nt.noService'); }
  else { badge.className = 'badge badge-stopped'; badge.textContent = t('nt.off'); }

  btn.textContent = t(v.active ? 'nt.btnOff' : 'nt.btnOn');
  btn.disabled = ntBusy || !usable || (!v.active && v.ok && !v.server_tun);
  btn.className = v.active ? 'btn btn-secondary' : 'btn btn-primary';

  $('ntDetails').hidden = !(v.active || (usable && v.ip));
  $('ntIP').textContent = v.ip || '—';
  $('ntPool').textContent = v.pool || '—';
  $('ntIface').textContent = v.interface || '—';
  $('ntDNS').textContent = !v.active ? '—'
    : v.dns_ok ? ('*.zorven.internal (' + (v.dns_method || 'ok') + ')')
    : (t('nt.dnsNotRegistered') + (v.dns_note ? ': ' + v.dns_note : ''));

  const peers = v.peers || [];
  $('ntPeers').hidden = peers.length === 0;
  $('ntPeers').innerHTML = peers.map(p => {
    const cls = !p.online ? 'off' : (p.path === 'direct' ? 'direct' : 'relay');
    const label = !p.online ? t('nt.peerOffline')
      : p.path === 'direct' ? (t('nt.peerDirect') + (p.rtt_ms ? ' · ' + p.rtt_ms + ' ms' : ''))
      : t('nt.peerRelay');
    return '<div class="nt-peer"><span><span class="nt-pname">' + esc(p.name) + '</span><span class="nt-pip">' + esc(p.ip) +
      '</span></span><span class="nt-path ' + cls + '">' + esc(label) + '</span></div>';
  }).join('');

  let err = v.error || '';
  if (v.ok && !v.active && !v.server_tun && (v.service_found || v.embedded)) {
    // Eski servis "connected" alanini bilmez: reason yoksa eski mesaj.
    if (v.embedded && !v.connected) err = t('nt.errNotConnected', { reason: v.reason ? ' (' + v.reason + ')' : '' });
    else if (!v.connected && v.reason) err = t('nt.errSvcNotConnected', { reason: v.reason });
    else if (v.connected) err = t('nt.errNotOffered');
    else err = t('nt.errNotOfferedOrWaiting');
  }
  $('ntErr').textContent = err; $('ntErr').hidden = !err;
  $('ntHint').textContent = v.hint || ''; $('ntHint').hidden = !v.hint;
  $('btnNtInstall').hidden = !(v.need_service && v.can_install);
  $('btnNtElevate').hidden = !(v.need_elevation && v.can_elevate);
  $('ntActions').hidden = $('btnNtInstall').hidden && $('btnNtElevate').hidden;
}

async function ntRefresh() {
  if (ntBusy || !api()?.NetTunStatus) return;
  try { ntRender(await api().NetTunStatus()); } catch (e) { /* sessiz */ }
}

function startNetTun() {
  if (ntTimer) return;
  ntRefresh();
  ntTimer = setInterval(() => { if (!$('main').hidden) ntRefresh(); }, 5000);
}

$('btnNtToggle').addEventListener('click', async () => {
  if (!ntState) return;
  ntBusy = true; $('btnNtToggle').disabled = true;
  $('btnNtToggle').textContent = t(ntState.active ? 'nt.closing' : 'nt.opening');
  try { ntRender(ntState.active ? await api().NetTunDown() : await api().NetTunUp()); }
  catch (e) { ntRender({ ...ntState, error: String(e) }); }
  finally { ntBusy = false; ntRender(ntState); }
});

$('btnNtInstall').addEventListener('click', async () => {
  $('btnNtInstall').disabled = true;
  const r = await api().NetTunInstallService();
  $('ntErr').textContent = r.error || ''; $('ntErr').hidden = !r.error;
  if (r.message) { $('ntHint').textContent = r.message; $('ntHint').hidden = false; }
  // UAC sonrasi servis kurulup baslayana kadar durumu sik sorgula.
  for (let i = 0; r.ok && i < 15; i++) {
    await new Promise(res => setTimeout(res, 2000));
    const v = await api().NetTunStatus();
    if (v.service_found) { ntRender(v); break; }
  }
  $('btnNtInstall').disabled = false;
});

$('btnNtElevate').addEventListener('click', async () => {
  const r = await api().NetTunRelaunchElevated();
  if (r && r.error) { $('ntErr').textContent = r.error; $('ntErr').hidden = false; }
});

// ---------------------------------------------------------------- DIL
// applyLanguage: arayuz dilini degistirir ve dinamik icerigi yeniden cizer
// (yeniden baslatma gerekmez). Go tarafi (SetLanguage) tepsiyi ve ajan
// mesajlarini kendisi gunceller.
function applyLanguage(lang) {
  ZI18n.setLang(lang);
  $('lfLive').textContent = t(logLive ? 'log.live' : 'log.paused');
  lpRender();
  if (currentStatus) renderStatus(currentStatus);
  if (ntState) { ntRender(ntState); ntRefresh(); }
  updMsg('', false);
}
async function loadLanguage() {
  try {
    const li = await api().GetLanguage();
    $('setLanguage').value = li.setting || '';
    if (li.effective !== ZI18n.getLang()) applyLanguage(li.effective);
  } catch { /* api hazir degil */ }
}
$('setLanguage').addEventListener('change', async (e) => {
  const err = await api().SetLanguage(e.target.value);
  if (err) { updMsg(err, true); return; }
  applyLanguage((await api().GetLanguage()).effective);
  flashSaved();
});

// ---------------------------------------------------------------- BAŞLANGIÇ
window.addEventListener('DOMContentLoaded', async () => {
  // Go'dan etkin dil gelene kadar tarayici dili (ilk cizimde bos/yanlis dil gorunmesin).
  ZI18n.setLang(ZI18n.guess());
  for (let i = 0; i < 50 && !api(); i++) await new Promise(r => setTimeout(r, 100));
  if (!api()) return;
  await loadLanguage();
  loadVersion();

  const authed = await api().IsAuthed();
  if (authed) { showMain(); loadSettings(); startNetTun(); renderStatus(await api().GetStatus()); }
  else showAuth();

  window.runtime?.EventsOn('status', renderStatus);
  window.runtime?.EventsOn('telemetry', renderTelemetry);
  window.runtime?.EventsOn('request_log', (r) => pushLog(r));
  window.runtime?.EventsOn('device:approved', () => { showMain(); loadSettings(); startNetTun(); });
  window.runtime?.EventsOn('device:error', (msg) => {
    $('browserErr').textContent = msg || t('auth.loginFailed'); $('browserErr').hidden = false;
    $('browserWaiting').hidden = true; $('btnBrowserLogin').hidden = false; $('btnBrowserLogin').disabled = false;
  });
  window.runtime?.EventsOn('auth:changed', async () => {
    (await api().IsAuthed()) ? (showMain(), startNetTun()) : showAuth();
  });
});
