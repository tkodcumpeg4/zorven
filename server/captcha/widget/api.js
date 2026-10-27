/*! Zorven CAPTCHA widget v1 - https://zorven.app/captcha/v1/api.js
 * Cerezsiz (varsayilan), izleme yok, dis kaynak yok. Shadow DOM icinde calisir.
 * Kullanim:
 *   <script src="https://zorven.app/captcha/v1/api.js" async defer></script>
 *   <div class="zorven-captcha" data-sitekey="zcs_..." data-callback="onOk"></div>
 * JS API: zorvenCaptcha.render(el, {sitekey, callback, 'expired-callback', 'error-callback',
 *   theme, lang, mode}) -> id; getResponse(id); reset(id); remove(id).
 */
(function () {
  'use strict';
  if (window.zorvenCaptcha && window.zorvenCaptcha.__zv) return;

  var VERSION = '1';
  var FIELD = 'zorven-captcha-response';
  var PASS_COOKIE = 'zcap_pass';
  var SOLVE_TIMEOUT_MS = 90000;

  // ---- betigin kaynagi (API tabani) ----
  var script = document.currentScript;
  if (!script) {
    var all = document.getElementsByTagName('script');
    for (var si = 0; si < all.length; si++) {
      if (/\/captcha\/v1\/api\.js/.test(all[si].src || '')) { script = all[si]; break; }
    }
  }
  var BASE = location.origin;
  var params = { render: '', onload: '' };
  try {
    var su = new URL(script.src, location.href);
    BASE = su.origin;
    params.render = su.searchParams.get('render') || '';
    params.onload = su.searchParams.get('onload') || '';
  } catch (e) { /* varsayilanlar */ }

  // ---- metinler ----
  var I18N = {
    tr: { idle: 'Ben robot değilim', verifying: 'Doğrulanıyor…', success: 'Doğrulandı', error: 'Doğrulama başarısız', expired: 'Süre doldu', retry: 'Tekrar dene', label: 'Zorven CAPTCHA' },
    en: { idle: 'I am not a robot', verifying: 'Verifying…', success: 'Verified', error: 'Verification failed', expired: 'Expired', retry: 'Try again', label: 'Zorven CAPTCHA' },
    ja: { idle: '私はロボットではありません', verifying: '確認中…', success: '確認済み', error: '確認に失敗しました', expired: '有効期限切れ', retry: '再試行', label: 'Zorven CAPTCHA' }
  };
  function pickLang(l) {
    l = String(l || 'auto').toLowerCase();
    if (l === 'auto') {
      l = String((navigator.languages && navigator.languages[0]) || navigator.language || 'en').toLowerCase();
    }
    l = l.slice(0, 2);
    return I18N[l] ? l : 'en';
  }

  // ---- tek renk SVG ikonlar (lucide tarzi, currentColor) ----
  function svg(inner, cls) {
    return '<svg class="' + (cls || '') + '" viewBox="0 0 24 24" width="24" height="24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">' + inner + '</svg>';
  }
  var ICON = {
    idle: svg('<rect x="3" y="3" width="18" height="18" rx="4"/>'),
    spin: svg('<path d="M21 12a9 9 0 1 1-6.2-8.55"/>', 'spin'),
    ok: svg('<circle cx="12" cy="12" r="10"/><path d="m8 12.5 2.8 2.8L16 9.5"/>'),
    err: svg('<circle cx="12" cy="12" r="10"/><path d="M12 7v6"/><path d="M12 16.5v.01"/>'),
    shield: svg('<path d="M20 13c0 5-3.5 7.5-7.66 8.95a1 1 0 0 1-.67-.01C7.5 20.5 4 18 4 13V6a1 1 0 0 1 1-1c2 0 4.5-1.2 6.24-2.72a1.17 1.17 0 0 1 1.52 0C14.51 3.81 17 5 19 5a1 1 0 0 1 1 1z"/>', 'shield')
  };

  var CSS = ':host{display:inline-block;max-width:100%;line-height:normal}' +
    '.box{--bg:#fafafa;--fg:#1f2328;--mut:#6b7280;--bd:#d0d7de;--ok:#1a7f37;--er:#cf222e;--ac:#2563eb;' +
    'box-sizing:border-box;width:300px;max-width:100%;height:65px;display:flex;align-items:center;gap:12px;padding:0 14px;' +
    'background:var(--bg);color:var(--fg);border:1px solid var(--bd);border-radius:6px;' +
    'font:14px/1.3 system-ui,-apple-system,"Segoe UI",Roboto,"Noto Sans JP","Noto Sans",sans-serif}' +
    '.box[data-t=dark]{--bg:#232326;--fg:#e6e6e6;--mut:#9ca3af;--bd:#3f3f46;--ok:#3fb950;--er:#f85149;--ac:#60a5fa}' +
    '.ind{flex:none;width:24px;height:24px;display:flex;color:var(--mut)}' +
    '.ind svg{width:24px;height:24px}' +
    '.box[data-s=success] .ind{color:var(--ok)}.box[data-s=error] .ind,.box[data-s=expired] .ind{color:var(--er)}' +
    '.box[data-s=verifying] .ind{color:var(--ac)}' +
    '.spin{animation:zv-spin .9s linear infinite}@keyframes zv-spin{to{transform:rotate(360deg)}}' +
    '@media (prefers-reduced-motion:reduce){.spin{animation-duration:2.4s}}' +
    '.msg{flex:1;min-width:0;display:flex;flex-direction:column;gap:2px;overflow:hidden}' +
    '.txt{white-space:nowrap;overflow:hidden;text-overflow:ellipsis}' +
    '.retry{all:unset;cursor:pointer;color:var(--ac);font-size:12px;text-decoration:underline}' +
    '.retry:focus-visible,.click:focus-visible{outline:2px solid var(--ac);outline-offset:2px;border-radius:3px}' +
    '.retry[hidden]{display:none}' +
    '.click{all:unset;cursor:pointer;display:flex;color:inherit}' +
    '.brand{flex:none;display:flex;flex-direction:column;align-items:center;gap:1px;color:var(--mut);font-size:10px;letter-spacing:.02em}' +
    '.shield{width:16px;height:16px}';

  // ---- PoW cozucu (SHA-256(nonce || ondalik sayac), basta >= bits sifir bit) ----
  // Saf JS, tek blok (nonce+sayac <= 55 bayt). Worker'a metin olarak da gonderilir.
  function solverFactory() {
    var K = new Int32Array([
      0x428a2f98, 0x71374491, 0xb5c0fbcf, 0xe9b5dba5, 0x3956c25b, 0x59f111f1, 0x923f82a4, 0xab1c5ed5,
      0xd807aa98, 0x12835b01, 0x243185be, 0x550c7dc3, 0x72be5d74, 0x80deb1fe, 0x9bdc06a7, 0xc19bf174,
      0xe49b69c1, 0xefbe4786, 0x0fc19dc6, 0x240ca1cc, 0x2de92c6f, 0x4a7484aa, 0x5cb0a9dc, 0x76f988da,
      0x983e5152, 0xa831c66d, 0xb00327c8, 0xbf597fc7, 0xc6e00bf3, 0xd5a79147, 0x06ca6351, 0x14292967,
      0x27b70a85, 0x2e1b2138, 0x4d2c6dfc, 0x53380d13, 0x650a7354, 0x766a0abb, 0x81c2c92e, 0x92722c85,
      0xa2bfe8a1, 0xa81a664b, 0xc24b8b70, 0xc76c51a3, 0xd192e819, 0xd6990624, 0xf40e3585, 0x106aa070,
      0x19a4c116, 0x1e376c08, 0x2748774c, 0x34b0bcb5, 0x391c0cb3, 0x4ed8aa4a, 0x5b9cca4f, 0x682e6ff3,
      0x748f82ee, 0x78a5636f, 0x84c87814, 0x8cc70208, 0x90befffa, 0xa4506ceb, 0xbef9a3f7, 0xc67178f2
    ]);
    var w = new Int32Array(64);
    // find(nonce, bits, from, count): [from, from+count) araliginda cozum sayaci ya da -1
    function find(nonce, bits, from, count) {
      var L = nonce.length;
      if (L > 35) return -2; // tek blok sinirini asar
      var blk = new Uint8Array(64);
      var i;
      for (i = 0; i < L; i++) blk[i] = nonce.charCodeAt(i) & 0x7f;
      var lastLen = -1;
      var digits = new Uint8Array(20);
      for (var c = from; c < from + count; c++) {
        // ondalik rakamlar
        var n = c, nd = 0;
        if (n === 0) { digits[nd++] = 48; }
        else { while (n > 0) { digits[nd++] = 48 + (n % 10); n = Math.floor(n / 10); } }
        var len = L + nd;
        if (len !== lastLen) {
          for (i = L; i < 64; i++) blk[i] = 0;
          blk[len] = 0x80;
          blk[62] = ((len * 8) >> 8) & 255;
          blk[63] = (len * 8) & 255;
          lastLen = len;
        }
        for (i = 0; i < nd; i++) blk[L + i] = digits[nd - 1 - i];
        for (i = 0; i < 16; i++) {
          w[i] = (blk[4 * i] << 24) | (blk[4 * i + 1] << 16) | (blk[4 * i + 2] << 8) | blk[4 * i + 3];
        }
        for (i = 16; i < 64; i++) {
          var x = w[i - 15], y = w[i - 2];
          var s0 = ((x >>> 7) | (x << 25)) ^ ((x >>> 18) | (x << 14)) ^ (x >>> 3);
          var s1 = ((y >>> 17) | (y << 15)) ^ ((y >>> 19) | (y << 13)) ^ (y >>> 10);
          w[i] = (w[i - 16] + s0 + w[i - 7] + s1) | 0;
        }
        var a = 0x6a09e667 | 0, b = 0xbb67ae85 | 0, cc = 0x3c6ef372 | 0, d = 0xa54ff53a | 0;
        var e = 0x510e527f | 0, f = 0x9b05688c | 0, g = 0x1f83d9ab | 0, h = 0x5be0cd19 | 0;
        for (i = 0; i < 64; i++) {
          var S1 = ((e >>> 6) | (e << 26)) ^ ((e >>> 11) | (e << 21)) ^ ((e >>> 25) | (e << 7));
          var ch = (e & f) ^ (~e & g);
          var t1 = (h + S1 + ch + K[i] + w[i]) | 0;
          var S0 = ((a >>> 2) | (a << 30)) ^ ((a >>> 13) | (a << 19)) ^ ((a >>> 22) | (a << 10));
          var mj = (a & b) ^ (a & cc) ^ (b & cc);
          var t2 = (S0 + mj) | 0;
          h = g; g = f; f = e; e = (d + t1) | 0; d = cc; cc = b; b = a; a = (t1 + t2) | 0;
        }
        var h0 = (0x6a09e667 + a) | 0;
        if (Math.clz32(h0) >= bits) return c;
      }
      return -1;
    }
    return { find: find };
  }

  var WORKER_SRC = '(' + (function (factory) {
    var S = factory();
    self.onmessage = function (ev) {
      var d = ev.data, c = 0, STEP = 200000;
      for (;;) {
        var r = S.find(d.nonce, d.bits, c, STEP);
        if (r !== -1) { self.postMessage({ c: r }); return; }
        c += STEP;
      }
    };
  }).toString() + ')(' + solverFactory.toString() + ')';

  // ---- etkilesim/ortam sinyalleri (yalniz sayac; kisisel veri yok) ----
  var inter = { mv: 0, kd: 0, tc: 0, fc: 0, sc: 0 };
  function bump(k) { return function () { if (inter[k] < 1000) inter[k]++; }; }
  try {
    var po = { passive: true, capture: true };
    window.addEventListener('pointermove', bump('mv'), po);
    window.addEventListener('mousemove', bump('mv'), po);
    window.addEventListener('keydown', bump('kd'), po);
    window.addEventListener('touchstart', bump('tc'), po);
    window.addEventListener('focus', bump('fc'), po);
    document.addEventListener('focusin', bump('fc'), po);
    window.addEventListener('scroll', bump('sc'), po);
  } catch (e) { /* eski tarayici */ }

  function envSignals(solveMs) {
    var ua = navigator.userAgent || '';
    var hl = 0;
    if (/HeadlessChrome|Headless/i.test(ua)) hl++;
    if (/Chrome\//.test(ua) && !/Edg|OPR|Firefox|SamsungBrowser/.test(ua) && !window.chrome) hl++;
    if (window.outerWidth === 0 && window.outerHeight === 0) hl++;
    if (navigator.languages && navigator.languages.length === 0) hl++;
    var ms = 0;
    if (typeof window.fetch !== 'function') ms++;
    if (typeof window.Promise !== 'function') ms++;
    if (!(window.crypto && window.crypto.getRandomValues)) ms++;
    if (typeof window.URL !== 'function') ms++;
    if (typeof window.Blob !== 'function') ms++;
    return {
      wd: navigator.webdriver === true, hl: hl, ms: ms,
      mv: inter.mv, kd: inter.kd, tc: inter.tc, fc: inter.fc, sc: inter.sc,
      t: Math.max(0, Math.round(solveMs || 0))
    };
  }

  // ---- ag ----
  function post(path, body) {
    return fetch(BASE + path, {
      method: 'POST', mode: 'cors', credentials: 'omit', cache: 'no-store',
      headers: { 'Content-Type': 'text/plain;charset=UTF-8' },
      body: JSON.stringify(body)
    }).then(function (res) {
      return res.json().then(function (j) { return { ok: res.ok, status: res.status, j: j || {} }; },
        function () { return { ok: false, status: res.status, j: {} }; });
    });
  }
  function errCode(r) { return (r && r.j && r.j.error && r.j.error.code) || ('http_' + (r ? r.status : 0)); }

  // ---- gecis cerezi (yalniz sitede pass_cookie aciksa sunucu `pass` doner) ----
  function readPass() {
    try {
      var m = document.cookie.match(new RegExp('(?:^|;\\s*)' + PASS_COOKIE + '=([^;]+)'));
      return m ? m[1] : '';
    } catch (e) { return ''; }
  }
  function writePass(v, ttlSec) {
    try {
      document.cookie = PASS_COOKIE + '=' + v + '; Max-Age=' + ttlSec + '; Path=/; SameSite=Lax' +
        (location.protocol === 'https:' ? '; Secure' : '');
    } catch (e) { /* cerez kapali */ }
  }
  function clearPass() {
    try {
      document.cookie = PASS_COOKIE + '=; Max-Age=0; Path=/; SameSite=Lax' + (location.protocol === 'https:' ? '; Secure' : '');
    } catch (e) { /* yok say */ }
  }

  // ---- widget ----
  var widgets = {};
  var seq = 0;
  var mqDark = window.matchMedia ? window.matchMedia('(prefers-color-scheme: dark)') : null;

  function resolveFn(f) {
    if (typeof f === 'function') return f;
    if (typeof f === 'string' && f && typeof window[f] === 'function') return window[f];
    return null;
  }

  function Widget(el, opts) {
    this.el = el;
    this.id = 'zcw-' + (++seq);
    this.opts = opts;
    this.state = 'idle';
    this.response = '';
    this.run = 0;
    this.timers = [];
    this.worker = null;
    this.lang = pickLang(opts.lang);
    this.t = I18N[this.lang];
    this.build();
    var self = this;
    this.onTheme = function () { self.applyTheme(); };
    if (mqDark && mqDark.addEventListener) mqDark.addEventListener('change', this.onTheme);
    if (opts.mode === 'click') this.setState('idle'); else this.start();
  }

  Widget.prototype.build = function () {
    var el = this.el;
    var root = el.shadowRoot || (el.attachShadow ? el.attachShadow({ mode: 'open' }) : null);
    if (!root) { root = el; }
    this.root = root;
    root.innerHTML = '<style>' + CSS + '</style>' +
      '<div class="box" role="group" aria-label="' + this.t.label + '" data-s="idle">' +
      '<div class="ind"></div>' +
      '<div class="msg"><span class="txt" role="status" aria-live="polite"></span>' +
      '<button type="button" class="retry" hidden></button></div>' +
      '<div class="brand">' + ICON.shield + '<span>Zorven</span></div></div>';
    this.box = root.querySelector('.box');
    this.ind = root.querySelector('.ind');
    this.txt = root.querySelector('.txt');
    this.retryBtn = root.querySelector('.retry');
    var self = this;
    this.retryBtn.addEventListener('click', function () { self.reset(); });
    // Gizli alan, formun gonderecegi ISIK DOM'unda (shadow disinda).
    var inp = el.querySelector('input[name="' + FIELD + '"]');
    if (!inp) {
      inp = document.createElement('input');
      inp.type = 'hidden';
      inp.name = FIELD;
      el.appendChild(inp);
    }
    this.input = inp;
    this.applyTheme();
  };

  Widget.prototype.applyTheme = function () {
    var th = String(this.opts.theme || 'auto').toLowerCase();
    if (th !== 'light' && th !== 'dark') th = (mqDark && mqDark.matches) ? 'dark' : 'light';
    this.box.setAttribute('data-t', th);
  };

  Widget.prototype.setState = function (s, detail) {
    this.state = s;
    var t = this.t, box = this.box;
    box.setAttribute('data-s', s);
    var icon = ICON.idle, text = t.idle, showRetry = false;
    if (s === 'verifying') { icon = ICON.spin; text = t.verifying; }
    else if (s === 'success') { icon = ICON.ok; text = t.success; }
    else if (s === 'error') { icon = ICON.err; text = t.error; showRetry = true; }
    else if (s === 'expired') { icon = ICON.err; text = t.expired; showRetry = true; }
    this.ind.innerHTML = icon;
    this.txt.textContent = text;
    this.retryBtn.textContent = t.retry;
    this.retryBtn.hidden = !showRetry;
    box.setAttribute('aria-busy', s === 'verifying' ? 'true' : 'false');
    if (s === 'idle') {
      // tiklanabilir kip: kutu ikonu bir dugme, metin de tiklanabilir
      var self = this;
      this.ind.innerHTML = '<button type="button" class="click" aria-label="' + t.idle + '">' + ICON.idle + '</button>';
      var go = function () { self.start(); };
      this.ind.firstChild.addEventListener('click', go);
      this.txt.style.cursor = 'pointer';
      this.txt.onclick = go;
    } else {
      this.txt.style.cursor = '';
      this.txt.onclick = null;
    }
    if (detail && s === 'error') this.lastError = detail;
  };

  Widget.prototype.clearTimers = function () {
    for (var i = 0; i < this.timers.length; i++) clearTimeout(this.timers[i]);
    this.timers = [];
    if (this.worker) { try { this.worker.terminate(); } catch (e) { /* yok say */ } this.worker = null; }
  };

  Widget.prototype.fire = function (name, arg) {
    var f = resolveFn(this.opts[name]);
    if (f) { try { f(arg); } catch (e) { if (window.console) console.error(e); } }
  };

  Widget.prototype.fail = function (code) {
    this.clearTimers();
    this.response = '';
    this.input.value = '';
    this.setState('error', code);
    this.fire('error-callback', code);
  };

  Widget.prototype.start = function () {
    var self = this;
    var run = ++this.run;
    this.clearTimers();
    this.response = '';
    this.input.value = '';
    this.setState('verifying');
    var sk = this.opts.sitekey;
    if (!sk) { this.fail('missing_sitekey'); return; }
    var alive = function () { return self.run === run && widgets[self.id] === self; };

    var finish = function (res) {
      if (!alive()) return;
      self.clearTimers();
      self.response = res.response;
      self.input.value = res.response;
      if (res.pass && res.pass_ttl_seconds) writePass(res.pass, res.pass_ttl_seconds);
      self.setState('success');
      var left = Math.max(1000, new Date(res.expires_at).getTime() - Date.now() - 2000);
      self.timers.push(setTimeout(function () {
        if (!alive()) return;
        self.response = '';
        self.input.value = '';
        self.setState('expired');
        self.fire('expired-callback');
      }, left));
      self.fire('callback', res.response);
    };

    var normal = function () {
      post('/api/v1/captcha/challenge', { sitekey: sk, signals: envSignals(0) }).then(function (r) {
        if (!alive()) return;
        if (!r.ok) { self.fail(errCode(r)); return; }
        var ch = r.j;
        var solveStart = Date.now();
        self.timers.push(setTimeout(function () { if (alive() && self.state === 'verifying') self.fail('timeout'); }, SOLVE_TIMEOUT_MS));
        self.solve(ch.nonce, ch.difficulty, alive, function (counter) {
          if (!alive()) return;
          post('/api/v1/captcha/solve', { token: ch.token, counter: counter, signals: envSignals(Date.now() - solveStart) }).then(function (r2) {
            if (!alive()) return;
            if (!r2.ok) { self.fail(errCode(r2)); return; }
            finish(r2.j);
          }, function () { if (alive()) self.fail('network'); });
        }, function (code) { if (alive()) self.fail(code); });
      }, function () { if (alive()) self.fail('network'); });
    };

    var pass = readPass();
    if (pass) {
      post('/api/v1/captcha/renew', { sitekey: sk, pass: pass }).then(function (r) {
        if (!alive()) return;
        if (r.ok) { finish(r.j); return; }
        if (r.status >= 400 && r.status < 500 && r.status !== 429) clearPass();
        normal();
      }, function () { if (alive()) normal(); });
      return;
    }
    normal();
  };

  // PoW: Worker (Blob URL); engellenirse ana is parcaciginda kucuk partilerle.
  Widget.prototype.solve = function (nonce, bits, alive, done, failCb) {
    var self = this;
    var fallback = function () {
      var S = solverFactory(), c = 0, STEP = 20000;
      (function tick() {
        if (!alive()) return;
        var r = S.find(nonce, bits, c, STEP);
        if (r === -2) { failCb('unsupported'); return; }
        if (r >= 0) { done(String(r)); return; }
        c += STEP;
        self.timers.push(setTimeout(tick, 0));
      })();
    };
    var w = null, url = '';
    try {
      var blob = new Blob([WORKER_SRC], { type: 'application/javascript' });
      url = URL.createObjectURL(blob);
      w = new Worker(url);
    } catch (e) { w = null; }
    if (url) { try { URL.revokeObjectURL(url); } catch (e) { /* yok say */ } }
    if (!w) { fallback(); return; }
    this.worker = w;
    var settled = false;
    w.onmessage = function (ev) {
      if (settled) return;
      settled = true;
      try { w.terminate(); } catch (e) { /* yok say */ }
      self.worker = null;
      if (ev.data && typeof ev.data.c === 'number' && ev.data.c >= 0) done(String(ev.data.c));
      else failCb('unsupported');
    };
    w.onerror = function () {
      if (settled) return;
      settled = true;
      try { w.terminate(); } catch (e) { /* yok say */ }
      self.worker = null;
      fallback(); // CSP vb. ile Worker engellendi
    };
    try { w.postMessage({ nonce: nonce, bits: bits }); } catch (e) { settled = true; fallback(); }
  };

  Widget.prototype.reset = function () { this.start(); };

  Widget.prototype.destroy = function () {
    this.run++;
    this.clearTimers();
    if (mqDark && mqDark.removeEventListener) mqDark.removeEventListener('change', this.onTheme);
    if (this.input && this.input.parentNode) this.input.parentNode.removeChild(this.input);
    try { this.root.innerHTML = ''; } catch (e) { /* yok say */ }
    this.el.removeAttribute('data-zv-rendered');
  };

  // ---- genel API ----
  function lookup(id) {
    if (id && widgets[id]) return widgets[id];
    if (!id) { for (var k in widgets) { if (widgets.hasOwnProperty(k)) return widgets[k]; } }
    return null;
  }

  function render(target, opts) {
    var el = typeof target === 'string' ? document.querySelector(target) : target;
    if (!el || el.nodeType !== 1) return null;
    if (el.getAttribute('data-zv-rendered')) return el.getAttribute('data-zv-rendered');
    opts = opts || {};
    var ds = el.dataset || {};
    var o = {
      sitekey: opts.sitekey || ds.sitekey,
      callback: opts.callback || ds.callback,
      'expired-callback': opts['expired-callback'] || ds.expiredCallback,
      'error-callback': opts['error-callback'] || ds.errorCallback,
      theme: opts.theme || ds.theme || 'auto',
      lang: opts.lang || ds.lang || 'auto',
      mode: opts.mode || ds.mode || 'auto'
    };
    var w = new Widget(el, o);
    widgets[w.id] = w;
    el.setAttribute('data-zv-rendered', w.id);
    return w.id;
  }

  var api = {
    __zv: true,
    version: VERSION,
    render: render,
    getResponse: function (id) { var w = lookup(id); return w ? w.response : ''; },
    reset: function (id) { var w = lookup(id); if (w) w.reset(); },
    remove: function (id) { var w = lookup(id); if (w) { delete widgets[w.id]; w.destroy(); } }
  };
  window.zorvenCaptcha = api;

  function autoRender() {
    var els = document.querySelectorAll('.zorven-captcha:not([data-zv-rendered])');
    for (var i = 0; i < els.length; i++) render(els[i]);
  }
  function init() {
    if (params.render !== 'explicit') autoRender();
    var cb = params.onload && window[params.onload];
    if (typeof cb === 'function') { try { cb(); } catch (e) { if (window.console) console.error(e); } }
  }
  if (document.readyState === 'loading') document.addEventListener('DOMContentLoaded', init);
  else init();
})();
