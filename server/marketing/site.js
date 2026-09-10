// Zorven tanıtım sitesi — paylaşılan istemci betiği.
// (1) dil geçişi TR⇄EN, (2) hreflang, (3) çerez rızası (kabul/ret) + rızaya bağlı analytics.
(function () {
  // Gizlilik dostu analytics (self-hosted Umami). websiteId dolunca aktifleşir;
  // yalnızca kullanıcı çerez bildiriminde "Kabul" derse yüklenir.
  var ANALYTICS = { src: 'https://analytics.zorven.app/script.js', websiteId: '9b3df05e-044d-4dbd-ac2f-032032bfeb99' };

  var path = location.pathname;
  var isEN = path === '/en' || path.indexOf('/en/') === 0;
  var lang = isEN ? 'en' : 'tr';

  function trPath() { return isEN ? (path.replace(/^\/en/, '') || '/') : path; }
  function enPath() { return isEN ? path : ('/en' + (path === '/' ? '' : path)); }
  var other = isEN ? trPath() : enPath();

  // (2) hreflang — çok dilli SEO
  function addAlt(hl, href) {
    var l = document.createElement('link');
    l.rel = 'alternate'; l.hreflang = hl; l.href = location.origin + href;
    document.head.appendChild(l);
  }
  addAlt('tr', trPath());
  addAlt('en', enPath());
  addAlt('x-default', trPath());
  try { document.documentElement.lang = lang; } catch (e) {}

  var KEY = 'zorven_cookie_consent'; // 'accepted' | 'rejected'

  function loadAnalytics() {
    if (!ANALYTICS.websiteId || window.__zorvenAnalytics) return;
    window.__zorvenAnalytics = true;
    var s = document.createElement('script');
    s.defer = true;
    s.src = ANALYTICS.src;
    s.setAttribute('data-website-id', ANALYTICS.websiteId);
    document.head.appendChild(s);
  }

  document.addEventListener('DOMContentLoaded', function () {
    // (1) dil geçiş butonu
    var cta = document.querySelector('.nav-cta');
    if (cta) {
      var a = document.createElement('a');
      a.href = other; a.className = 'btn btn-ghost';
      a.setAttribute('aria-label', isEN ? 'Türkçe' : 'English');
      a.textContent = isEN ? 'TR' : 'EN';
      cta.insertBefore(a, cta.firstChild);
    }
    // Onceki rıza kabulse analytics'i yükle
    var prev = null;
    try { prev = localStorage.getItem(KEY); } catch (e) {}
    if (prev === 'accepted') loadAnalytics();
    if (!prev) initConsent();

    // (4) Panel mockup sekmeleri — tıklayınca ilgili içerik gösterilir.
    var side = document.querySelector('.preview .side');
    if (side) {
      side.addEventListener('click', function (e) {
        var it = e.target.closest('.item');
        if (!it) return;
        var tab = it.getAttribute('data-tab');
        var items = side.querySelectorAll('.item');
        for (var i = 0; i < items.length; i++) {
          items[i].classList.toggle('on', items[i] === it);
        }
        var panels = document.querySelectorAll('.preview .panel');
        for (var j = 0; j < panels.length; j++) {
          panels[j].hidden = panels[j].getAttribute('data-panel') !== tab;
        }
      });
    }
  });

  // (3) çerez rızası — kabul/ret
  function initConsent() {
    var t = {
      tr: { msg: 'Zorunlu çerezleri her zaman kullanırız. İzin verirsen, siteyi iyileştirmek için gizlilik dostu (çerezsiz) analytics de kullanırız. ',
            link: 'Çerez Politikası', href: '/legal/cookies', acc: 'Kabul et', rej: 'Reddet' },
      en: { msg: 'We always use essential cookies. With your consent we also use privacy-friendly (cookieless) analytics to improve the site. ',
            link: 'Cookie Policy', href: '/en/legal/cookies', acc: 'Accept', rej: 'Reject' }
    }[lang];

    var bar = document.createElement('div');
    bar.setAttribute('role', 'dialog');
    bar.style.cssText = 'position:fixed;left:16px;right:16px;bottom:16px;z-index:1000;max-width:760px;margin:0 auto;' +
      'display:flex;gap:14px;align-items:center;justify-content:space-between;flex-wrap:wrap;' +
      'background:#111114;border:1px solid rgba(255,255,255,.14);border-radius:14px;padding:14px 18px;' +
      'box-shadow:0 20px 50px -20px rgba(0,0,0,.8);font-family:\'IBM Plex Sans\',system-ui,sans-serif';

    var p = document.createElement('p');
    p.style.cssText = 'margin:0;font-size:13.5px;color:#A3A3A3;line-height:1.5;flex:1;min-width:220px';
    p.innerHTML = t.msg + '<a href="' + t.href + '" style="color:#4ADE80;text-decoration:underline">' + t.link + '</a>.';

    var actions = document.createElement('div');
    actions.style.cssText = 'display:flex;gap:8px;flex-shrink:0';

    function choose(val) {
      try { localStorage.setItem(KEY, val); } catch (e) {}
      if (val === 'accepted') loadAnalytics();
      bar.remove();
    }
    var reject = document.createElement('button');
    reject.textContent = t.rej;
    reject.style.cssText = 'cursor:pointer;border:1px solid rgba(255,255,255,.18);border-radius:10px;background:transparent;color:#F5F5F5;font-weight:600;font-size:14px;padding:9px 16px;font-family:inherit';
    reject.onclick = function () { choose('rejected'); };
    var accept = document.createElement('button');
    accept.textContent = t.acc;
    accept.style.cssText = 'cursor:pointer;border:none;border-radius:10px;background:#22C55E;color:#052E16;font-weight:600;font-size:14px;padding:9px 18px;white-space:nowrap;font-family:inherit';
    accept.onclick = function () { choose('accepted'); };

    actions.appendChild(reject); actions.appendChild(accept);
    bar.appendChild(p); bar.appendChild(actions);
    document.body.appendChild(bar);
  }
})();
