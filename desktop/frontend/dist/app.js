// ==============================================================================
// Zorven Desktop — v3: sadece Tüneller + Loglar (ayarlar/tünel config web'de)
// ==============================================================================
const api = () => window.go?.main?.App;
const $ = (id) => document.getElementById(id);

let currentStatus = null;
let logWin = null;         // ayrı log penceresi referansı
let totalReq = 0;

const STATES = {
  connected:    { cls: 'badge-connected',  label: 'Bağlı' },
  connecting:   { cls: 'badge-connecting', label: 'Bağlanıyor…' },
  reconnecting: { cls: 'badge-connecting', label: 'Yeniden Bağlanıyor…' },
  stopped:      { cls: 'badge-stopped',    label: 'Bağlı Değil' },
  fatal:        { cls: 'badge-stopped',    label: 'Bağlanamadı' },
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
  badge.textContent = meta.label;

  // Bağlan / Bağlantıyı Kes butonu (duruma göre)
  const live = s.state === 'connected' || s.state === 'connecting' || s.state === 'reconnecting';
  const btn = $('btnConn');
  if (btn) {
    btn.textContent = live ? 'Bağlantıyı Kes' : 'Bağlan';
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
    if (ta > 0) parts.push('<span class="as-item as-cli"><svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><path d="m4 17 6-6-6-6"/><path d="M12 19h8"/></svg>Uzak CLI etkin' + (ta > 1 ? ' (' + ta + ')' : '') + '</span>');
    if (sa > 0) parts.push('<span class="as-item as-screen"><svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><rect width="18" height="12" x="3" y="4" rx="2"/><path d="M7 20h10M12 16v4"/></svg>Ekran paylaşımı etkin' + (sa > 1 ? ' (' + sa + ')' : '') + '</span>');
    asEl.innerHTML = parts.join('');
    asEl.hidden = parts.length === 0;
  }

  const tunnels = s.tunnels || [];
  const list = $('tunnelList');
  if (tunnels.length && tunnels[0].hostnames) {
    const rows = [];
    for (const t of tunnels) {
      for (const h of (t.hostnames || [])) {
        rows.push(`<div class="tunnel-row">
          <div style="min-width:0">
            <a class="tunnel-url" href="#" data-url="https://${h}">https://${h}</a>
            <div class="tunnel-target">→ ${t.target || 'yerel servis'}</div>
          </div>
          <button class="btn-ghost" data-open="https://${h}">Aç</button>
        </div>`);
      }
    }
    list.innerHTML = rows.join('') || `<div class="empty">Açık tünel yok. Tünelleri web panelinden oluşturun.</div>`;
    list.querySelectorAll('[data-open]').forEach(b => b.addEventListener('click', () => api().OpenBrowser(b.dataset.open)));
  } else {
    list.innerHTML = `<div class="empty">Açık tünel yok. Tünelleri <b>web panelinden</b> (zorven.app) oluşturun.</div>`;
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
  $('lfCount').textContent = logCount + ' istek';
}
function pushLog(r){
  if (!r) return;
  logCount++; logRows.unshift(r); if (logRows.length > 3000) logRows.pop();
  if (!$('logPanel').hidden){ if (logLive) lpRender(); else $('lfCount').textContent = logCount + ' istek'; }
}
function openLogPanel(){ $('logPanel').hidden = false; try { api().ResizeWindow(1120, 760); } catch {} lpRender(); }
function closeLogPanel(){ $('logPanel').hidden = true; try { api().ResizeWindow(460, 820); } catch {} }
$('lfClose').addEventListener('click', closeLogPanel);
$('lfLive').addEventListener('click', () => { logLive = !logLive; $('lfLive').classList.toggle('paused', !logLive); $('lfLive').textContent = logLive ? '● canlı' : '● duraklatıldı'; if (logLive) lpRender(); });
$('lfClear').addEventListener('click', () => { logRows = []; logCount = 0; lpRender(); });
['lfMethod','lfStatus','lfQ','lfDur'].forEach(id => $(id).addEventListener('input', lpRender));
$('lfExport').addEventListener('click', () => {
  const NL = String.fromCharCode(10);
  const vis = logRows.filter(lpMatch);
  const head = ['ts','method','path','status','duration_ms'].join(',');
  const lines = vis.map(r => [r.ts, r.method, '"'+(r.path||'').replace(/"/g,'""')+'"', r.status, r.duration_ms].join(','));
  const csv = head + NL + lines.join(NL);
  const a = document.createElement('a'); a.href = URL.createObjectURL(new Blob([csv],{type:'text/csv'})); a.download = 'zorven-loglar.csv'; a.click();
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
  btn.disabled = true; btn.textContent = 'Denetleniyor…';
  $('btnInstallUpdate').hidden = true;
  try {
    const u = await api().CheckForUpdate();
    if (u.error) updMsg(u.error, true);
    else if (u.available) {
      updMsg('Yeni sürüm hazır: v' + u.latest + ' (şu an v' + u.current + ')', false);
      $('btnInstallUpdate').hidden = false;
    } else updMsg('En güncel sürümü kullanıyorsunuz (v' + u.current + ').', false);
  } catch { updMsg('Güncelleme denetlenemedi.', true); }
  btn.disabled = false; btn.textContent = 'Güncellemeleri denetle';
});
$('btnInstallUpdate').addEventListener('click', async () => {
  const btn = $('btnInstallUpdate');
  btn.disabled = true; btn.textContent = 'İndiriliyor…';
  const err = await api().InstallUpdate();
  if (err) { updMsg(err, true); btn.disabled = false; btn.textContent = 'Güncelle ve yeniden başlat'; }
  else updMsg('Güncelleme kuruldu, uygulama yeniden başlatılıyor…', false);
});
$('setAutostart').addEventListener('change', async (e) => {
  const err = await api().SetAutostart(e.target.checked);
  if (err) e.target.checked = !e.target.checked; else flashSaved();
});

// ---------------------------------------------------------------- BAŞLANGIÇ
window.addEventListener('DOMContentLoaded', async () => {
  for (let i = 0; i < 50 && !api(); i++) await new Promise(r => setTimeout(r, 100));
  if (!api()) return;
  loadVersion();

  const authed = await api().IsAuthed();
  if (authed) { showMain(); loadSettings(); renderStatus(await api().GetStatus()); }
  else showAuth();

  window.runtime?.EventsOn('status', renderStatus);
  window.runtime?.EventsOn('telemetry', renderTelemetry);
  window.runtime?.EventsOn('request_log', (r) => pushLog(r));
  window.runtime?.EventsOn('device:approved', () => { showMain(); loadSettings(); });
  window.runtime?.EventsOn('device:error', (msg) => {
    $('browserErr').textContent = msg || 'Giriş başarısız.'; $('browserErr').hidden = false;
    $('browserWaiting').hidden = true; $('btnBrowserLogin').hidden = false; $('btnBrowserLogin').disabled = false;
  });
  window.runtime?.EventsOn('auth:changed', async () => {
    (await api().IsAuthed()) ? showMain() : showAuth();
  });
});
