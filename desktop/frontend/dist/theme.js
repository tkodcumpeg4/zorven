// Tema: varsayilan isletim sistemini izler; ust cubuktaki dugme secimi saklar.
// Ilk cizimden once yuklenir (acilista yanlis tema parlamasin).
(function () {
  var KEY = 'zv-theme';
  function stored() {
    try { var v = localStorage.getItem(KEY); return v === 'light' || v === 'dark' ? v : ''; } catch (e) { return ''; }
  }
  function osLight() {
    try { return !!(window.matchMedia && window.matchMedia('(prefers-color-scheme: light)').matches); } catch (e) { return false; }
  }
  function effective() { return stored() || (osLight() ? 'light' : 'dark'); }
  function apply() {
    var light = effective() === 'light';
    document.documentElement.classList.toggle('light', light);
    var u = document.getElementById('themeUse');
    if (u) u.setAttribute('href', light ? '#i-moon' : '#i-sun');
  }
  // Secimi Go tarafina da bildir: pencere cercevesi (Windows baslik cubugu) ayni
  // temaya gecer ve bir sonraki acilista pencere dogru renkle baslar.
  function syncNative(mode) {
    try {
      var app = window.go && window.go.main && window.go.main.App;
      if (app && app.SetTheme) app.SetTheme(mode || '');
    } catch (e) { /* Go baglamasi yoksa (tarayici onizleme) yok say */ }
  }
  function toggle() {
    var next = effective() === 'light' ? 'dark' : 'light';
    try { localStorage.setItem(KEY, next); } catch (e) { /* depolama yoksa yalniz bu oturum */ }
    window.__zvTheme = next;
    apply();
    syncNative(next);
  }
  window.ZTheme = { apply: apply, toggle: toggle, effective: effective };
  // Depolama yazilamazsa bile secim oturum boyunca gecerli kalsin.
  var _eff = effective;
  effective = function () { return window.__zvTheme || _eff(); };
  apply();
  // Wails baglamalari yuklendikten sonra kayitli secimi cerceveye uygula.
  window.addEventListener('load', function () { setTimeout(function () { syncNative(stored()); }, 300); });
  try {
    var mq = window.matchMedia('(prefers-color-scheme: light)');
    var on = function () { if (!stored() && !window.__zvTheme) apply(); };
    if (mq.addEventListener) mq.addEventListener('change', on); else if (mq.addListener) mq.addListener(on);
  } catch (e) { /* eski WebView */ }
})();
