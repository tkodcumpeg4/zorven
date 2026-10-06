package ingress

import (
	"html"
	"net/http"
	"strings"

	"github.com/tkodcumpeg4/zorven/server/store"
)

// FAZ 4 — Kötüye kullanım / phishing caydırma.
//
// Ücretsiz katman kullanicilarinin *.PlatformDomain altinda paylastigi HTTP
// tunellerine ilk ziyarette bir UYARI ARA-SAYFASI (interstitial) gosterilir:
// icerik Zorven tarafindan dogrulanmamistir, ziyaretci kisisel/finansal bilgi
// girmemeli. "Devam et" bir onay cerezi yazip sayfayi yeniler; sonraki istekler
// (ve asset'ler) atlanir. Ucretli katman, ozel domain ve ust-seviye-olmayan
// istekler (asset/XHR/WS) etkilenmez.

const interstitialCookie = "_zorven_ack"

// shouldShowInterstitial, bu istegin uyari ara-sayfasini gerektirip
// gerektirmedigini soyler.
func (h *Handler) shouldShowInterstitial(tun store.HostRoute, r *http.Request) bool {
	if h.PlatformDomain == "" {
		return false
	}
	// Yalnizca platform-domain (*.zorven.app) tunelleri. Ozel domainler sahibinin
	// kontrolundedir; interstitial gosterilmez.
	host := normalizeHost(r.Host)
	if host != h.PlatformDomain && !strings.HasSuffix(host, "."+h.PlatformDomain) {
		return false
	}
	// Yalnizca ucretsiz katman (bos plan = free).
	if tun.Plan != "" && tun.Plan != store.PlanFree {
		return false
	}
	// Yalnizca ham TCP/UDP olmayan, HTTP tunelleri (interstitial bir HTML sayfasidir).
	if tun.Proto != "" && tun.Proto != store.ProtoHTTP {
		return false
	}
	// Yalnizca ust-seviye HTML navigasyonu: GET + Accept: text/html.
	// Asset (css/js/img), XHR ve WebSocket yukseltmeleri atlanir.
	if r.Method != http.MethodGet {
		return false
	}
	if isWebSocketUpgrade(r) {
		return false
	}
	if !strings.Contains(r.Header.Get("Accept"), "text/html") {
		return false
	}
	// Onay verildiyse gec.
	if c, err := r.Cookie(interstitialCookie); err == nil && c.Value == "1" {
		return false
	}
	return true
}

// serveInterstitial, uyari ara-sayfasini yazar (HTTP 200).
func (h *Handler) serveInterstitial(w http.ResponseWriter, r *http.Request) {
	host := html.EscapeString(normalizeHost(r.Host))
	page := strings.ReplaceAll(interstitialHTML, "{{HOST}}", host)
	page = strings.ReplaceAll(page, "{{PLATFORM}}", html.EscapeString(h.PlatformDomain))
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Referrer-Policy", "no-referrer")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte(page))
}

// serveSuspended, platform admin tarafindan dondurulmus (askiya alinmis) bir
// tunel icin "erisim engellendi" sayfasi yazar (FAZ 4). 451 Unavailable For
// Legal Reasons: icerik kotuye kullanim politikasi geregi askiya alinmistir.
func (h *Handler) serveSuspended(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusUnavailableForLegalReasons) // 451
	_, _ = w.Write([]byte(suspendedHTML))
}

const suspendedHTML = `<!doctype html>
<html lang="tr">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<meta name="robots" content="noindex, nofollow">
<meta name="color-scheme" content="dark">
<title>Askıya alındı · zorven</title>
<style>
  :root { color-scheme: dark; }
  body { margin:0; min-height:100vh; display:grid; place-items:center; padding:24px;
    background:#0A0A0A; color:#F5F5F5; font-family:ui-sans-serif,system-ui,-apple-system,'Segoe UI',Roboto,sans-serif; line-height:1.55; }
  .card { width:100%; max-width:480px; background:#141414; border:1px solid #262626; border-radius:14px; padding:28px 26px; }
  h1 { font-size:18px; margin:0 0 10px; }
  p { margin:0 0 12px; color:#C7C7C7; font-size:14px; }
</style>
</head>
<body>
  <div class="card">
    <h1>Bu bağlantı askıya alındı</h1>
    <p>Bu Zorven bağlantısı, kötüye kullanım politikası gereği erişime kapatılmıştır.</p>
    <p>Bir hata olduğunu düşünüyorsanız <strong>abuse@zorven.app</strong> adresiyle iletişime geçin.</p>
  </div>
</body>
</html>`

const interstitialHTML = `<!doctype html>
<html lang="tr">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<meta name="robots" content="noindex, nofollow">
<meta name="color-scheme" content="dark">
<title>Uyarı · {{HOST}}</title>
<style>
  :root { color-scheme: dark; }
  * { box-sizing: border-box; }
  body {
    margin: 0; min-height: 100vh; padding: 24px;
    display: grid; place-items: center;
    background: #0A0A0A; color: #F5F5F5;
    font-family: ui-sans-serif, system-ui, -apple-system, 'Segoe UI', Roboto, sans-serif;
    line-height: 1.55;
  }
  .card {
    width: 100%; max-width: 520px;
    background: #141414; border: 1px solid #262626; border-radius: 14px;
    padding: 28px 26px;
  }
  .brand { display: flex; align-items: center; gap: 8px; margin-bottom: 18px; }
  .brand svg { width: 22px; height: 22px; }
  .brand span { font-weight: 700; letter-spacing: -0.01em; }
  h1 { font-size: 18px; margin: 0 0 10px; }
  p { margin: 0 0 12px; color: #C7C7C7; font-size: 14px; }
  .host { font-family: ui-monospace, SFMono-Regular, Menlo, monospace; color: #7CE7B0; word-break: break-all; }
  .warn {
    background: rgba(245, 158, 11, 0.08); border: 1px solid rgba(245, 158, 11, 0.3);
    border-radius: 10px; padding: 12px 14px; margin: 14px 0 18px; font-size: 13px; color: #FCD9A6;
  }
  .actions { display: flex; gap: 10px; flex-wrap: wrap; }
  button {
    appearance: none; cursor: pointer; border: 0; border-radius: 9px;
    padding: 11px 18px; font-size: 14px; font-weight: 600;
    background: #22C55E; color: #06240F;
  }
  button:hover { opacity: 0.92; }
  a.report { color: #9A9A9A; font-size: 12px; text-decoration: none; align-self: center; }
  a.report:hover { color: #C7C7C7; text-decoration: underline; }
  .foot { margin-top: 18px; font-size: 11px; color: #6B6B6B; }
</style>
</head>
<body>
  <div class="card">
    <div class="brand">
      <svg viewBox="0 0 24 24" fill="none" aria-hidden="true"><path d="M12 2c3 3 6 4 8 4 0 6-3 11-8 14-5-3-8-8-8-14 2 0 5-1 8-4Z" stroke="#22C55E" stroke-width="1.6" stroke-linejoin="round"/></svg>
      <span>zorven</span>
    </div>
    <h1>Devam etmeden önce</h1>
    <p>Erişmek üzere olduğunuz adres <span class="host">{{HOST}}</span>, bir kullanıcı tarafından Zorven üzerinden paylaşılan geçici bir bağlantıdır.</p>
    <div class="warn">
      Bu içerik <strong>Zorven tarafından doğrulanmamıştır</strong>. Bu sayfaya
      şifre, kredi kartı, kimlik veya başka kişisel/finansal bilgilerinizi
      yalnızca site sahibine <strong>güveniyorsanız</strong> girin.
    </div>
    <div class="actions">
      <button id="go" type="button">Anladım, devam et</button>
      <a class="report" href="mailto:abuse@{{PLATFORM}}?subject=Kotuye%20kullanim%20bildirimi:%20{{HOST}}">Bu siteyi bildir</a>
    </div>
    <div class="foot">Bu uyarı ücretsiz Zorven bağlantılarında bir kez gösterilir.</div>
  </div>
  <script>
    document.getElementById('go').addEventListener('click', function () {
      document.cookie = '` + interstitialCookie + `=1; path=/; max-age=86400; samesite=lax';
      location.reload();
    });
  </script>
</body>
</html>`
