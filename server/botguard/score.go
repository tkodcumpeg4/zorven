package botguard

import (
	"net/http"
	"strings"

	"github.com/tkodcumpeg4/zorven/server/tunsec"
)

// SuspiciousThreshold, bot.mode=suspicious icin sorgu esigi. Skor >= esik ise
// ziyaretci sorgulanir.
//
// AGIRLIKLAR (yanlis pozitif dusuk tutulur: tek basina zayif sinyal esige
// ulasmaz; "gercek" Chrome/Firefox/Safari basliklari 0 puan alir):
//
//	otomasyon/headless/kutuphane UA            6  (tek basina yeter)
//	tarayici tarayici UA'si degil ama bot/spider/crawler sozcugu   6
//	dogrulanamayan "Googlebot" vb. iddiasi    6  (ingress ekler: ScoreFakeBot)
//	tarayici UA'si + TLS'de Chromium GREASE'i yok   4
//	tarayici UA'si + Accept-Language yok (HTML gezinti)   3
//	tarayici UA'si + Accept yok               3
//	tarayici UA'si + TLS ALPN yok             3
//	tarayici UA'si + < 8 sifre takimi         3
//	HTTP/1.0                                  3
//	tarayici UA'si + TLS 1.3 yok              2
//
// Bos UA tek basina 10 (esigin uzerinde; ayrica block_empty_ua 403 verir).
const SuspiciousThreshold = 5

// Sinyal agirliklari.
const (
	wEmptyUA      = 10
	wAutomationUA = 6
	wScannerUA    = 6
	wBotWord      = 6
	wNoGrease     = 4
	wNoLang       = 3
	wNoAccept     = 3
	wNoALPN       = 3
	wFewCiphers   = 3
	wHTTP10       = 3
	wNoTLS13      = 2
	// ScoreFakeBot, UA'sinda iyi bot iddiasi olup dogrulanamayan istek puani.
	ScoreFakeBot = 6
)

// automationUATokens, otomasyon/kutuphane/headless istemci sozcukleri (kucuk harf).
// Tarayici tabanli degil; HTTP kutuphaneleri ve headless tarayicilar.
// okhttp / Dart gibi mobil uygulama kutuphaneleri BILEREK yok (API trafigi icin
// exclude_paths kullanilir; yanlis pozitif riski).
var automationUATokens = []string{
	"headlesschrome", "phantomjs", "slimerjs", "python-requests", "python-urllib", "python/", "aiohttp", "httpx",
	"curl/", "wget/", "go-http-client", "axios/", "node-fetch", "node-superagent", "undici", "scrapy",
	"java/", "apache-httpclient", "libwww-perl", "lwp::", "mechanize", "selenium", "puppeteer", "playwright",
	"webdriver", "httrack", "guzzlehttp", "postmanruntime", "restsharp", "ruby", "perl/", "php/", "colly",
}

// botWordTokens, genel "bot" sozcukleri. Dogrulanmis iyi botlar ingress'te once
// ayiklanir; geri kalan crawler'lar supheli sayilir.
var botWordTokens = []string{"bot/", "bot;", "bot)", "spider", "crawler", "crawl/", "slurp", "scraper", "scan/", "fetcher"}

// Result, skor ve nedenleri.
type Result struct {
	Score   int
	Signals []string
}

func (r *Result) add(w int, sig string) {
	r.Score += w
	r.Signals = append(r.Signals, sig)
}

// ClaimsBrowser, UA'nin tarayici gibi gorunmeyi iddia ettigi (Mozilla/5.0 ...).
func ClaimsBrowser(ua string) bool {
	l := strings.ToLower(ua)
	return strings.HasPrefix(l, "mozilla/") && !hasAny(l, botWordTokens) && !hasAny(l, automationUATokens)
}

func hasAny(l string, toks []string) bool {
	for _, t := range toks {
		if strings.Contains(l, t) {
			return true
		}
	}
	return false
}

// isNavigation, istegin tarayici sayfa gezintisi olup olmadigi: GET/HEAD ve
// (Sec-Fetch-Dest: document | Sec-Fetch-Mode: navigate | Accept text/html | Accept yok).
func isNavigation(r *http.Request) bool {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		return false
	}
	if r.Header.Get("Sec-Fetch-Dest") == "document" || r.Header.Get("Sec-Fetch-Mode") == "navigate" {
		return true
	}
	acc := strings.ToLower(r.Header.Get("Accept"))
	return acc == "" || strings.Contains(acc, "text/html")
}

// Score, istegin supheli skorunu hesaplar. fp nil ise (TLS yok / parmak izi
// yakalanamadi / sure doldu) TLS sinyalleri atlanir.
func Score(r *http.Request, fp *TLSFingerprint) Result {
	var res Result
	ua := strings.TrimSpace(r.Header.Get("User-Agent"))
	if ua == "" {
		res.add(wEmptyUA, "empty-ua")
		return res
	}
	l := strings.ToLower(ua)
	if hasAny(l, automationUATokens) {
		res.add(wAutomationUA, "automation-ua")
	}
	if hasAny(l, tunsec.ScannerUATokens) {
		res.add(wScannerUA, "scanner-ua")
	}
	if hasAny(l, botWordTokens) {
		res.add(wBotWord, "bot-word")
	}
	if r.ProtoMajor == 1 && r.ProtoMinor == 0 {
		res.add(wHTTP10, "http1.0")
	}
	if !ClaimsBrowser(ua) {
		return res
	}
	// Buradan sonrasi yalniz "tarayiciyim" diyen istekler icin.
	if isNavigation(r) {
		if strings.TrimSpace(r.Header.Get("Accept-Language")) == "" {
			res.add(wNoLang, "no-accept-language")
		}
		if strings.TrimSpace(r.Header.Get("Accept")) == "" {
			res.add(wNoAccept, "no-accept")
		}
	}
	if fp != nil {
		if len(fp.ALPN) == 0 {
			res.add(wNoALPN, "tls-no-alpn")
		}
		if fp.CipherCount < 8 {
			res.add(wFewCiphers, "tls-few-ciphers")
		}
		if !fp.HasTLS13 {
			res.add(wNoTLS13, "tls-no-tls13")
		}
		// Chromium ailesi (Chrome, Edge, Opera, CriOS) her zaman GREASE gonderir;
		// Firefox ve Safari'de bu kontrol yapilmaz (Firefox GREASE kullanmaz).
		if (strings.Contains(l, "chrome/") || strings.Contains(l, "crios/") || strings.Contains(l, "chromium/")) &&
			!strings.Contains(l, "firefox/") && !fp.HasGREASE {
			res.add(wNoGrease, "tls-no-grease")
		}
	}
	return res
}

// Suspicious, skor esigi asti mi.
func (r Result) Suspicious() bool { return r.Score >= SuspiciousThreshold }
