package botguard

import (
	"crypto/rand"
	"html/template"
	"io"
	"net/http"
	"strconv"
	"strings"
)

type pageText struct {
	Title, Heading, Checking, Progress, Noscript, Error, Retry, NeedHTTPS, Footer string
}

var pageTexts = map[string]pageText{
	"en": {
		Title:     "Checking your browser",
		Heading:   "Checking your browser",
		Checking:  "This takes a few seconds. You will be redirected automatically.",
		Progress:  "Progress",
		Noscript:  "JavaScript is required to continue. Please enable JavaScript and reload this page.",
		Error:     "Verification failed. Please try again.",
		Retry:     "Try again",
		NeedHTTPS: "This check needs a secure (HTTPS) connection and a current browser.",
		Footer:    "Protected by Zorven Tunnel",
	},
	"tr": {
		Title:     "Tarayıcınız kontrol ediliyor",
		Heading:   "Tarayıcınız kontrol ediliyor",
		Checking:  "Bu işlem birkaç saniye sürer. Otomatik olarak yönlendirileceksiniz.",
		Progress:  "İlerleme",
		Noscript:  "Devam etmek için JavaScript gereklidir. Lütfen JavaScript'i etkinleştirip sayfayı yenileyin.",
		Error:     "Doğrulama başarısız oldu. Lütfen tekrar deneyin.",
		Retry:     "Tekrar dene",
		NeedHTTPS: "Bu kontrol güvenli (HTTPS) bir bağlantı ve güncel bir tarayıcı gerektirir.",
		Footer:    "Zorven Tünel ile korunuyor",
	},
	"ja": {
		Title:     "ブラウザを確認しています",
		Heading:   "ブラウザを確認しています",
		Checking:  "数秒かかります。自動的に移動します。",
		Progress:  "進捗",
		Noscript:  "続行するには JavaScript が必要です。JavaScript を有効にしてページを再読み込みしてください。",
		Error:     "確認に失敗しました。もう一度お試しください。",
		Retry:     "再試行",
		NeedHTTPS: "この確認には安全な (HTTPS) 接続と最新のブラウザが必要です。",
		Footer:    "Zorven トンネルで保護されています",
	},
	"zh": {
		Title:     "正在检查您的浏览器",
		Heading:   "正在检查您的浏览器",
		Checking:  "这需要几秒钟，完成后将自动跳转。",
		Progress:  "进度",
		Noscript:  "需要启用 JavaScript 才能继续。请启用 JavaScript 后重新加载本页面。",
		Error:     "验证失败，请重试。",
		Retry:     "重试",
		NeedHTTPS: "此检查需要安全 (HTTPS) 连接和较新的浏览器。",
		Footer:    "由 Zorven 隧道提供保护",
	},
}

// PickLang, Accept-Language'dan tr/en/ja/zh secer (q degerine gore; yoksa en).
func PickLang(r *http.Request) string {
	bestQ, best := 0.0, "en"
	for _, part := range strings.Split(r.Header.Get("Accept-Language"), ",") {
		tag, params, _ := strings.Cut(strings.TrimSpace(part), ";")
		q := 1.0
		if v, ok := strings.CutPrefix(strings.TrimSpace(params), "q="); ok {
			if f, err := strconv.ParseFloat(v, 64); err == nil {
				q = f
			}
		}
		primary, _, _ := strings.Cut(strings.ToLower(strings.TrimSpace(tag)), "-")
		if _, ok := pageTexts[primary]; ok && q > 0 {
			// Esit q'da ilk gelen kazanir (i artarken yalniz kesin buyuk degistirir).
			if q > bestQ {
				bestQ, best = q, primary
			}
		}
	}
	return best
}

// WantsHTMLPage, sorgu SAYFASI gosterilebilir mi: GET/HEAD ve Accept text/html.
func WantsHTMLPage(r *http.Request) bool {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		return false
	}
	return strings.Contains(strings.ToLower(r.Header.Get("Accept")), "text/html")
}

const shieldIcon = `<div class="ic" aria-hidden="true"><span class="ring"></span><svg viewBox="0 0 24 24" width="44" height="44" fill="none" stroke="currentColor" stroke-width="1.6" stroke-linecap="round" stroke-linejoin="round" focusable="false"><path class="sh" pathLength="1" d="M12 3 4.5 6v5.5c0 4.4 3.1 8.3 7.5 9.5 4.4-1.2 7.5-5.1 7.5-9.5V6z"/><path class="tr" pathLength="1" d="M12 3 4.5 6v5.5c0 4.4 3.1 8.3 7.5 9.5 4.4-1.2 7.5-5.1 7.5-9.5V6z"/><path class="ck" pathLength="1" d="m9 12 2.2 2.2L15.5 10"/></svg></div>`

var pageTmpl = template.Must(template.New("p").Parse(`<!doctype html>
<html lang="{{.Lang}}">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width,initial-scale=1">
<meta name="robots" content="noindex,nofollow">
<title>{{.T.Title}}</title>
<style nonce="{{.Nonce}}">
:root{color-scheme:light dark;--bg:#fafafa;--fg:#18181b;--mut:#52525b;--bd:#d4d4d8;--ac:#18181b}
@media (prefers-color-scheme:dark){:root{--bg:#0e0e11;--fg:#f4f4f5;--mut:#a1a1aa;--bd:#3f3f46;--ac:#f4f4f5}}
*{box-sizing:border-box}
body{margin:0;min-height:100vh;display:flex;align-items:center;justify-content:center;background:var(--bg);color:var(--fg);font:16px/1.5 system-ui,-apple-system,"Segoe UI",Roboto,"Noto Sans JP","PingFang SC","Microsoft YaHei","Noto Sans SC",sans-serif}
main{width:100%;max-width:420px;padding:32px 16px;text-align:center}
h1{font-size:20px;margin:16px 0 8px;font-weight:600}
p{margin:0 0 16px;color:var(--mut);font-size:14px}
.bar{height:6px;border-radius:3px;background:var(--bd);overflow:hidden;margin:20px 0 8px}
.bar>i{display:block;height:100%;width:0;background:var(--ac);transition:width .2s}
button{font:inherit;font-size:14px;padding:8px 16px;border:1px solid var(--bd);border-radius:6px;background:transparent;color:var(--fg);cursor:pointer}
[hidden]{display:none!important}
footer{position:fixed;left:0;right:0;bottom:0;padding:12px 16px;text-align:center;font-size:12px;color:var(--mut);pointer-events:none}
footer svg{vertical-align:-2px;margin-right:4px}
.ic{position:relative;width:64px;height:64px;margin:0 auto;display:flex;align-items:center;justify-content:center}
.ic svg{position:relative;z-index:1;overflow:visible}
.ring{position:absolute;inset:6px;border-radius:50%;border:1.5px solid var(--ac);opacity:0;animation:zr 2s ease-out infinite}
.sh{opacity:.35}
.tr{stroke-dasharray:.22 .78;animation:zt 1.6s linear infinite}
.ck{stroke-dasharray:1;stroke-dashoffset:1;opacity:.35;animation:zb 1.6s ease-in-out infinite}
h1 .t{background:linear-gradient(90deg,var(--fg) 0%,var(--fg) 40%,var(--mut) 50%,var(--fg) 60%,var(--fg) 100%);background-size:250% 100%;-webkit-background-clip:text;background-clip:text;color:transparent;animation:zs 2.2s linear infinite}
h1 .d{display:inline-flex;gap:3px;margin-left:4px;vertical-align:middle}
h1 .d i{width:4px;height:4px;border-radius:50%;background:var(--fg);opacity:.25;animation:zd 1.2s ease-in-out infinite}
h1 .d i:nth-child(2){animation-delay:.2s}h1 .d i:nth-child(3){animation-delay:.4s}
main.ok .tr,main.ok .ring,main.ok h1 .d{animation:none;opacity:0}
main.ok .sh{opacity:1;transition:opacity .3s}
main.ok .ck{animation:zc .45s ease-out forwards;opacity:1}
main.ok h1 .t{animation:none;color:var(--fg)}
main.err .tr,main.err .ring,main.err .ck,main.err h1 .t,main.err h1 .d i{animation:none}
main.err h1 .t{color:var(--fg)}
main.err .ck{opacity:0}
@keyframes zr{0%{transform:scale(.7);opacity:.5}100%{transform:scale(1.35);opacity:0}}
@keyframes zt{to{stroke-dashoffset:-1}}
@keyframes zb{0%,100%{opacity:.2}50%{opacity:.55}}
@keyframes zc{from{stroke-dashoffset:1}to{stroke-dashoffset:0}}
@keyframes zs{from{background-position:100% 0}to{background-position:-150% 0}}
@keyframes zd{0%,100%{opacity:.25;transform:translateY(0)}50%{opacity:1;transform:translateY(-3px)}}
@media (prefers-reduced-motion:reduce){.ring,.tr,.ck,h1 .t,h1 .d i{animation:none!important}.tr{opacity:0}.ck{stroke-dashoffset:0;opacity:.6}h1 .t{color:var(--fg)}}
</style>
</head>
<body>
<main id="zv" data-t="{{.Token}}" data-b="{{.Bits}}" data-c="{{.Nonce2}}" data-n="{{.Next}}" data-e="{{.T.Error}}" data-h="{{.T.NeedHTTPS}}">
{{.Icon}}
<h1><span class="t">{{.T.Heading}}</span><span class="d" aria-hidden="true"><i></i><i></i><i></i></span></h1>
<p id="msg">{{.T.Checking}}</p>
<noscript><p>{{.T.Noscript}}</p></noscript>
<div class="bar" role="progressbar" aria-label="{{.T.Progress}}" aria-valuemin="0" aria-valuemax="100"><i id="pb"></i></div>
<button id="rt" type="button" hidden>{{.T.Retry}}</button>
</main>
<footer><svg viewBox="0 0 24 24" width="13" height="13" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true" focusable="false"><path d="M12 3 4.5 6v5.5c0 4.4 3.1 8.3 7.5 9.5 4.4-1.2 7.5-5.1 7.5-9.5V6z"/></svg>{{.T.Footer}}</footer>
<script nonce="{{.Nonce}}">
(function(){
var el=document.getElementById('zv'),msg=document.getElementById('msg'),pb=document.getElementById('pb'),rt=document.getElementById('rt');
var tok=el.dataset.t,bits=+el.dataset.b,nonce=el.dataset.c,next=el.dataset.n;
function fail(t){el.classList.remove('ok');el.classList.add('err');msg.textContent=t;rt.hidden=false;pb.style.width='0'}
rt.addEventListener('click',function(){location.reload()});
if(!(window.crypto&&crypto.subtle&&window.TextEncoder)){fail(el.dataset.h);return}
var enc=new TextEncoder();
function good(h){var f=bits>>3,r=bits&7,i;for(i=0;i<f;i++){if(h[i]!==0)return false}
if(r&&(h[f]>>(8-r))!==0)return false;return true}
async function run(){
var c=0,B=512,exp=Math.pow(2,bits),found=-1,i;
while(found<0){
var ps=[];for(i=0;i<B;i++){ps.push(crypto.subtle.digest('SHA-256',enc.encode(nonce+(c+i))))}
var rs=await Promise.all(ps);
for(i=0;i<B;i++){if(good(new Uint8Array(rs[i]))){found=c+i;break}}
if(found<0){c+=B;pb.style.width=Math.min(95,Math.round(100*(1-Math.exp(-c/exp))))+'%';await new Promise(function(r){setTimeout(r,0)})}
}
pb.style.width='100%';el.classList.add('ok');
var res=await fetch('/.zorven/challenge',{method:'POST',credentials:'same-origin',headers:{'Content-Type':'application/json'},body:JSON.stringify({token:tok,counter:String(found),next:next})});
if(!res.ok){fail(el.dataset.e);return}
var j=await res.json();
var to=(j&&typeof j.next==='string'&&j.next.charAt(0)==='/'&&j.next.charAt(1)!=='/'&&j.next.charAt(1)!=='\\')?j.next:'/';
location.replace(to);
}
run().catch(function(){fail(el.dataset.e)});
})();
</script>
</body>
</html>
`))

type pageData struct {
	Lang, Nonce, Nonce2, Token, Next string
	Bits                             int
	T                                pageText
	Icon                             template.HTML
}

// RenderPage, PoW sorgu sayfasini yazar (HTTP 403, no-store, CSP nonce'li).
// token: imzali sorgu jetonu, nonce: PoW girdisi, next: sayfa yolu (SafeNext'ten gecer).
func RenderPage(w http.ResponseWriter, r *http.Request, token, nonce string, bits int, next string) {
	lang := PickLang(r)
	nb := make([]byte, 16)
	_, _ = rand.Read(nb)
	cspNonce := b64(nb)
	h := w.Header()
	h.Set("Content-Type", "text/html; charset=utf-8")
	h.Set("Cache-Control", "no-store")
	h.Set("X-Zorven-Challenge", "required")
	h.Set("X-Content-Type-Options", "nosniff")
	h.Set("Referrer-Policy", "no-referrer")
	h.Set("Content-Security-Policy", "default-src 'none'; script-src 'nonce-"+cspNonce+"'; style-src 'nonce-"+cspNonce+
		"'; connect-src 'self'; base-uri 'none'; form-action 'none'; frame-ancestors 'none'")
	h.Set("Vary", "Accept, Accept-Language")
	w.WriteHeader(http.StatusForbidden)
	if r.Method == http.MethodHead {
		return
	}
	_ = pageTmpl.Execute(io.Writer(w), pageData{
		Lang: lang, Nonce: cspNonce, Nonce2: nonce, Token: token, Next: SafeNext(next), Bits: bits,
		T: pageTexts[lang], Icon: template.HTML(shieldIcon),
	})
}

// WriteRequired, sayfa gosterilemeyen istekler (XHR, POST, Accept html degil) icin
// kisa JSON 403 + X-Zorven-Challenge: required yazar.
func WriteRequired(w http.ResponseWriter) {
	h := w.Header()
	h.Set("Content-Type", "application/json; charset=utf-8")
	h.Set("Cache-Control", "no-store")
	h.Set("X-Zorven-Challenge", "required")
	w.WriteHeader(http.StatusForbidden)
	_, _ = w.Write([]byte(`{"error":{"code":"challenge_required","message":"Browser verification required. Open this page in a web browser."}}` + "\n"))
}
