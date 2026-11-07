// ==============================================================================
// Zorven Desktop - yan menulu arayuz: Genel bakis / Tuneller / Ag / Loglar / Ayarlar
// ==============================================================================
const api = () => window.go?.main?.App;
const $ = (id) => document.getElementById(id);
function esc(x) { return String(x == null ? '' : x).replace(/[&<>"']/g, c => ({ '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;', "'": '&#39;' }[c])); }

let currentStatus = null;
let svState = { found: false };     // CLI ajani / sistem hizmeti durumu (Go: service:state)
let installInfo = {};               // Windows kurulum modu (GetInstallInfo): installed | portable | legacy
let traffic = null;                 // gomulu ajan toplam trafik ozeti
let actionErr = '';                 // son eylem hatasi: bir sonraki eyleme kadar gorunur kalir
let connBusy = false;
let connBusyTimer = null;
let cfgCache = {};                  // GetConfig yaniti (sunucu adresi, son portlar)
let shareTarget = '';
let lastEntries = [], lastSvcMode = false, flyOn = false, flyOpenedAt = 0;               // o an paylasilan hedef (CurrentShare)

const STATES = {
  connected:    { cls: 'ok',   key: 'state.connected' },
  connecting:   { cls: 'warn', key: 'state.connecting' },
  reconnecting: { cls: 'warn', key: 'state.reconnecting' },
  stopped:      { cls: '',     key: 'state.stopped' },
  fatal:        { cls: 'bad',  key: 'state.fatal' },
};
const isLive = (s) => !!s && ['connected', 'connecting', 'reconnecting'].includes(s.state);

const ico = (id) => `<svg class="i" aria-hidden="true"><use href="#i-${id}"/></svg>`;
function setPill(el, cls, text) {
  el.className = 'pill' + (cls ? ' ' + cls : '');
  const tx = el.querySelector('.pill-t');
  if (tx) tx.textContent = text;
}
function announce(msg) { const el = $('srStatus'); if (el) el.textContent = msg; }

// ---------------------------------------------------------------- SAYFALAR
const PAGES = {
  overview: { key: 'nav.overview' },
  tunnels:  { key: 'nav.tunnels' },
  network:  { key: 'nav.network' },
  shares:   { key: 'ns.pageTitle' },
  logs:     { key: 'nav.logs' },
  settings: { key: 'nav.settings' },
};
let curPage = 'overview';
function showPage(p) {
  if (!PAGES[p]) p = 'overview';
  curPage = p;
  document.querySelectorAll('.nav').forEach(b => {
    const on = b.dataset.p === p;
    b.classList.toggle('on', on);
    if (on) b.setAttribute('aria-current', 'page'); else b.removeAttribute('aria-current');
  });
  document.querySelectorAll('.page').forEach(s => s.classList.toggle('on', s.id === 'p-' + p));
  $('pageTitle').textContent = t(PAGES[p].key);
  $('content').scrollTop = 0;
  if (p === 'logs') lpRender();
  if (p === 'shares') nsRefresh();
}
document.querySelectorAll('.nav').forEach(b => b.addEventListener('click', () => showPage(b.dataset.p)));
$('ovAll').addEventListener('click', () => showPage('tunnels'));
$('btnTheme').addEventListener('click', () => window.ZTheme && window.ZTheme.toggle());
$('btnShare').addEventListener('click', () => { showPage('tunnels'); $('sharePort').focus(); });

// ---------------------------------------------------------------- AUTH GATE
function showAuth() { $('auth').hidden = false; $('main').hidden = true; }
function showMain() { $('auth').hidden = true; $('main').hidden = false; }

// auth sekmeleri
document.querySelectorAll('.auth-tab').forEach(tab => tab.addEventListener('click', () => {
  document.querySelectorAll('.auth-tab').forEach(x => x.classList.toggle('is-active', x === tab));
  $('authToken').hidden = tab.dataset.pane !== 'token';
  $('authBrowser').hidden = tab.dataset.pane !== 'browser';
}));

// Giris sonrasi (token ile de tarayicidan da) ayni yol: ana ekran + ayarlar + tam ag + hizmet durumu.
function enterMain() {
  showMain();
  loadSettings();
  startNetTun();
  nsStart();
  loadService();
  loadUpdateInfo();
  loadVersion();
  showConfigNotice();
  refreshShare();
  api()?.GetStatus().then(renderStatus).catch(() => {});
}

// Cikis: butun arayuz durumunu sifirla (token, tunel, rozet, log, zamanlayici, sayfa).
function leaveMain() {
  currentStatus = null; svState = { found: false }; installInfo = {}; traffic = null; actionErr = ''; shareTarget = '';
  clearConnBusy();
  banners = []; renderBanner();
  $('tokenInput').value = ''; $('tokenErr').hidden = true;
  resetBrowserLogin();
  logRows = []; logCount = 0; lpRender();
  stopNetTun();
  nsStop();
  deployState = null; deployDocker = null; renderDeploy();
  ['cpu', 'ram', 'disk'].forEach(id => { $(id + 'Bar').style.width = '0%'; $(id + 'Bar').classList.remove('hot'); $(id + 'Val').textContent = '—'; });
  $('updMsg').hidden = true; $('updProgress').hidden = true; $('btnInstallUpdate').hidden = true; $('btnCancelUpdate').hidden = true; $('btnNotes').hidden = true;
  $('setErr').hidden = true; $('advErr').hidden = true; $('shareErr').hidden = true;
  $('diagMsg').textContent = t('set.diagHint');
  showPage('overview');
  renderAll();
  showAuth();
}

function resetBrowserLogin() {
  $('browserErr').hidden = true;
  $('browserWaiting').hidden = true;
  $('userCode').textContent = '····-····';
  $('btnBrowserLogin').hidden = false;
  $('btnBrowserLogin').disabled = false;
}

$('btnTokenConnect').addEventListener('click', async () => {
  const btn = $('btnTokenConnect');
  btn.disabled = true;
  try {
    const err = await api().SetToken($('tokenInput').value);
    if (err) { $('tokenErr').textContent = err; $('tokenErr').hidden = false; }
    else { $('tokenErr').hidden = true; $('tokenInput').value = ''; enterMain(); }
  } finally { btn.disabled = false; }
});
$('tokenInput').addEventListener('keydown', (e) => { if (e.key === 'Enter') $('btnTokenConnect').click(); });

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

$('btnBrowserCancel').addEventListener('click', async () => {
  try { await api().CancelDeviceLogin(); } catch { /* yoksay */ }
  resetBrowserLogin();
});

// ---------------------------------------------------------------- BANNER (bloklamayan)
let banners = []; // {id, text(), cls, icon, actionKey, onAction}
function addBanner(b) {
  banners = banners.filter(x => x.id !== b.id);
  banners.push(b);
  renderBanner();
}
function removeBanner(id) { banners = banners.filter(x => x.id !== id); renderBanner(); }
function renderBanner() {
  const el = $('banner');
  const b = banners[0];
  if (!b) { el.hidden = true; return; }
  el.hidden = false;
  el.className = 'banner' + (b.cls ? ' ' + b.cls : '');
  $('bannerIcon').setAttribute('href', '#i-' + (b.icon || 'alert'));
  $('bannerText').textContent = b.text();
  const act = $('bannerAction');
  act.hidden = !b.actionKey;
  if (b.actionKey) act.textContent = t(b.actionKey);
}
$('bannerClose').addEventListener('click', () => { if (banners[0]) removeBanner(banners[0].id); });
$('bannerAction').addEventListener('click', () => { const b = banners[0]; if (b && b.onAction) b.onAction(); });

let configNoticeShown = false;
async function showConfigNotice() {
  if (configNoticeShown) return;
  try {
    const msg = await api().GetConfigNotice();
    configNoticeShown = true;
    if (msg) addBanner({ id: 'cfg', text: () => msg, cls: '', icon: 'alert' });
  } catch { /* api hazir degil */ }
}

// ---------------------------------------------------------------- DURUM / TUNELLER
function setConnBusy() {
  connBusy = true;
  clearTimeout(connBusyTimer);
  connBusyTimer = setTimeout(() => { clearConnBusy(); renderAll(); }, 20000); // olay gelmezse dugme kilitli kalmasin
  renderAll();
}
function clearConnBusy() { connBusy = false; clearTimeout(connBusyTimer); }

function renderStatus(s) {
  if (!s) return;
  const prev = currentStatus && currentStatus.state;
  currentStatus = s;
  clearConnBusy();
  renderAll();
  if (prev !== s.state) {
    refreshShare();
    if (curPage === 'shares') nsRefresh();
    if (deployState) loadDeploy(false);
  }
}

// Sunucunun verdigi bir ad/adres icin {url, proto, openable} uretir.
function parseHost(h) {
  h = String(h || '').trim();
  let m = h.match(/^(tcp|udp):\/\/(.+)$/i);
  if (m) return { url: h, label: m[2], proto: m[1].toUpperCase(), openable: false };
  if (/^https?:\/\//i.test(h)) return { url: h, label: h.replace(/^https?:\/\//i, ''), proto: 'HTTP', openable: true };
  if (/:\d{1,5}$/.test(h)) return { url: 'tcp://' + h, label: h, proto: 'TCP', openable: false };
  return { url: 'https://' + h, label: h, proto: 'HTTP', openable: true };
}

function embeddedTunnelEntries(s) {
  const out = [];
  for (const tn of (s.tunnels || [])) {
    for (const h of (tn.hostnames || [])) out.push({ ...parseHost(h), target: tn.target });
  }
  return out;
}

function serviceTunnelEntries(sv) {
  return (sv.tunnels || []).map(tn => {
    const p = parseHost(tn.url || tn.name || '');
    const proto = (tn.protocol || p.proto || 'HTTP').toUpperCase();
    return { ...p, proto, openable: p.openable && proto === 'HTTP', target: tn.target, stats: tn.requests != null ? tn : null };
  });
}

const fmtBytes = (n) => {
  n = Number(n) || 0;
  if (n < 1024) return n + ' B';
  const u = ['KB', 'MB', 'GB', 'TB']; let i = -1;
  do { n /= 1024; i++; } while (n >= 1024 && i < u.length - 1);
  return n.toFixed(n >= 100 ? 0 : 1) + ' ' + u[i];
};
const fmtNum = (n) => { try { return Number(n || 0).toLocaleString(ZI18n.getLang()); } catch { return String(n || 0); } };
const fmtPct = (p) => { try { return p.toLocaleString(ZI18n.getLang(), { minimumFractionDigits: 1, maximumFractionDigits: 1 }) + '%'; } catch { return p.toFixed(1) + '%'; } };
const cell = (label, val) => `<div><small>${esc(label)}</small><span class="mono">${esc(val)}</span></div>`;

function fmtUptime(since) {
  const ms = Date.now() - Date.parse(since);
  if (!since || !(ms >= 0)) return '';
  const mins = Math.floor(ms / 60000);
  if (mins < 1) return t('dur.lt1');
  const h = Math.floor(mins / 60), m = mins % 60;
  return h > 0 ? t('dur.hm', { h, m }) : t('dur.m', { m });
}

function tunnelActionsHTML(e) {
  const open = e.openable
    ? `<button class="icon-btn" type="button" data-open="${esc(e.url)}" title="${esc(t('tunnels.open'))}" aria-label="${esc(t('tunnels.openAria', { url: e.url }))}">${ico('open')}</button>`
    : '<span style="width:28px;display:inline-block"></span>';
  const copy = `<button class="icon-btn" type="button" data-copy="${esc(e.url)}" title="${esc(t('tunnels.copy'))}" aria-label="${esc(t('tunnels.copyAria', { url: e.url }))}">${ico('copy')}</button>`;
  return `<span class="row" style="gap:6px;justify-content:flex-end">${copy}${open}</span>`;
}
const tagHTML = (proto) => `<span class="tag ${esc(proto.toLowerCase())}">${esc(proto)}</span>`;

function overviewItemHTML(e) {
  return `<div class="item" role="listitem" aria-label="${esc(e.label)}">${tagHTML(e.proto)}
    <div class="grow"><div class="host">${esc(e.label)}</div><div class="tgt">→ ${esc(e.target || t('tunnels.localService'))}</div></div>
    ${tunnelActionsHTML(e)}</div>`;
}

function tunnelTableHTML(entries) {
  const stats = entries.some(e => e.stats);
  let h = `<thead><tr><th scope="col">${esc(t('tunnels.colAddress'))}</th><th scope="col">${esc(t('tunnels.colType'))}</th><th scope="col">${esc(t('tunnels.colTarget'))}</th>`;
  if (stats) h += `<th scope="col" class="right">${esc(t('traffic.requests'))}</th><th scope="col" class="right">${esc(t('traffic.errors'))}</th><th scope="col" class="right">${esc(t('traffic.p95'))}</th>`;
  h += '<th scope="col"><span class="sr-only">' + esc(t('tunnels.colActions')) + '</span></th></tr></thead><tbody>';
  for (const e of entries) {
    h += `<tr><td class="host">${esc(e.label)}</td><td>${tagHTML(e.proto)}</td><td class="mono subtle">${esc(e.target || t('tunnels.localService'))}</td>`;
    if (stats) {
      const x = e.stats;
      h += x ? `<td class="right num">${esc(fmtNum(x.requests))}</td><td class="right num">${esc(fmtNum(x.errors))}</td><td class="right num">${esc(Math.round(x.p95_ms || 0))} ms</td>`
             : '<td class="right num">—</td><td class="right num">—</td><td class="right num">—</td>';
    }
    h += `<td class="right">${tunnelActionsHTML(e)}</td></tr>`;
  }
  return h + '</tbody>';
}

// Ozet kutulari: veri yoksa "—" (uydurma deger gosterilmez).
function renderTiles(svcMode, entries) {
  let req = null, errs = null, p50 = null, p95 = null, bytesIn = null, bytesOut = null;
  if (svcMode) {
    const tn = (svState.tunnels || []).filter(x => x.requests != null);
    if (tn.length) {
      req = tn.reduce((a, x) => a + (x.requests || 0), 0);
      errs = tn.reduce((a, x) => a + (x.errors || 0), 0);
      p95 = Math.max(...tn.map(x => x.p95_ms || 0));
      p50 = Math.max(...tn.map(x => x.p50_ms || 0));
      bytesIn = tn.reduce((a, x) => a + (x.bytes_in || 0), 0);
      bytesOut = tn.reduce((a, x) => a + (x.bytes_out || 0), 0);
    }
  } else if (traffic && currentStatus && currentStatus.state === 'connected') {
    req = traffic.requests; errs = traffic.errors; p50 = traffic.p50_ms; p95 = traffic.p95_ms;
  }
  const has = req != null;
  $('tReq').textContent = has ? fmtNum(req) : '—';
  $('tReqD').textContent = has ? t('tile.perMin', { n: requestsLastMinute() }) : '';
  $('tErr').textContent = has ? (req > 0 ? fmtPct(errs * 100 / req) : '0%') : '—';
  $('tErrD').textContent = has ? t('tile.errCount', { n: fmtNum(errs) }) : '';
  $('tP95').textContent = has ? Math.round(p95) + ' ms' : '—';
  $('tP95D').textContent = has ? t('tile.p50', { ms: Math.round(p50) }) : '';
  if (has && bytesIn != null && (bytesIn || bytesOut)) {
    $('tFourL').textContent = t('traffic.bytes');
    $('tFour').textContent = fmtBytes(bytesIn + bytesOut);
    $('tFourD').textContent = fmtBytes(bytesIn) + ' / ' + fmtBytes(bytesOut);
  } else {
    $('tFourL').textContent = t('tile.tunnels');
    $('tFour').textContent = String(entries.length);
    $('tFourD').textContent = '';
  }
}

function requestsLastMinute() {
  const cut = Date.now() - 60000;
  let n = 0;
  for (const r of logRows) {
    const ts = Date.parse(r.ts || '');
    if (!(ts >= cut)) break; // en yeni basta; eskiye gelince dur
    n++;
  }
  return n;
}

function renderSide(s, svcMode) {
  const pill = $('sideConn');
  let cls, text;
  if (svcMode) {
    cls = svState.connected ? 'ok' : 'warn';
    text = t(svState.connected ? 'svc.badge' : 'svc.badgeWaiting');
  } else {
    const meta = STATES[s.state] ?? STATES.stopped;
    cls = meta.cls; text = t(meta.key);
    if (s.state === 'connected') { const up = fmtUptime(s.since); if (up) text += ' · ' + up; }
  }
  setPill(pill, cls, text);
  pill.title = text;
  const srv = svcMode ? (svState.server || cfgCache.server_addr) : cfgCache.server_addr;
  $('sideServer').textContent = srv || ''; $('sideServer').hidden = !srv;
}

function renderHero(s, live, svcMode) {
  const hero = $('hero');
  let st, title;
  const kv = [];
  const k = (label, val) => kv.push(`<span>${esc(label)} <b>${esc(val)}</b></span>`);
  if (svcMode) {
    st = svState.connected ? 'connected' : 'connecting';
    title = t(svState.connected ? 'svc.badge' : 'svc.badgeWaiting');
    const srv = svState.server || cfgCache.server_addr;
    if (srv) k(t('hero.server'), srv);
    if (svState.version) k(t('svc.version'), 'v' + svState.version);
    if (svState.reconnects) k(t('svc.reconnects'), svState.reconnects);
    if (svState.started_at) { const d = new Date(svState.started_at); if (!isNaN(d)) k(t('hero.since'), d.toTimeString().slice(0, 5)); }
  } else {
    st = STATES[s.state] ? s.state : 'stopped';
    title = st === 'connected' ? t('hero.connected') : t(STATES[st].key);
    if (cfgCache.server_addr) k(t('hero.server'), cfgCache.server_addr);
    if (st === 'connected' && s.since) { const d = new Date(s.since); if (!isNaN(d)) k(t('hero.since'), d.toTimeString().slice(0, 5)); }
  }
  hero.className = 'hero st-' + st;
  $('heroTitle').textContent = title;
  $('heroKv').innerHTML = kv.join('');
  $('heroKv').hidden = kv.length === 0;
  return st;
}

function renderAll() {
  const s = currentStatus || { state: 'stopped' };
  const live = isLive(s);
  // Kurulu Windows kipinde baglanti HER ZAMAN hizmet uzerinden: kart ve hizmet gorunumu sabit.
  const inst = !!installInfo.installed;
  const svcShown = inst || (!!svState.found && !live);
  const svcMode = inst || (svcShown && !svState.takeover);

  renderHero(s, live, svcMode);
  renderSide(s, svcMode);

  // --- mesaj: eylem hatasi bir sonraki eyleme kadar kalir; yoksa kalici durum mesaji
  const cm = $('connMsg');
  let msg = actionErr;
  if (!msg && !svcMode && (s.state === 'fatal' || s.state === 'reconnecting') && s.message) msg = s.message;
  cm.textContent = msg; cm.hidden = !msg;

  // --- Baglan / Baglantiyi Kes (ust cubuk)
  const btn = $('btnConn');
  btn.hidden = svcMode;
  btn.disabled = connBusy;
  $('btnConnText').textContent = connBusy ? t('state.connecting') : (live ? t('conn.disconnect') : t('conn.connect'));
  btn.className = 'btn ' + (live ? 'danger' : 'primary');
  $('btnShare').hidden = svcMode;
  $('shareCard').hidden = svcMode;
  $('btnShareGo').disabled = connBusy;

  // --- uzak oturum gostergesi
  const ta = s.terminal_active || 0, sa = s.screen_active || 0;
  const parts = [];
  if (!svcMode) {
    if (ta > 0) parts.push(`<span class="pill ok"><span class="dot"></span>${ico('terminal')}<span class="pill-t">${esc(t('sess.cli'))}${ta > 1 ? ' (' + ta + ')' : ''}</span></span>`);
    if (sa > 0) parts.push(`<span class="pill ok"><span class="dot"></span>${ico('screen')}<span class="pill-t">${esc(t('sess.screen'))}${sa > 1 ? ' (' + sa + ')' : ''}</span></span>`);
  }
  $('activeSessions').innerHTML = parts.join('');
  $('activeSessions').hidden = parts.length === 0;

  // --- hizmet karti
  renderServiceCard(svcShown);

  // --- tunel listeleri
  let entries;
  if (svcMode) entries = serviceTunnelEntries(svState);
  else entries = embeddedTunnelEntries(s);
  const emptyHTML = svcMode ? esc(t('svc.noTunnels')) : t('tunnels.empty'); // tunnels.empty: guvenilir sozluk metni (<b>)

  const hp = $('heroPill');
  const showPill = (svcMode ? svState.connected : s.state === 'connected');
  hp.hidden = !showPill;
  if (showPill) setPill(hp, 'ok', t('tunnels.countOpen', { n: entries.length }));

  $('ovTunnels').innerHTML = entries.length ? entries.slice(0, 5).map(overviewItemHTML).join('') : `<div class="empty">${emptyHTML}</div>`;
  $('ovAll').hidden = entries.length <= 5;
  const nc = $('navCount');
  nc.textContent = entries.length; nc.hidden = entries.length === 0;
  $('tunnelsMeta').textContent = entries.length ? t('tunnels.countOpen', { n: entries.length }) : '';
  const tbl = $('tunnelTable');
  tbl.innerHTML = entries.length ? tunnelTableHTML(entries) : '';
  tbl.hidden = entries.length === 0;
  const te = $('tunnelEmpty');
  te.innerHTML = emptyHTML; te.hidden = entries.length !== 0;

  renderTiles(svcMode, entries);
  renderDevicePills();
  lastEntries = entries; lastSvcMode = svcMode;
  if (flyOn) renderFlyout();
}

function renderDevicePills() {
  setPill($('devTerm'), settingsCache.noTerminal ? '' : 'ok', t(settingsCache.noTerminal ? 'nt.off' : 'nt.on'));
  setPill($('devScreen'), settingsCache.noScreen ? '' : 'ok', t(settingsCache.noScreen ? 'nt.off' : 'nt.on'));
}

// Tunel listeleri: olay devri (sistem tarayicisinda ac / kopyala)
$('content').addEventListener('click', async (ev) => {
  const b = ev.target.closest('button');
  if (!b) return;
  if (b.dataset.open) { api().OpenBrowser(b.dataset.open); return; }
  if (b.dataset.copy) {
    let ok = true;
    try { const e = await api().CopyClipboard(b.dataset.copy); if (e) ok = false; }
    catch { try { await navigator.clipboard.writeText(b.dataset.copy); } catch { ok = false; } }
    if (ok) {
      const old = b.title;
      b.title = t('tunnels.copied'); b.classList.add('copied');
      announce(t('tunnels.copied'));
      setTimeout(() => { b.title = old; b.classList.remove('copied'); }, 1500);
    }
  }
});

function renderSetupCard() {
  const show = !!installInfo.portable;
  $('setupCard').hidden = !show;
  $('setupLegacy').hidden = !(show && installInfo.legacy);
}

function renderServiceCard(show) {
  const inst = !!installInfo.installed;
  $('svcCard').hidden = !show;
  $('svcRelease').hidden = inst || !(svState.found && svState.takeover && !show);
  // Kurulu kipte devralma secimi yok; hizmet yoksa/aktarim basarisizsa yalniz onarim.
  $('btnSvcTakeover').hidden = inst;
  $('svcTakeoverHint').hidden = inst;
  $('btnSvcRepair').hidden = !(inst && (!svState.found || installInfo.handoff_err));
  renderSetupCard();
  if (!show) return;
  const sv = svState;
  const rows = [
    cell(t('svc.kind'), t(sv.service ? 'svc.kindService' : 'svc.kindCli')),
    cell(t('svc.state'), sv.state || (sv.connected ? 'connected' : '—')),
  ];
  if (sv.version) rows.push(cell(t('svc.version'), 'v' + sv.version));
  if (sv.server) rows.push(cell('Server', sv.server));
  if (sv.reconnects) rows.push(cell(t('svc.reconnects'), sv.reconnects));
  $('svcDetails').innerHTML = rows.join('');
  const err = $('svcErr');
  let text = sv.denied ? t('svc.accessDenied') : sv.err ? t('svc.noAccess') + sv.err : (sv.last_error ? t('svc.lastError') + ': ' + sv.last_error : '');
  if (inst && installInfo.handoff_err) text = installInfo.handoff_err;
  else if (inst && !sv.found && !text) text = t('svc.waitingService');
  err.textContent = text; err.hidden = !text;
  const d = $('svcDisc');
  const ds = (sv.disconnects || []).slice(-3).reverse();
  d.hidden = ds.length === 0;
  d.innerHTML = ds.map(x => `<div>${esc((x.at || '').slice(11, 19))} — ${esc(x.reason || '')}</div>`).join('');
}

async function loadService() {
  try {
    if (api().GetInstallInfo) installInfo = (await api().GetInstallInfo()) || {};
    svState = (await api().GetServiceState()) || { found: false };
    renderAll();
  } catch { /* api hazir degil */ }
}
$('btnSvcRepair').addEventListener('click', async () => {
  $('btnSvcRepair').disabled = true;
  const r = await api().RepairService();
  actionErr = r.error || '';
  if (r.ok) await new Promise(res => setTimeout(res, 3000));
  await loadService();
  $('btnSvcRepair').disabled = false;
});
$('btnSetupInstall').addEventListener('click', async () => {
  const err = await api().StartSetup();
  $('setupErr').textContent = err || ''; $('setupErr').hidden = !err;
});
$('btnSvcRefresh').addEventListener('click', loadService);
$('btnSvcTakeover').addEventListener('click', async () => {
  actionErr = ''; setConnBusy();
  const err = await api().TakeOver();
  if (err) { actionErr = err; clearConnBusy(); }
  await loadService();
  renderAll();
});
$('btnSvcRelease').addEventListener('click', async () => {
  actionErr = '';
  await api().Disconnect();
  await api().ReleaseTakeover();
  await loadService();
});

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

// ---------------------------------------------------------------- HIZLI PAYLASIM (port)
async function refreshShare() {
  try { shareTarget = (await api().CurrentShare()) || ''; } catch { shareTarget = ''; }
  renderShare();
}
function renderShare() {
  const act = $('shareActive');
  act.hidden = !shareTarget || !isLive(currentStatus);
  $('shareTarget').textContent = shareTarget ? '→ ' + shareTarget : '';
  const rp = (cfgCache.recent_ports || []).filter(p => Number.isInteger(p));
  $('shareRecentRow').hidden = rp.length === 0;
  $('shareRecent').innerHTML = rp.map(p => `<button type="button" class="chip" data-port="${esc(p)}">${esc(p)}</button>`).join('');
}
async function doShare() {
  const raw = $('sharePort').value.trim();
  const port = /^\d{1,5}$/.test(raw) ? parseInt(raw, 10) : 0; // 0: Go tarafi yerel "gecersiz port" metnini doner
  $('shareErr').hidden = true; actionErr = '';
  setConnBusy();
  let err;
  try { err = await api().SharePort(port, $('shareSecure').checked); } catch (e) { err = String(e); }
  if (err) { $('shareErr').textContent = err; $('shareErr').hidden = false; clearConnBusy(); }
  else announce(t('share.active') + ' ' + raw);
  try { cfgCache = await api().GetConfig(); } catch { /* yoksay */ }
  await refreshShare();
  renderAll();
}
$('btnShareGo').addEventListener('click', doShare);
$('sharePort').addEventListener('keydown', (e) => { if (e.key === 'Enter') doShare(); });
$('shareRecent').addEventListener('click', (e) => {
  const b = e.target.closest('button[data-port]');
  if (b) { $('sharePort').value = b.dataset.port; $('sharePort').focus(); }
});
$('btnShareStop').addEventListener('click', async () => {
  $('shareErr').hidden = true; setConnBusy();
  let err;
  try { err = await api().StopSharing(); } catch (e) { err = String(e); }
  if (err) { $('shareErr').textContent = err; $('shareErr').hidden = false; clearConnBusy(); }
  await refreshShare();
  renderAll();
});

// ---------------------------------------------------------------- LOGLAR (tam sayfa)
let logRows = [], logLive = true, logCount = 0, lpRaf = 0;
const rowIp = (r) => r.ip || r.client_ip || '';
function lpMatch(r) {
  const m = $('lfMethod').value, st = $('lfStatus').value, q = $('lfQ').value, d = +$('lfDur').value;
  if (m && r.method !== m) return false;
  if (st && String(r.status).charAt(0) !== st) return false;
  if (q && !((r.path || '').includes(q) || rowIp(r).includes(q))) return false;
  if (d && (r.duration_ms || 0) < d) return false;
  return true;
}
function lpCls(c) { return c >= 500 ? 's5' : c >= 400 ? 's4' : c >= 300 ? 's3' : 's2'; }
const KNOWN_METHODS = ['get', 'post', 'put', 'patch', 'delete'];
// ts: RFC3339 (yerel saat dilimli); "time" yalnizca eski olaylar icin yedek.
const rowTime = (r) => (r.ts ? String(r.ts).slice(11, 19) : (r.time || ''));
function lpRender() {
  const vis = logRows.filter(lpMatch).slice(0, 800);
  $('lfEmpty').hidden = vis.length > 0;
  const hasIp = logRows.some(r => rowIp(r));
  document.querySelector('.logtable').classList.toggle('no-ip', !hasIp);
  $('lfBody').innerHTML = vis.map(r => {
    const m = String(r.method || 'GET'), ml = m.toLowerCase();
    return '<tr><td class="num subtle">' + esc(rowTime(r)) +
      '</td><td class="m' + (KNOWN_METHODS.includes(ml) ? ' m-' + ml : '') + '">' + esc(m) +
      '</td><td class="path" title="' + esc(r.path) + '">' + esc(r.path || '/') +
      '</td><td class="col-ip mono subtle">' + esc(rowIp(r)) +
      '</td><td class="' + lpCls(r.status) + '">' + esc(r.status || 200) +
      '</td><td class="right num subtle">' + esc(r.duration_ms || 0) + ' ms</td></tr>';
  }).join('');
  $('lfCount').textContent = t('log.count', { n: logCount });
}
function lpSchedule() { if (!lpRaf) lpRaf = requestAnimationFrame(() => { lpRaf = 0; lpRender(); }); }
function pushLog(r) {
  if (!r) return;
  logCount++; logRows.unshift(r); if (logRows.length > 3000) logRows.pop();
  if (curPage === 'logs' && !$('main').hidden) { if (logLive) lpSchedule(); else $('lfCount').textContent = t('log.count', { n: logCount }); }
  else if (curPage === 'overview') $('tReqD').textContent = $('tReqD').textContent && t('tile.perMin', { n: requestsLastMinute() });
}
function renderLiveBtn() {
  const b = $('lfLive');
  b.classList.toggle('paused', !logLive);
  b.setAttribute('aria-pressed', String(!logLive));
  $('lfLiveText').textContent = t(logLive ? 'log.pause' : 'log.resume');
  $('lfLiveUse').setAttribute('href', logLive ? '#i-pause' : '#i-play');
}
$('lfLive').addEventListener('click', () => { logLive = !logLive; renderLiveBtn(); if (logLive) lpRender(); });
$('lfClear').addEventListener('click', () => { logRows = []; logCount = 0; lpRender(); });
['lfMethod', 'lfStatus', 'lfQ', 'lfDur'].forEach(id => $(id).addEventListener('input', lpRender));
// CSV: formul enjeksiyonuna karsi =,+,-,@ ile baslayan hucrelerin onune kesme eklenir.
const csvCell = (v) => { let s = String(v == null ? '' : v); if (/^[=+\-@\t\r]/.test(s)) s = "'" + s; return '"' + s.replace(/"/g, '""') + '"'; };
$('lfExport').addEventListener('click', () => {
  const NL = String.fromCharCode(10);
  const vis = logRows.filter(lpMatch);
  const head = t('log.csvHeader');
  const lines = vis.map(r => [csvCell(r.ts || r.time), csvCell(r.method), csvCell(r.path), r.status, r.duration_ms].join(','));
  const csv = head + NL + lines.join(NL);
  const a = document.createElement('a'); a.href = URL.createObjectURL(new Blob([csv], { type: 'text/csv' })); a.download = t('log.csvFile'); a.click();
});

// ---------------------------------------------------------------- BUTONLAR
$('btnLogout').addEventListener('click', async () => {
  try { await api().Logout(); } catch { /* yoksay */ }
  leaveMain();
});

// Bağlan / Bağlantıyı Kes (islem suresince dugme kilitli)
$('btnConn').addEventListener('click', async () => {
  if (connBusy) return;
  actionErr = '';
  if (isLive(currentStatus)) { setConnBusy(); api().Disconnect(); return; }
  setConnBusy();
  const err = await api().Connect();
  if (err) { actionErr = err; clearConnBusy(); loadService(); }
  renderAll();
});

// ---------------------------------------------------------------- AYARLAR
let settingsCache = { noTerminal: false, noScreen: false, hideTray: true, notify: true, autostart: false, autoConnect: false, deploy: false };
function applySettingsToUI() {
  // Anahtar 'izin ver' anlaminda: isaretli = acik (no_terminal/no_screen'in tersi).
  $('setNoTerminal').checked = !settingsCache.noTerminal;
  $('setNoScreen').checked = !settingsCache.noScreen;
  $('setHideTray').checked = settingsCache.hideTray;
  $('setNotify').checked = settingsCache.notify;
  $('setAutostart').checked = settingsCache.autostart;
  $('setAutoConnect').checked = settingsCache.autoConnect;
  $('setDeploy').checked = settingsCache.deploy;
  renderDevicePills();
}
async function loadSettings() {
  try {
    const cfg = await api().GetConfig();
    cfgCache = cfg || {};
    settingsCache.noTerminal = !!cfg.no_terminal;
    settingsCache.noScreen = !!cfg.no_screen;
    settingsCache.hideTray = cfg.hide_to_tray !== false; // varsayilan açık
    settingsCache.notify = await api().GetNotifications();
    settingsCache.autostart = await api().GetAutostart();
    settingsCache.autoConnect = !!cfg.auto_connect;
    settingsCache.deploy = !!cfg.enable_deploy;
    applySettingsToUI();
    $('advServer').value = cfg.server_addr || '';
    $('advCA').value = cfg.ca_cert_path || '';
    renderShare();
    renderAll();
    loadDeploy(true);
  } catch { /* api hazir degil */ }
}
let _savedTimer;
function flashSaved() {
  $('setErr').hidden = true;
  const el = $('setSaved'); el.hidden = false;
  clearTimeout(_savedTimer); _savedTimer = setTimeout(() => el.hidden = true, 1800);
}
// Hata kalici gorunur (bir sonraki degisiklige kadar) ve anahtar eski haline doner.
function showSetErr(msg) { $('setSaved').hidden = true; $('setErr').textContent = msg; $('setErr').hidden = false; }
async function saveBehavior() {
  let err;
  try { err = await api().UpdateBehavior(!$('setNoTerminal').checked, !$('setNoScreen').checked, $('setHideTray').checked); }
  catch (e) { err = String(e); }
  if (err) { applySettingsToUI(); showSetErr(err); return; }
  settingsCache.noTerminal = !$('setNoTerminal').checked;
  settingsCache.noScreen = !$('setNoScreen').checked;
  settingsCache.hideTray = $('setHideTray').checked;
  renderDevicePills();
  flashSaved();
}
$('setNoTerminal').addEventListener('change', saveBehavior);
$('setNoScreen').addEventListener('change', saveBehavior);
$('setHideTray').addEventListener('change', saveBehavior);
$('setNotify').addEventListener('change', async (e) => {
  const want = e.target.checked;
  let err;
  try { err = await api().SetNotifications(want); } catch (x) { err = String(x); }
  if (err) { applySettingsToUI(); showSetErr(err); return; }
  settingsCache.notify = want; flashSaved();
});
$('setAutostart').addEventListener('change', async (e) => {
  const want = e.target.checked;
  let err;
  try { err = await api().SetAutostart(want); } catch (x) { err = String(x); }
  if (err) { applySettingsToUI(); showSetErr(err); return; }
  settingsCache.autostart = want; flashSaved();
});

$('setAutoConnect').addEventListener('change', async (e) => {
  const want = e.target.checked;
  let err;
  try { err = await api().SetAutoConnect(want); } catch (x) { err = String(x); }
  if (err) { applySettingsToUI(); showSetErr(err); return; }
  settingsCache.autoConnect = want; flashSaved();
});

// --- Zorven Apps dagitim izni (CLI enable_deploy) + Docker durumu ---
let deployState = null;   // GetDeployState yaniti
let deployDocker = null;  // son Docker yoklamasi {docker, detail}; null = denetleniyor
function renderDeploy() {
  const pill = $('deployPill'), msg = $('deployMsg'), s = deployState, d = deployDocker;
  $('btnDeployRestart').hidden = !(s && s.pending);
  if (!s) { setPill(pill, '', d ? '—' : t('set.dockerChecking')); msg.textContent = ''; return; }
  if (!d) setPill(pill, '', t('set.dockerChecking'));
  else if (d.docker === 'ok') setPill(pill, 'ok', t('set.dockerOk') + (d.detail ? ' ' + d.detail : ''));
  else if (d.docker === 'off') setPill(pill, s.enabled && !s.via_service ? 'bad' : '', t('set.dockerOff'));
  else if (d.docker === 'denied') setPill(pill, s.enabled && !s.via_service ? 'bad' : '', t('set.dockerDenied'));
  else setPill(pill, s.enabled && !s.via_service ? 'bad' : '', t('set.dockerError'));
  const parts = [];
  if (s.service) parts.push(t('set.deployService'));
  else if (s.secure && s.enabled) parts.push(t('set.deploySecure'));
  else if (s.pending) { parts.push(t('set.deployPending')); if (s.sessions > 0) parts.push(t('set.deploySessions')); }
  else if (!s.enabled) parts.push(t('set.deployOff'));
  else if (d && d.docker !== 'ok') parts.push(t('set.deployNoDocker'));
  else parts.push(t('set.deployActive'));
  if (d && d.docker === 'off') parts.push(t('set.dockerOffMsg'));
  else if (d && d.docker === 'denied') parts.push(t('set.dockerDeniedMsg'));
  else if (d && d.docker !== 'ok') parts.push(t('set.dockerErrorMsg', { detail: d.detail || '' }));
  if (s.via_service) parts.push(t('set.deployViaService'));
  msg.textContent = parts.join(' ');
}
// probe true: Docker'i da yokla (bir kac sn surebilir); false: yalniz izin/ajan durumu.
async function loadDeploy(probe) {
  if (!api()?.GetDeployState) return;
  if (probe) { deployDocker = null; renderDeploy(); }
  try {
    const s = await api().GetDeployState(!!probe);
    if (probe) deployDocker = { docker: s.docker, detail: s.detail || '' };
    deployState = s;
  } catch { deployState = null; }
  renderDeploy();
}
$('setDeploy').addEventListener('change', async (e) => {
  const want = e.target.checked;
  let err;
  try { err = await api().SetEnableDeploy(want); } catch (x) { err = String(x); }
  if (err) { applySettingsToUI(); showSetErr(err); return; }
  settingsCache.deploy = want; flashSaved();
  await loadDeploy(false);
  // Ajan izinle yeniden baslatiliyorsa (acik uzak oturum yoksa) durum bitince tazelenir.
  setTimeout(() => loadDeploy(false), 2500);
});
$('btnDeployRecheck').addEventListener('click', () => loadDeploy(true));
$('btnDeployRestart').addEventListener('click', async () => {
  actionErr = ''; setConnBusy();
  let err;
  try { err = await api().Reconnect(); } catch (e) { err = String(e); }
  if (err) { actionErr = err; clearConnBusy(); renderAll(); }
  loadDeploy(false);
});

$('btnSaveAdv').addEventListener('click', async () => {
  $('advErr').hidden = true;
  let err;
  try { err = await api().SaveAdvanced($('advServer').value, $('advCA').value); } catch (x) { err = String(x); }
  if (err) { $('advErr').textContent = err; $('advErr').hidden = false; return; }
  flashSaved();
  loadSettings();
});
$('btnOpenLogs').addEventListener('click', async () => {
  const err = await api().OpenLogFolder();
  if (err) showSetErr(err);
});
$('btnDiag').addEventListener('click', async () => {
  try { await api().CopyDiagnostics(); $('diagMsg').textContent = t('set.diagCopied'); }
  catch (e) { $('diagMsg').textContent = String(e); }
});

// --- Surum + guncelleme ---
let updInfo = null;
function updMsg(text, isErr) {
  const el = $('updMsg');
  el.textContent = text; el.hidden = !text;
  el.classList.toggle('upd-err', !!isErr);
}
async function loadVersion() {
  try { const v = 'v' + (await api().GetVersion()); $('verText').textContent = v; $('verBrand').textContent = v; } catch { /* api hazir degil */ }
}
function gotoUpdate() {
  showPage('settings');
  $('btnInstallUpdate').scrollIntoView({ behavior: 'smooth', block: 'center' });
}
function showUpdateAvailable(u) {
  updInfo = u;
  if (u && u.available) {
    updMsg(t('upd.available', { latest: u.latest, current: u.current }), false);
    $('btnInstallUpdate').hidden = false;
    $('btnNotes').hidden = false;
    addBanner({
      id: 'upd', cls: 'is-update', icon: 'download', actionKey: 'upd.view',
      text: () => t('upd.banner', { latest: u.latest }),
      onAction: () => { gotoUpdate(); removeBanner('upd'); },
    });
  } else {
    $('btnInstallUpdate').hidden = true; $('btnNotes').hidden = true;
    removeBanner('upd');
  }
}
async function loadUpdateInfo() {
  try { const u = await api().GetUpdateInfo(); if (u && u.available) showUpdateAvailable(u); } catch { /* api hazir degil */ }
}
$('btnCheckUpdate').addEventListener('click', async () => {
  const btn = $('btnCheckUpdate');
  btn.disabled = true; btn.textContent = t('set.checking');
  $('btnInstallUpdate').hidden = true; $('btnNotes').hidden = true;
  try {
    const u = await api().CheckForUpdate();
    if (u.error) updMsg(u.error, true);
    else if (u.available) showUpdateAvailable(u);
    else { updInfo = u; updMsg(t('upd.upToDate', { current: u.current }), false); removeBanner('upd'); }
  } catch { updMsg(t('upd.checkFailed'), true); }
  btn.disabled = false; btn.textContent = t('set.checkUpdate');
});
$('btnNotes').addEventListener('click', () => api().OpenReleaseNotes());
$('btnCancelUpdate').addEventListener('click', () => api().CancelUpdate());

function onUpdateProgress(p) {
  if (!p) return;
  const box = $('updProgress'), bar = $('updProgressBar');
  if (p.phase === 'error') { box.hidden = true; return; }
  box.hidden = false;
  if (p.phase === 'download') {
    const pct = p.total > 0 ? Math.min(100, Math.floor(p.done * 100 / p.total)) : 0;
    bar.style.width = pct + '%'; bar.setAttribute('aria-valuenow', pct);
    updMsg(t('upd.progress', { pct }), false);
  } else if (p.phase === 'apply') {
    bar.style.width = '100%'; updMsg(t('upd.applying'), false);
  } else if (p.phase === 'restart') {
    bar.style.width = '100%'; updMsg(t('upd.restarting'), false);
  }
}
$('btnInstallUpdate').addEventListener('click', async () => {
  const btn = $('btnInstallUpdate');
  btn.disabled = true; btn.textContent = t('set.downloading');
  $('btnCancelUpdate').hidden = false;
  $('updProgressBar').style.width = '0%';
  $('updProgress').hidden = false;
  let err;
  try { err = await api().InstallUpdate(); } catch (e) { err = String(e); }
  $('btnCancelUpdate').hidden = true;
  if (err) {
    // Baglanti ve uygulama oldugu gibi surer; kullanici yeniden deneyebilir.
    updMsg(err, true); $('updProgress').hidden = true;
    btn.disabled = false; btn.textContent = t('upd.retry');
  } else updMsg(t('upd.installed'), false);
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
  if (v.active) setPill(badge, 'ok', t('nt.on'));
  else if (!usable) setPill(badge, 'bad', t(v.need_elevation ? 'nt.noAccess' : 'nt.noService'));
  else setPill(badge, '', t('nt.off'));

  btn.textContent = t(v.active ? 'nt.btnOff' : 'nt.btnOn');
  btn.disabled = ntBusy || !usable || (!v.active && v.ok && !v.server_tun);
  btn.className = 'btn ' + (v.active ? '' : 'primary');

  $('ntDetails').hidden = !(v.active || (usable && v.ip));
  $('ntIP').textContent = v.ip || '—';
  $('ntPool').textContent = v.pool || '—';
  $('ntIface').textContent = v.interface || '—';
  $('ntDNS').textContent = !v.active ? '—'
    : v.dns_ok ? ('*.zorven.internal (' + (v.dns_method || 'ok') + ')')
    : (t('nt.dnsNotRegistered') + (v.dns_note ? ': ' + v.dns_note : ''));

  // arka plan hizmeti karti
  setPill($('bgBadge'), v.service_found ? 'ok' : '', t(v.service_found ? 'bg.installed' : 'bg.missing'));
  const inst = !!installInfo.installed;
  $('bgDesc').textContent = t(inst ? 'bg.descInstalled' : 'bg.desc');
  $('btnNtInstall').textContent = t(inst ? 'nt.repair' : 'nt.install');

  const peers = v.peers || [];
  $('peersTable').hidden = peers.length === 0;
  $('peersEmpty').hidden = peers.length !== 0;
  $('peersMeta').textContent = peers.length ? t('peers.count', { n: peers.length }) : '';
  $('peersBody').innerHTML = peers.map(p => {
    const label = !p.online ? t('nt.peerOffline') : p.path === 'direct' ? t('nt.peerDirect') : t('nt.peerRelay');
    const cls = p.online && p.path === 'direct' ? 'direct' : '';
    const rtt = p.online && p.path === 'direct' && p.rtt_ms ? p.rtt_ms + ' ms' : '—';
    return '<tr><td>' + esc(p.name) + '</td><td class="mono">' + esc(p.ip) + '</td><td><span class="tag ' + cls + '">' + esc(label) +
      '</span></td><td class="right num">' + esc(rtt) + '</td></tr>';
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
  // Kurulu kipte servis kurulumun parcasi: yalniz hizmet yokken onarim dugmesi.
  $('btnNtInstall').hidden = inst ? !!v.service_found : !(v.need_service && v.can_install);
  $('bgActions').hidden = $('btnNtInstall').hidden;
  $('btnNtElevate').hidden = !(v.need_elevation && v.can_elevate);
  $('ntActions').hidden = $('btnNtElevate').hidden;
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

function stopNetTun() {
  if (ntTimer) { clearInterval(ntTimer); ntTimer = null; }
  ntState = null; ntBusy = false;
  setPill($('ntBadge'), '', t('nt.off'));
  setPill($('bgBadge'), '', '—');
  ['ntDetails', 'ntErr', 'ntHint', 'ntActions', 'bgActions', 'peersTable'].forEach(id => { $(id).hidden = true; });
  $('peersEmpty').hidden = false; $('peersMeta').textContent = '';
  $('btnNtToggle').disabled = true; $('btnNtToggle').textContent = t('nt.btnOn'); $('btnNtToggle').className = 'btn primary';
}

// ---------------------------------------------------------------- PAYLASIM VE ERISIM (Zorven Net)
// CLI'deki `zorven share` / `zorven access` karsiligi. Islemler Go tarafinda (netshare.go)
// gomulu ajana ya da ayni makinedeki CLI ajanina/servise yaptirilir; burada yalniz form,
// liste ve yoklama var.
let nsState = null, nsTimer = null, nsBusy = false, nsAgain = false, nsLastJSON = '', nsPersistChoice = false;

const nsDate = (unix) => {
  try { return new Date(unix * 1000).toLocaleString(ZI18n.getLang(), { dateStyle: 'short', timeStyle: 'short' }); }
  catch { return new Date(unix * 1000).toISOString().slice(0, 16).replace('T', ' '); }
};
function nsPathLabel(p) {
  if (p === 'direct') return t('nt.peerDirect');
  if (p === 'relay') return t('nt.peerRelay');
  if (p === 'mixed') return t('ns.pathMixed');
  return '';
}
function nsPathHTML(p, rtt) {
  const l = nsPathLabel(p);
  if (!l) return '—';
  return `<span class="tag ${p === 'direct' ? 'direct' : ''}">${esc(l)}</span>` + (rtt ? ` <span class="subtle num">${esc(rtt)} ms</span>` : '');
}
function nsReasonText(r) {
  switch (r) {
    case 'preparing': return t('ns.reason.preparing');
    case 'nonet': return t('ns.reason.nonet');
    case 'denied': return t('ns.reason.denied');
    case 'old': return t('ns.reason.old');
    case 'error': return t('ns.reason.error');
    default: return t('ns.reason.connect');
  }
}
function nsPersistHint() {
  const forced = !!(nsState && nsState.persist_forced);
  $('nsPersistHint').textContent = forced ? t('ns.persistForced') : t('ns.tempHint');
  $('nsPersistHint').hidden = !forced && nsPersistChoice;
}

function nsRender(v, force) {
  nsState = v;
  const j = JSON.stringify(v) + '|' + (isLive(currentStatus) ? 1 : 0); // Baglan dugmesi baglanti durumuna da bagli
  if (!force && j === nsLastJSON) return;
  nsLastJSON = j;
  const off = !v.ok;

  // Kullanilamama nedeni
  $('nsStatus').hidden = !off;
  if (off) {
    let text = nsReasonText(v.reason);
    if (v.detail && (v.reason === 'error' || v.reason === 'denied')) text += ' (' + v.detail + ')';
    $('nsStatusText').textContent = text;
    const preparing = v.reason === 'preparing';
    setPill($('nsStatusPill'), preparing ? 'warn' : (v.reason === 'connect' ? '' : 'bad'), t(preparing ? 'ns.preparing' : 'ns.unavailable'));
    $('btnNsElevate').hidden = !(v.need_elevation && v.can_elevate);
    $('btnNsConnect').hidden = v.reason !== 'connect' || isLive(currentStatus);
    $('nsStatusActions').hidden = $('btnNsElevate').hidden && $('btnNsConnect').hidden;
  }
  $('btnNsCreate').disabled = off;
  $('btnNsBind').disabled = off;

  // Paylastiklarim
  const shares = v.shares || [];
  $('nsMineMeta').textContent = v.mode === 'service' ? t('ns.viaService') : (shares.length ? t('ns.count', { n: shares.length }) : '');
  const forced = !!v.persist_forced;
  $('nsPersist').disabled = forced;
  $('nsPersist').checked = forced ? true : nsPersistChoice;
  nsPersistHint();
  const chips = ['group:everyone'].concat((v.devices || []).map(d => 'device:' + d));
  $('nsToChips').innerHTML = chips.map(c => `<button type="button" class="chip" data-ns-to="${esc(c)}">${esc(c)}</button>`).join('');
  $('nsSharesTable').hidden = shares.length === 0;
  $('nsSharesEmpty').hidden = shares.length !== 0 || off;
  $('nsSharesBody').innerHTML = shares.map(s => {
    const who = s.to && s.to.length ? s.to.join(', ') : t('ns.peersN', { n: s.peers || 0 });
    const life = s.life === 'persistent' ? t('ns.persistent') : s.life === 'temporary' ? t('ns.temporary') : '';
    const lifeTag = life ? ` <span class="tag ns-life">${esc(life)}</span>` : '';
    const exp = s.expires_unix ? nsDate(s.expires_unix) : t('ns.noExpiry');
    return `<tr><td class="host">${esc(s.name)}${lifeTag}</td>` +
      `<td class="num">${tagHTML(String(s.proto || 'tcp').toUpperCase())} ${esc(s.port)}</td>` +
      `<td class="mono subtle" title="${esc(who)}">${esc(who)}</td>` +
      `<td class="subtle">${esc(exp)}</td>` +
      `<td>${nsPathHTML(s.path, 0)}</td>` +
      `<td class="right"><button class="icon-btn" type="button" data-ns-del="${esc(s.id)}" title="${esc(t('ns.delete'))}" aria-label="${esc(t('ns.deleteAria', { name: s.name }))}">${ico('trash')}</button></td></tr>`;
  }).join('');
  const warn = v.warn || [];
  $('nsWarn').hidden = warn.length === 0;
  $('nsWarn').textContent = warn.length ? t('ns.warnRestore', { list: warn.join('; ') }) : '';

  // Erisebildiklerim
  const acc = v.access || [];
  $('nsTheirsMeta').textContent = acc.length ? t('ns.count', { n: acc.length }) : '';
  $('nsAccessTable').hidden = acc.length === 0;
  $('nsAccessEmpty').hidden = acc.length !== 0 || off;
  $('nsAccessEmpty').textContent = v.can_list ? t('ns.theirsEmpty') : t('ns.theirsNoList');
  $('nsAccessBody').innerHTML = acc.map(s => {
    const bound = (s.bound || []).length ? `<div class="ns-bound">${s.bound.map(b => `<span class="tag direct">→ ${esc(b)}</span>`).join('')}</div>` : '';
    const owner = `<span class="pill${s.online ? ' ok' : ''}"><span class="dot"></span><span class="pill-t">${esc(s.owner)}</span></span>`;
    return `<tr><td class="host">${esc(s.name)}${bound}</td><td>${owner}</td>` +
      `<td class="num">${tagHTML(String(s.proto || 'tcp').toUpperCase())} ${esc(s.port)}</td>` +
      `<td>${nsPathHTML(s.online ? s.path : '', s.rtt_ms)}</td>` +
      `<td class="subtle">${s.expires_unix ? esc(nsDate(s.expires_unix)) : '—'}</td>` +
      `<td class="right"><button class="btn sm" type="button" data-ns-pick="${esc(s.name)}" data-ns-port="${esc(s.port)}" aria-label="${esc(t('ns.pickAria', { name: s.name }))}">${esc(t('ns.pick'))}</button></td></tr>`;
  }).join('');

  // Acik yerel erisimler
  const binds = v.binds || [];
  $('nsBindTable').hidden = binds.length === 0;
  $('nsBindEmpty').hidden = binds.length !== 0;
  $('nsBindBody').innerHTML = binds.map(b =>
    `<tr><td class="host">${esc(b.target)}</td>` +
    `<td class="mono">${esc(b.local)}${b.error ? `<div class="err ns-cell-err">${esc(b.error)}</div>` : ''}</td>` +
    `<td class="right"><span class="row" style="gap:6px;justify-content:flex-end">` +
    `<button class="icon-btn" type="button" data-copy="${esc(b.local)}" title="${esc(t('tunnels.copy'))}" aria-label="${esc(t('tunnels.copyAria', { url: b.local }))}">${ico('copy')}</button>` +
    `<button class="btn sm" type="button" data-ns-close="${esc(b.local)}">${esc(t('ns.close'))}</button></span></td></tr>`
  ).join('');
}

async function nsRefresh() {
  if (!api()?.NetShareState) return;
  if (nsBusy) { nsAgain = true; return; }
  nsBusy = true;
  try { nsRender(await api().NetShareState()); } catch { /* api hazir degil */ }
  finally { nsBusy = false; if (nsAgain) { nsAgain = false; nsRefresh(); } }
}
function nsStart() {
  if (nsTimer) return;
  nsTimer = setInterval(() => { if (curPage === 'shares' && !$('main').hidden) nsRefresh(); }, 3000);
}
function nsStop() {
  if (nsTimer) { clearInterval(nsTimer); nsTimer = null; }
  nsState = null; nsLastJSON = ''; nsBusy = false; nsAgain = false;
  ['nsCreateErr', 'nsBindErr', 'nsStatus', 'nsWarn'].forEach(id => { $(id).hidden = true; });
  ['nsPort', 'nsName', 'nsTo', 'nsExpires', 'nsTarget', 'nsLocal'].forEach(id => { $(id).value = ''; });
}
function nsShowErr(id, msg) { const el = $(id); el.textContent = msg || ''; el.hidden = !msg; }

async function nsCreate() {
  const raw = $('nsPort').value.trim();
  const port = /^\d{1,5}$/.test(raw) ? parseInt(raw, 10) : 0; // 0: Go tarafi yerel "gecersiz port" metnini doner
  nsShowErr('nsCreateErr', '');
  $('btnNsCreate').disabled = true;
  let r;
  try {
    r = await api().NetShareCreate({
      port, proto: $('nsProto').value, name: $('nsName').value, to: $('nsTo').value,
      expires: $('nsExpires').value, persist: $('nsPersist').checked,
    });
  } catch (e) { r = { error: String(e) }; }
  $('btnNsCreate').disabled = !(nsState && nsState.ok);
  if (!r.ok) { nsShowErr('nsCreateErr', r.error); return; }
  $('nsPort').value = ''; $('nsName').value = '';
  announce(t('ns.created', { name: (r.share && r.share.name) || '' }));
  await nsRefresh();
}
async function nsDelete(id) {
  nsShowErr('nsCreateErr', '');
  let r;
  try { r = await api().NetShareDelete(id); } catch (e) { r = { error: String(e) }; }
  if (!r.ok) { nsShowErr('nsCreateErr', r.error); return; }
  announce(t('ns.deleted', { name: (r.share && r.share.name) || '' }));
  await nsRefresh();
}
async function nsBind() {
  const target = $('nsTarget').value.trim();
  const raw = $('nsLocal').value.trim();
  const local = raw === '' ? 0 : (/^\d{1,5}$/.test(raw) ? parseInt(raw, 10) : -1); // -1: Go tarafi "gecersiz yerel port" doner
  nsShowErr('nsBindErr', '');
  $('btnNsBind').disabled = true;
  let r;
  try { r = await api().NetAccessOpen(target, local); } catch (e) { r = { error: String(e) }; }
  $('btnNsBind').disabled = !(nsState && nsState.ok);
  if (!r.ok) { nsShowErr('nsBindErr', r.error); return; }
  $('nsTarget').value = ''; $('nsLocal').value = ''; $('nsLocal').placeholder = '';
  announce(t('ns.bound', { target, local: r.local || '' }));
  await nsRefresh();
}
async function nsCloseBind(local) {
  nsShowErr('nsBindErr', '');
  let r;
  try { r = await api().NetAccessClose(local); } catch (e) { r = { error: String(e) }; }
  if (!r.ok) { nsShowErr('nsBindErr', r.error); return; }
  await nsRefresh();
}
function nsAddRecipient(x) {
  const cur = $('nsTo').value.split(/[\s,;]+/).filter(Boolean);
  if (!cur.includes(x)) cur.push(x);
  $('nsTo').value = cur.join(', ');
  $('nsTo').focus();
}

$('btnNsCreate').addEventListener('click', nsCreate);
$('btnNsBind').addEventListener('click', nsBind);
['nsPort', 'nsName', 'nsTo', 'nsExpires'].forEach(id => $(id).addEventListener('keydown', (e) => { if (e.key === 'Enter') nsCreate(); }));
['nsTarget', 'nsLocal'].forEach(id => $(id).addEventListener('keydown', (e) => { if (e.key === 'Enter') nsBind(); }));
$('nsPersist').addEventListener('change', (e) => { nsPersistChoice = e.target.checked; nsPersistHint(); });
$('btnNsConnect').addEventListener('click', () => $('btnConn').click());
$('btnNsElevate').addEventListener('click', async () => {
  const r = await api().NetTunRelaunchElevated();
  if (r && r.error) { $('nsStatusText').textContent = r.error; }
});
$('p-shares').addEventListener('click', (ev) => {
  const b = ev.target.closest('button');
  if (!b) return;
  if (b.dataset.nsTo) nsAddRecipient(b.dataset.nsTo);
  else if (b.dataset.nsPick) {
    $('nsTarget').value = b.dataset.nsPick;
    $('nsLocal').value = ''; $('nsLocal').placeholder = b.dataset.nsPort || '';
    $('nsLocal').focus();
  }
  else if (b.dataset.nsDel) nsDelete(b.dataset.nsDel);
  else if (b.dataset.nsClose) nsCloseBind(b.dataset.nsClose);
});

// ---------------------------------------------------------------- DIL
// applyLanguage: arayuz dilini degistirir ve dinamik icerigi yeniden cizer
// (yeniden baslatma gerekmez). Go tarafi (SetLanguage) tepsiyi ve ajan
// mesajlarini kendisi gunceller.
function applyLanguage(lang) {
  ZI18n.setLang(lang);
  renderLiveBtn();
  $('pageTitle').textContent = t(PAGES[curPage].key);
  lpRender();
  renderAll();
  renderBanner();
  if (ntState) { ntRender(ntState); ntRefresh(); } else stopNetTunLabels();
  if (updInfo && updInfo.available) updMsg(t('upd.available', { latest: updInfo.latest, current: updInfo.current }), false);
  else updMsg('', false);
  renderDeploy();
  if (nsState) nsRender(nsState, true);
}
function stopNetTunLabels() { if (!ntTimer) setPill($('ntBadge'), '', t('nt.off')); }
async function loadLanguage() {
  try {
    const li = await api().GetLanguage();
    $('setLanguage').value = li.setting || '';
    if (li.effective !== ZI18n.getLang()) applyLanguage(li.effective);
  } catch { /* api hazir degil */ }
}
$('setLanguage').addEventListener('change', async (e) => {
  const err = await api().SetLanguage(e.target.value);
  if (err) { showSetErr(err); return; }
  applyLanguage((await api().GetLanguage()).effective);
  flashSaved();
});

// ---------------------------------------------------------------- BAŞLANGIÇ
window.addEventListener('DOMContentLoaded', async () => {
  if (window.ZTheme) window.ZTheme.apply();
  // Go'dan etkin dil gelene kadar tarayici dili (ilk cizimde bos/yanlis dil gorunmesin).
  ZI18n.setLang(ZI18n.guess());
  renderLiveBtn(); renderAll(); stopNetTun(); showPage('overview');
  setInterval(() => { if (!$('main').hidden) renderSide(currentStatus || { state: 'stopped' }, !!svState.found && !isLive(currentStatus) && !svState.takeover); }, 30000);
  for (let i = 0; i < 50 && !api(); i++) await new Promise(r => setTimeout(r, 100));
  if (!api()) return;
  await loadLanguage();
  loadVersion();

  const authed = await api().IsAuthed();
  if (authed) enterMain(); else showAuth();

  const on = (name, fn) => window.runtime?.EventsOn(name, fn);
  on('status', renderStatus);
  on('telemetry', renderTelemetry);
  on('traffic', (d) => { traffic = d; renderAll(); });
  on('request_log', (r) => pushLog(r));
  on('service:state', (d) => {
    const prev = svState;
    svState = d || { found: false };
    renderAll();
    // Kurulu kipte izin anahtarlarinin kaynagi servistir: izinler degistiyse ayarlari tazele.
    if (installInfo.installed && svState.found && (prev.terminal_cap !== svState.terminal_cap ||
        prev.screen_cap !== svState.screen_cap || prev.deploy_on !== svState.deploy_on)) loadSettings();
  });
  on('update:available', (d) => { if (d && d.available) showUpdateAvailable(d); else if (d && !d.error) { updInfo = d; $('btnInstallUpdate').hidden = true; $('btnNotes').hidden = true; removeBanner('upd'); } });
  on('update:progress', onUpdateProgress);
  on('update:focus', () => gotoUpdate());
  on('device:approved', () => enterMain());
  on('device:error', (msg) => {
    $('browserErr').textContent = msg || t('auth.loginFailed'); $('browserErr').hidden = false;
    $('browserWaiting').hidden = true; $('btnBrowserLogin').hidden = false; $('btnBrowserLogin').disabled = false;
  });
  on('auth:changed', async () => {
    (await api().IsAuthed()) ? enterMain() : leaveMain();
  });
});

// --- Cercevesiz pencere dugmeleri -------------------------------------------
(function () {
  const rt = () => window.runtime || {};
  document.addEventListener('click', (e) => {
    const b = e.target.closest('[data-win]');
    if (!b) return;
    const r = rt();
    try {
      if (b.dataset.win === 'min' && r.WindowMinimise) r.WindowMinimise();
      else if (b.dataset.win === 'max' && r.WindowToggleMaximise) { r.WindowToggleMaximise(); setTimeout(syncMax, 150); }
      // CloseMain: "tepsiye gizle" aciksa pencere gizlenir, degilse uygulama kapanir.
      else if (b.dataset.win === 'close') { if (api() && api().CloseMain) api().CloseMain(); else if (r.Quit) r.Quit(); }
    } catch (err) { /* tarayici onizlemesi */ }
  });
  async function syncMax() {
    let max = false;
    try { if (rt().WindowIsMaximised) max = await rt().WindowIsMaximised(); } catch (e) { /* yok say */ }
    document.querySelectorAll('.win-max-use').forEach((u) => u.setAttribute('href', max ? '#i-win-restore' : '#i-win-max'));
    document.querySelectorAll('[data-win="max"]').forEach((b) => {
      const k = max ? 'win.restore' : 'win.max';
      b.title = t(k); b.setAttribute('aria-label', t(k));
    });
  }
  window.addEventListener('resize', () => { clearTimeout(syncMax._t); syncMax._t = setTimeout(syncMax, 120); });
  syncMax();
})();

// --- Tepsi paneli (flyout) ---------------------------------------------------
function renderFlyout() {
  const authed = !$('main').hidden || flyAuthed;
  $('flyAuth').hidden = authed; $('flyMain').hidden = !authed;
  const pill = $('flyPill');
  if (!authed) { setPill(pill, '', t('state.stopped')); return; }
  const s = currentStatus || { state: 'stopped' };
  const live = isLive(s), svcMode = lastSvcMode;
  let cls, text;
  if (svcMode) { cls = svState.connected ? 'ok' : 'warn'; text = t(svState.connected ? 'svc.badge' : 'svc.badgeWaiting'); }
  else { const meta = STATES[s.state] ?? STATES.stopped; cls = meta.cls; text = t(meta.key); }
  setPill(pill, cls, text);
  $('flyState').textContent = text;
  const srv = svcMode ? (svState.server || cfgCache.server_addr) : cfgCache.server_addr;
  const up = s.state === 'connected' ? fmtUptime(s.since) : '';
  $('flySub').textContent = [srv, up].filter(Boolean).join(' · ');
  $('flyMain').querySelector('.fly-conn').classList.toggle('on', svcMode ? !!svState.connected : s.state === 'connected');
  const b = $('flyConn');
  b.hidden = svcMode; b.disabled = connBusy;
  b.className = 'btn ' + (live ? 'danger' : 'primary');
  $('flyConnText').textContent = connBusy ? t('state.connecting') : (live ? t('conn.disconnect') : t('conn.connect'));
  $('flyReq').textContent = $('tReq').textContent;
  $('flyErr').textContent = $('tErr').textContent;
  $('flyP95').textContent = $('tP95').textContent;
  const list = lastEntries.slice(0, 6);
  $('flyTunnels').innerHTML = list.length
    ? list.map(e => `<div class="item">${tagHTML(e.proto)}<span class="host grow mono" title="${esc(e.label)}">${esc(e.label)}</span>${tunnelActionsHTML(e)}</div>`).join('')
    : `<div class="empty" style="padding:14px">${svcMode ? esc(t('svc.noTunnels')) : esc(t('fly.noTunnels'))}</div>`;
  $('flyShareSec').hidden = svcMode;
  const rp = (cfgCache.recent_ports || []).filter(p => Number.isInteger(p)).slice(0, 5);
  $('flyRecent').innerHTML = rp.map(p => `<button type="button" class="chip" data-port="${esc(p)}">${esc(p)}</button>`).join('');
  $('flyShareActive').hidden = !shareTarget || !live;
  $('flyShareTarget').textContent = shareTarget ? '→ ' + shareTarget : '';
  $('flyShareGo').disabled = connBusy;
}
let flyAuthed = false;
async function flyShare(port) {
  $('flyErrMsg').hidden = true;
  if (!/^\d{1,5}$/.test(String(port))) { $('flyErrMsg').textContent = t('fly.badPort'); $('flyErrMsg').hidden = false; return; }
  setConnBusy();
  let err;
  try { err = await api().SharePort(parseInt(port, 10), false); } catch (e) { err = String(e); }
  if (err) { $('flyErrMsg').textContent = err; $('flyErrMsg').hidden = false; clearConnBusy(); }
  try { cfgCache = await api().GetConfig(); } catch { /* yoksay */ }
  await refreshShare(); renderAll();
}
$('flyShareGo').addEventListener('click', () => flyShare($('flyPort').value.trim()));
$('flyPort').addEventListener('keydown', (e) => { if (e.key === 'Enter') flyShare($('flyPort').value.trim()); });
$('flyRecent').addEventListener('click', (e) => { const b = e.target.closest('button[data-port]'); if (b) flyShare(b.dataset.port); });
$('flyShareStop').addEventListener('click', async () => {
  setConnBusy();
  try { await api().StopSharing(); } catch { /* yoksay */ }
  await refreshShare(); renderAll();
});
$('flyConn').addEventListener('click', () => $('btnConn').click());
$('flyOpenApp').addEventListener('click', () => api().CloseFlyout(true));
$('flyFull').addEventListener('click', () => api().CloseFlyout(true));
$('flyLogin').addEventListener('click', () => api().CloseFlyout(true));
$('flyQuit').addEventListener('click', () => api().QuitApp());
window.addEventListener('blur', () => {
  // Acilistan hemen sonra gelen odak kaybini yok say (pencere one gelirken olusabilir).
  if (flyOn && Date.now() - flyOpenedAt > 500) api().FlyoutBlur();
});
document.addEventListener('keydown', (e) => { if (flyOn && e.key === 'Escape') api().CloseFlyout(false); });
window.addEventListener('DOMContentLoaded', () => {
  const on = (name, fn) => window.runtime?.EventsOn(name, fn);
  on('flyout:open', async () => {
    flyOn = true; flyOpenedAt = Date.now();
    try { flyAuthed = await api().IsAuthed(); } catch { flyAuthed = false; }
    document.body.classList.add('flyout'); $('flyout').hidden = false;
    renderFlyout();
  });
  on('flyout:close', () => {
    flyOn = false; document.body.classList.remove('flyout'); $('flyout').hidden = true;
  });
});
