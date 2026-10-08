package ingress

import (
	"html/template"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
)

// Ziyaretci giris sayfalari (OAuth butonlari / Basic Auth formu).
//
// Gorunum errors.go'daki hata kartiyla ayni dildedir (koyu kart, monospace kod
// etiketi, "zorven tunnel" alt bilgisi); ek olarak prefers-color-scheme ile acik
// temaya gecer. Harici kaynak yoktur: yalnizca satir ici CSS/SVG (CSP dostu).
// Tum dinamik degerler html/template ile kacislanir.

// pageKind, giris sayfasinin turu.
type pageKind string

const (
	pageOAuth        pageKind = "oauth"        // saglayici butonlari
	pageDenied       pageKind = "denied"       // oturum var, e-posta izinli degil
	pageUnconfigured pageKind = "unconfigured" // kullanilabilir saglayici yok
	pageBasic        pageKind = "basic"        // kullanici adi + parola formu
	pageLimited      pageKind = "limited"      // cok fazla basarisiz deneme
	pageDoorOpen     pageKind = "dooropen"     // web door: kapi acildi (IP, sure, adres, kapat dugmesi)
	pageDoorClosed   pageKind = "doorclosed"   // web door: kapi kapatildi
)

type providerButton struct {
	Name  string
	Label string
	Href  string
	Icon  template.HTML // sabit, guvenilir SVG
}

type accessPageData struct {
	Lang      string
	Code      string
	Kind      pageKind
	Heading   string
	Message   string
	Hint      string
	Providers []providerButton
	// denied
	SwitchHref  string
	SwitchLabel string
	// basic
	RD         string
	User       string
	LoginError string
	LabelUser  string
	LabelPass  string
	SubmitText string
	// web door
	Rows       []doorRow
	CloseText  string // "Kapiyi kapat" dugmesi
	CloseHref  string // kapali sayfasinda "tekrar giris yap" baglantisi
	ReopenText string
}

// doorRow, "kapi acildi" sayfasindaki etiket/deger satiri.
type doorRow struct {
	Label string
	Value string
}

// pageText, bir dilin tum metinleri.
type pageText struct {
	protectedTitle, oauthHint, continueWith string
	basicHint, userLabel, passLabel, submit string
	badCreds                                string
	deniedTitle, deniedMsg, switchAccount   string
	unconfTitle, unconfMsg                  string
	limitedTitle, limitedMsg                string
	htmlLang                                string

	// plan siniri nedeniyle askiya alinmis adres
	suspendedTitle, suspendedMsg string

	// web door
	doorOpenTitle, doorOpenMsg, doorClosedTitle, doorClosedMsg string
	doorUnavailTitle, doorUnavailMsg                           string
	doorIP, doorUntil, doorAddr, doorProto, doorIdentity       string
	doorClose, doorReopen                                      string
	doorRemainingFmt                                           string // %d sa %d dk
	doorRemainingDaysFmt                                       string // %d gun %d sa
	doorSNIHint                                                string
}

var pageTexts = map[string]pageText{
	"tr": {
		htmlLang:       "tr",
		suspendedTitle: "Bu adres askıya alındı",
		suspendedMsg:   "Hesap sahibinin planı bu adresi içermiyor. Hesap sahibi planını yükselttiğinde adres yeniden açılır. Adres hesap sahibine ait olmaya devam eder.",
		protectedTitle: "Bu tünel korumalı",
		oauthHint:      "Devam etmek için aşağıdaki hesaplardan biriyle giriş yapın.",
		continueWith:   " ile devam et",
		basicHint:      "Devam etmek için kullanıcı adı ve parolanızı girin.",
		userLabel:      "Kullanıcı adı",
		passLabel:      "Parola",
		submit:         "Giriş yap",
		badCreds:       "Kullanıcı adı veya parola hatalı.",
		deniedTitle:    "Erişim reddedildi",
		deniedMsg:      "Hesabınız (%s) bu tünele erişim yetkisine sahip değil.",
		switchAccount:  "Farklı hesapla giriş yap",
		unconfTitle:    "Giriş henüz kullanılamıyor",
		unconfMsg:      "Tünel sahibi ziyaretçi girişini henüz tamamlamamış. Lütfen tünel sahibiyle iletişime geçin.",
		limitedTitle:   "Çok fazla deneme",
		limitedMsg:     "Çok sayıda başarısız giriş denemesi yapıldı. Lütfen biraz sonra tekrar deneyin.",

		doorOpenTitle:        "Kapı açıldı",
		doorOpenMsg:          "Bu IP adresinden gelen bağlantılara izin verildi. Aşağıdaki adrese bağlanabilirsiniz.",
		doorClosedTitle:      "Kapı kapatıldı",
		doorClosedMsg:        "Bu IP adresi için bağlantı izni kaldırıldı. Yeniden bağlanmak için tekrar giriş yapın.",
		doorUnavailTitle:     "Kapı kullanılamıyor",
		doorUnavailMsg:       "Bu tünelin planı web ile kapı açmayı içermiyor. Lütfen tünel sahibiyle iletişime geçin.",
		doorIP:               "IP adresiniz",
		doorUntil:            "Geçerlilik bitişi",
		doorAddr:             "Bağlantı adresi",
		doorProto:            "Protokol",
		doorIdentity:         "Giriş yapan",
		doorClose:            "Kapıyı kapat",
		doorReopen:           "Tekrar giriş yap",
		doorRemainingFmt:     "%d sa %d dk kaldı",
		doorRemainingDaysFmt: "%d gün %d sa kaldı",
		doorSNIHint:          "TLS (SNI) ile bağlanılır; 'zorven forward' kullanın.",
	},
	"en": {
		htmlLang:       "en",
		suspendedTitle: "This address is suspended",
		suspendedMsg:   "The account owner's plan does not include this address. It reopens automatically when the owner upgrades their plan. The address remains reserved for the owner.",
		protectedTitle: "This tunnel is protected",
		oauthHint:      "Sign in with one of the accounts below to continue.",
		continueWith:   "Continue with ",
		basicHint:      "Enter your username and password to continue.",
		userLabel:      "Username",
		passLabel:      "Password",
		submit:         "Sign in",
		badCreds:       "Incorrect username or password.",
		deniedTitle:    "Access denied",
		deniedMsg:      "Your account (%s) is not authorized to access this tunnel.",
		switchAccount:  "Sign in with a different account",
		unconfTitle:    "Sign-in is not available yet",
		unconfMsg:      "The tunnel owner has not finished setting up visitor sign-in yet. Please contact the tunnel owner.",
		limitedTitle:   "Too many attempts",
		limitedMsg:     "Too many failed sign-in attempts. Please try again in a little while.",

		doorOpenTitle:        "Access granted",
		doorOpenMsg:          "Connections from this IP address are now allowed. You can connect to the address below.",
		doorClosedTitle:      "Door closed",
		doorClosedMsg:        "Connection access for this IP address was removed. Sign in again to reconnect.",
		doorUnavailTitle:     "Door unavailable",
		doorUnavailMsg:       "This tunnel's plan does not include web door access. Please contact the tunnel owner.",
		doorIP:               "Your IP address",
		doorUntil:            "Valid until",
		doorAddr:             "Connection address",
		doorProto:            "Protocol",
		doorIdentity:         "Signed in as",
		doorClose:            "Close the door",
		doorReopen:           "Sign in again",
		doorRemainingFmt:     "%d h %d min left",
		doorRemainingDaysFmt: "%d d %d h left",
		doorSNIHint:          "Connects over TLS (SNI); use 'zorven forward'.",
	},
}

// pickLang, Accept-Language'e gore "tr" veya "en" secer (varsayilan tr).
// q degerlerine gore siralar; tr/en disindaki diller atlanir.
func pickLang(r *http.Request) string {
	type cand struct {
		lang string
		q    float64
		idx  int
	}
	var cs []cand
	for i, part := range strings.Split(r.Header.Get("Accept-Language"), ",") {
		tag, params, _ := strings.Cut(strings.TrimSpace(part), ";")
		q := 1.0
		if params != "" {
			if v, ok := strings.CutPrefix(strings.TrimSpace(params), "q="); ok {
				if f, err := strconv.ParseFloat(v, 64); err == nil {
					q = f
				}
			}
		}
		primary, _, _ := strings.Cut(strings.ToLower(strings.TrimSpace(tag)), "-")
		if (primary == "tr" || primary == "en") && q > 0 {
			cs = append(cs, cand{primary, q, i})
		}
	}
	if len(cs) == 0 {
		return "tr"
	}
	sort.SliceStable(cs, func(a, b int) bool { return cs[a].q > cs[b].q })
	return cs[0].lang
}

// wantsHTMLPage, istegin bir tarayicinin sayfa gezinmesi oldugunu soyler:
// GET/HEAD ve Accept'te text/html.
func wantsHTMLPage(r *http.Request) bool {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		return false
	}
	return strings.Contains(strings.ToLower(r.Header.Get("Accept")), "text/html")
}

// Sabit marka ikonlari (tek renk, currentColor).
const (
	iconGoogle = `<svg viewBox="0 0 24 24" width="18" height="18" aria-hidden="true" focusable="false"><path fill="currentColor" d="M12.48 10.92v3.28h7.84c-.24 1.84-.853 3.187-1.787 4.133-1.147 1.147-2.933 2.4-6.053 2.4-4.827 0-8.6-3.893-8.6-8.72s3.773-8.72 8.6-8.72c2.6 0 4.507 1.027 5.907 2.347l2.307-2.307C18.747 1.44 16.133 0 12.48 0 5.867 0 .307 5.387.307 12s5.56 12 12.173 12c3.573 0 6.267-1.173 8.373-3.36 2.16-2.16 2.84-5.213 2.84-7.667 0-.76-.053-1.467-.173-2.053H12.48z"/></svg>`
	iconGitHub = `<svg viewBox="0 0 24 24" width="18" height="18" aria-hidden="true" focusable="false"><path fill="currentColor" d="M12 .297c-6.63 0-12 5.373-12 12 0 5.303 3.438 9.8 8.205 11.385.6.113.82-.258.82-.577 0-.285-.01-1.04-.015-2.04-3.338.724-4.042-1.61-4.042-1.61C4.422 18.07 3.633 17.7 3.633 17.7c-1.087-.744.084-.729.084-.729 1.205.084 1.838 1.236 1.838 1.236 1.07 1.835 2.809 1.305 3.495.998.108-.776.417-1.305.76-1.605-2.665-.3-5.466-1.332-5.466-5.93 0-1.31.465-2.38 1.235-3.22-.135-.303-.54-1.523.105-3.176 0 0 1.005-.322 3.3 1.23.96-.267 1.98-.399 3-.405 1.02.006 2.04.138 3 .405 2.28-1.552 3.285-1.23 3.285-1.23.645 1.653.24 2.873.12 3.176.765.84 1.23 1.91 1.23 3.22 0 4.61-2.805 5.625-5.475 5.92.42.36.81 1.096.81 2.22 0 1.606-.015 2.896-.015 3.286 0 .315.21.69.825.57C20.565 22.092 24 17.592 24 12.297c0-6.627-5.373-12-12-12"/></svg>`
)

// providerOrder, butonlarin sabit gorunum sirasi.
var providerOrder = []string{"google", "github"}

func providerMeta(name string) (label string, icon template.HTML, ok bool) {
	switch name {
	case "google":
		return "Google", template.HTML(iconGoogle), true
	case "github":
		return "GitHub", template.HTML(iconGitHub), true
	}
	return "", "", false
}

// buildProviderButtons, sunucuda yapilandirilmis VE tunelin izin verdigi
// saglayicilari butona cevirir. allowed bossa tunel kisitlamamistir: yapilandirilmis
// hepsi gosterilir.
func buildProviderButtons(lang string, configured, allowed []string, rd string) []providerButton {
	cfg := map[string]bool{}
	for _, c := range configured {
		cfg[strings.ToLower(c)] = true
	}
	allow := map[string]bool{}
	for _, a := range allowed {
		allow[strings.ToLower(strings.TrimSpace(a))] = true
	}
	t := pageTexts[lang]
	var out []providerButton
	for _, name := range providerOrder {
		if !cfg[name] || (len(allow) > 0 && !allow[name]) {
			continue
		}
		label, icon, ok := providerMeta(name)
		if !ok {
			continue
		}
		q := url.Values{}
		q.Set("p", name)
		q.Set("rd", rd)
		text := t.continueWith + label
		if lang == "tr" {
			text = label + t.continueWith
		}
		out = append(out, providerButton{
			Name: name, Label: text, Icon: icon,
			Href: "/_zva/start?" + q.Encode(),
		})
	}
	return out
}

// renderAccessPage, giris/ret sayfasini yazar.
func renderAccessPage(w http.ResponseWriter, r *http.Request, status int, d accessPageData) {
	d.Lang = pageTexts[d.Lang].htmlLang
	if d.Lang == "" {
		d.Lang = "tr"
	}
	h := w.Header()
	h.Set("Content-Type", "text/html; charset=utf-8")
	h.Set("Cache-Control", "no-store")
	h.Set("X-Content-Type-Options", "nosniff")
	h.Set("X-Frame-Options", "DENY")
	h.Set("Referrer-Policy", "same-origin")
	h.Set("Content-Security-Policy",
		"default-src 'none'; style-src 'unsafe-inline'; img-src data:; form-action 'self'; base-uri 'none'; frame-ancestors 'none'")
	h.Set("X-Rpshell-Error", d.Code)
	w.WriteHeader(status)
	if r.Method == http.MethodHead {
		return
	}
	_ = accessPageTmpl.Execute(w, d)
}

var accessPageTmpl = template.Must(template.New("access").Parse(`<!doctype html>
<html lang="{{.Lang}}">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<meta name="color-scheme" content="dark light">
<meta name="robots" content="noindex">
<title>{{.Heading}} · zorven</title>
<style>
  :root { color-scheme: dark light; --bg:#0A0A0A; --fg:#F5F5F5; --card:#171717; --line:#262626;
          --muted:#A3A3A3; --faint:#8A8A8A; --field:#0A0A0A; --accent:#F5F5F5; --accent-fg:#0A0A0A;
          --danger:#F87171; --dot:#22C55E; }
  @media (prefers-color-scheme: light) {
    :root { --bg:#F5F5F5; --fg:#171717; --card:#FFFFFF; --line:#E5E5E5; --muted:#525252;
            --faint:#737373; --field:#FFFFFF; --accent:#171717; --accent-fg:#FFFFFF; --danger:#B91C1C; }
  }
  * { box-sizing: border-box; }
  body { margin:0; min-height:100vh; display:grid; place-items:center; padding:24px;
         background:var(--bg); color:var(--fg);
         font-family:'IBM Plex Sans', ui-sans-serif, system-ui, sans-serif; }
  .card { max-width:30rem; width:100%; border:1px solid var(--line); border-radius:12px;
          background:var(--card); padding:28px; }
  .code { margin:0; font-family:ui-monospace,'JetBrains Mono',monospace; font-size:11px;
          letter-spacing:.08em; text-transform:uppercase; color:var(--faint); }
  h1 { margin:10px 0 6px; font-size:22px; font-weight:700; color:var(--fg); }
  p { margin:0; color:var(--muted); font-size:14px; line-height:1.55; }
  .actions { display:grid; gap:10px; margin-top:22px; }
  .btn { display:flex; align-items:center; justify-content:center; gap:10px; min-height:44px;
         padding:10px 16px; border:1px solid var(--line); border-radius:8px; background:transparent;
         color:var(--fg); font:inherit; font-size:14px; font-weight:600; text-decoration:none; cursor:pointer; }
  .btn:hover { border-color:var(--faint); }
  .btn:focus-visible, input:focus-visible { outline:2px solid var(--fg); outline-offset:2px; }
  .btn.primary { background:var(--accent); color:var(--accent-fg); border-color:var(--accent); }
  form { margin-top:22px; display:grid; gap:14px; }
  label { display:block; margin-bottom:6px; font-size:13px; font-weight:600; color:var(--muted); }
  input[type=text], input[type=password] { width:100%; min-height:44px; padding:10px 12px;
         border:1px solid var(--line); border-radius:8px; background:var(--field); color:var(--fg);
         font:inherit; font-size:16px; }
  .err { margin-top:16px; padding:10px 12px; border:1px solid var(--danger); border-radius:8px;
         color:var(--danger); font-size:13px; }
  .kv { margin:20px 0 0; display:grid; gap:0; border:1px solid var(--line); border-radius:8px; overflow:hidden; }
  .kv div { display:flex; justify-content:space-between; gap:16px; padding:10px 12px; font-size:13px;
            border-top:1px solid var(--line); }
  .kv div:first-child { border-top:0; }
  .kv dt { color:var(--muted); }
  .kv dd { margin:0; color:var(--fg); text-align:right; word-break:break-all;
           font-family:ui-monospace,'JetBrains Mono',monospace; }
  .status { margin-top:22px; padding-top:16px; border-top:1px solid var(--line);
            font-family:ui-monospace,'JetBrains Mono',monospace; font-size:12px; color:var(--faint); }
  .dot { display:inline-block; width:6px; height:6px; border-radius:50%; background:var(--dot);
         margin-right:7px; vertical-align:middle; }
</style>
</head>
<body>
  <main class="card">
    <p class="code" lang="en">{{.Code}}</p>
    <h1>{{.Heading}}</h1>
    {{if .Message}}<p>{{.Message}}</p>{{end}}
    {{if .LoginError}}<div class="err" role="alert">{{.LoginError}}</div>{{end}}
    {{if eq .Kind "oauth"}}
      <div class="actions">
        {{range .Providers}}<a class="btn" href="{{.Href}}" rel="nofollow">{{.Icon}}<span>{{.Label}}</span></a>
        {{end}}
      </div>
    {{else if eq .Kind "denied"}}
      <div class="actions"><a class="btn primary" href="{{.SwitchHref}}" rel="nofollow">{{.SwitchLabel}}</a></div>
    {{else if eq .Kind "basic"}}
      <form method="post" action="/_zvb/login">
        <input type="hidden" name="rd" value="{{.RD}}">
        <div><label for="zv-user">{{.LabelUser}}</label>
          <input id="zv-user" name="user" type="text" value="{{.User}}" autocomplete="username"
                 autocapitalize="none" autocorrect="off" spellcheck="false" required autofocus></div>
        <div><label for="zv-pass">{{.LabelPass}}</label>
          <input id="zv-pass" name="pass" type="password" autocomplete="current-password" required></div>
        <button class="btn primary" type="submit">{{.SubmitText}}</button>
      </form>
    {{else if eq .Kind "dooropen"}}
      <dl class="kv">{{range .Rows}}<div><dt>{{.Label}}</dt><dd>{{.Value}}</dd></div>
      {{end}}</dl>
      <form method="post" action="/_zvd/close">
        <button class="btn" type="submit">{{.CloseText}}</button>
      </form>
    {{else if eq .Kind "doorclosed"}}
      <div class="actions"><a class="btn primary" href="{{.CloseHref}}" rel="nofollow">{{.ReopenText}}</a></div>
    {{end}}
    <div class="status"><span class="dot"></span>zorven tunnel</div>
  </main>
</body>
</html>`))
