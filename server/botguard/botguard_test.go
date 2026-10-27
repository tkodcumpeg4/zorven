package botguard

import (
	"context"
	"crypto/tls"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

var t0 = time.Date(2026, 10, 27, 12, 0, 0, 0, time.UTC)

const ua = "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/130.0.0.0 Safari/537.36"

func TestCookie(t *testing.T) {
	k := NewKeys([]byte("master"))
	v, _ := k.IssueCookie("tun_1", "203.0.113.7", ua, t0, time.Hour)
	if !k.VerifyCookie(v, "tun_1", "203.0.113.7", ua, t0.Add(time.Minute)) {
		t.Fatal("gecerli cerez reddedildi")
	}
	// ayni /24'teki baska IP gecer, farkli /24 gecmez
	if !k.VerifyCookie(v, "tun_1", "203.0.113.200", ua, t0) {
		t.Fatal("ayni /24 gecmeli")
	}
	if k.VerifyCookie(v, "tun_1", "203.0.114.7", ua, t0) {
		t.Fatal("farkli /24 gecmemeli")
	}
	if k.VerifyCookie(v, "tun_2", "203.0.113.7", ua, t0) {
		t.Fatal("baska tunel gecmemeli")
	}
	if k.VerifyCookie(v, "tun_1", "203.0.113.7", "curl/8", t0) {
		t.Fatal("baska UA gecmemeli")
	}
	if k.VerifyCookie(v, "tun_1", "203.0.113.7", ua, t0.Add(time.Hour+time.Second)) {
		t.Fatal("suresi dolan gecmemeli")
	}
	// kurcalama
	p, mac, _ := strings.Cut(v, ".")
	for _, bad := range []string{p + "." + mac[:len(mac)-2] + "AA", "x" + v, p, "", p + ".", "." + mac, p + "A." + mac} {
		if k.VerifyCookie(bad, "tun_1", "203.0.113.7", ua, t0) {
			t.Fatalf("kurcalanmis cerez kabul edildi: %q", bad)
		}
	}
	// baska ana anahtar
	if NewKeys([]byte("other")).VerifyCookie(v, "tun_1", "203.0.113.7", ua, t0) {
		t.Fatal("baska anahtar gecmemeli")
	}
	// sorgu jetonu cerez olarak kabul edilmemeli (amac ayrimi)
	tok, _ := k.NewChallenge("tun_1", "203.0.113.7", 12, t0)
	if k.VerifyCookie(tok, "tun_1", "203.0.113.7", ua, t0) {
		t.Fatal("sorgu jetonu cerez sayildi")
	}
}

func TestIPv6Prefix(t *testing.T) {
	if IPPrefix("2001:db8:1:2:aaaa::1") != IPPrefix("2001:db8:1:2:bbbb::9") {
		t.Fatal("ayni /64 ayni onek olmali")
	}
	if IPPrefix("2001:db8:1:3::1") == IPPrefix("2001:db8:1:2::1") {
		t.Fatal("farkli /64 ayri olmali")
	}
	if IPPrefix("::ffff:203.0.113.9") != IPPrefix("203.0.113.9") {
		t.Fatal("v4-mapped birlesmeli")
	}
}

func TestPoW(t *testing.T) {
	k := NewKeys([]byte("m"))
	tok, nonce := k.NewChallenge("tun_1", "198.51.100.4", 12, t0)
	n, bits, exp, err := k.ParseChallenge(tok, "tun_1", "198.51.100.4", 12, t0.Add(time.Second))
	if err != nil || n != nonce || bits != 12 {
		t.Fatalf("parse: %v", err)
	}
	ctr := SolvePoW(nonce, 12)
	if !CheckPoW(nonce, ctr, 12) {
		t.Fatal("dogru cozum reddedildi")
	}
	// yanlis sayac
	bad := 0
	for i := 0; i < 50; i++ {
		if CheckPoW(nonce, "9999999"+string(rune('0'+i%10)), 20) {
			bad++
		}
	}
	if bad > 0 {
		t.Fatal("rastgele sayac 20 bitte gecti")
	}
	for _, c := range []string{"", "x", "-1", "01", strings.Repeat("1", 21), "1.5"} {
		if CheckPoW(nonce, c, 0) {
			t.Fatalf("gecersiz sayac kabul edildi %q", c)
		}
	}
	// replay
	r := NewReplay(10)
	if !r.Use(nonce, exp, t0) || r.Use(nonce, exp, t0.Add(time.Second)) {
		t.Fatal("nonce tek kullanimlik olmali")
	}
	if !r.Use(nonce, exp, exp.Add(time.Second)) {
		t.Fatal("suresi dolan nonce yeniden kullanilabilir (jeton zaten gecersiz)")
	}
	// sure
	if _, _, _, err := k.ParseChallenge(tok, "tun_1", "198.51.100.4", 12, t0.Add(ChallengeLifetime+time.Second)); !errors.Is(err, ErrExpired) {
		t.Fatalf("sure: %v", err)
	}
	// zorluk: tunel ayari yukselmis
	if _, _, _, err := k.ParseChallenge(tok, "tun_1", "198.51.100.4", 18, t0); !errors.Is(err, ErrDifficulty) {
		t.Fatalf("zorluk: %v", err)
	}
	// baska tunel / IP / sahte imza
	if _, _, _, err := k.ParseChallenge(tok, "tun_2", "198.51.100.4", 12, t0); err == nil {
		t.Fatal("baska tunel")
	}
	if _, _, _, err := k.ParseChallenge(tok, "tun_1", "198.51.101.4", 12, t0); err == nil {
		t.Fatal("baska IP onegi")
	}
	if _, _, _, err := k.ParseChallenge(tok+"x", "tun_1", "198.51.100.4", 12, t0); err == nil {
		t.Fatal("kurcalanmis jeton")
	}
	// cerez jetonu sorgu olarak kabul edilmemeli
	cv, _ := k.IssueCookie("tun_1", "198.51.100.4", ua, t0, time.Hour)
	if _, _, _, err := k.ParseChallenge(cv, "tun_1", "198.51.100.4", 1, t0); err == nil {
		t.Fatal("cerez sorgu sayildi")
	}
}

func TestReplayCap(t *testing.T) {
	r := NewReplay(3)
	exp := t0.Add(time.Minute)
	for i := 0; i < 3; i++ {
		if !r.Use(string(rune('a'+i)), exp, t0) {
			t.Fatal("dolmadan reddetti")
		}
	}
	if r.Use("z", exp, t0) {
		t.Fatal("dolu onbellek fail-closed olmali")
	}
	if !r.Use("z", exp, exp.Add(time.Second)) || r.Len() != 1 {
		t.Fatalf("suresi dolanlar temizlenmeli: %d", r.Len())
	}
}

func TestSafeNext(t *testing.T) {
	ok := map[string]string{"/": "/", "/a/b?x=1&y=2": "/a/b?x=1&y=2", "/p#f": "/p#f"}
	for in, want := range ok {
		if SafeNext(in) != want {
			t.Errorf("SafeNext(%q)=%q", in, SafeNext(in))
		}
	}
	for _, in := range []string{"", "//evil.com", "/\\evil.com", "https://evil.com", "evil.com", "/a\r\nSet-Cookie: x", "/.zorven/challenge", "/a\\b", strings.Repeat("/a", 2000)} {
		if SafeNext(in) != "/" {
			t.Errorf("SafeNext(%q) = %q, / bekleniyordu", in, SafeNext(in))
		}
	}
}

func TestLimiter(t *testing.T) {
	now := t0
	l := NewLimiter(1000)
	l.SetClock(func() time.Time { return now })
	win := 10 * time.Second
	// patlama: 5 istek hemen gecer, 6. reddedilir
	for i := 0; i < 5; i++ {
		if ok, _ := l.Allow("k", 5, win, 1); !ok {
			t.Fatalf("patlama %d reddedildi", i)
		}
	}
	ok, wait := l.Allow("k", 5, win, 1)
	if ok || wait < time.Second {
		t.Fatalf("6. istek gecti ya da Retry-After yok: %v %v", ok, wait)
	}
	// dolum: 5 istek/10 sn = 0.5/sn -> 2 sn sonra 1 hak
	now = now.Add(2 * time.Second)
	if ok, _ := l.Allow("k", 5, win, 1); !ok {
		t.Fatal("dolum sonrasi gecmeli")
	}
	if ok, _ := l.Allow("k", 5, win, 1); ok {
		t.Fatal("tek hak dolmustu")
	}
	// farkli anahtar bagimsiz
	if ok, _ := l.Allow("other", 5, win, 1); !ok {
		t.Fatal("baska anahtar etkilenmemeli")
	}
	// 3x carpan
	for i := 0; i < 15; i++ {
		if ok, _ := l.Allow("m", 5, win, 3); !ok {
			t.Fatalf("3x kapasite %d", i)
		}
	}
	if ok, _ := l.Allow("m", 5, win, 3); ok {
		t.Fatal("3x kapasite asildi")
	}
}

func TestLimiterIPv6Aggregation(t *testing.T) {
	l := NewLimiter(1000)
	l.SetClock(func() time.Time { return t0 })
	key := func(ip string) string { return "t|" + IPPrefix(ip) }
	for i := 0; i < 3; i++ {
		if ok, _ := l.Allow(key("2001:db8:0:1::"+string(rune('1'+i))), 3, time.Minute, 1); !ok {
			t.Fatal("ayni /64 icin ilk 3 gecmeli")
		}
	}
	if ok, _ := l.Allow(key("2001:db8:0:1:ffff::9"), 3, time.Minute, 1); ok {
		t.Fatal("ayni /64'teki farkli adres ayni kovayi paylasmali")
	}
	if ok, _ := l.Allow(key("2001:db8:0:2::1"), 3, time.Minute, 1); !ok {
		t.Fatal("baska /64 ayri kova")
	}
}

func TestLimiterMemoryCapAndConcurrency(t *testing.T) {
	l := NewLimiter(640) // parca basina 10
	var wg sync.WaitGroup
	for g := 0; g < 8; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			for i := 0; i < 2000; i++ {
				l.Allow(string(rune('a'+g))+"-"+time.Duration(i).String(), 5, time.Minute, 1)
			}
		}(g)
	}
	wg.Wait()
	if n := l.Len(); n > 640 {
		t.Fatalf("bellek siniri asildi: %d", n)
	}
}

func req(hdr ...string) *http.Request {
	r := httptest.NewRequest("GET", "https://example.com/", nil)
	r.Header.Del("User-Agent")
	for i := 0; i+1 < len(hdr); i += 2 {
		r.Header.Set(hdr[i], hdr[i+1])
	}
	return r
}

// Gercek tarayicilar: ASLA supheli olmamali.
func TestScoreRealBrowsersPass(t *testing.T) {
	chromeFP := &TLSFingerprint{CipherCount: 15, HasGREASE: true, HasTLS13: true, ALPN: []string{"h2", "http/1.1"}}
	firefoxFP := &TLSFingerprint{CipherCount: 17, HasGREASE: false, HasTLS13: true, ALPN: []string{"h2", "http/1.1"}}
	safariFP := &TLSFingerprint{CipherCount: 20, HasGREASE: true, HasTLS13: true, ALPN: []string{"h2", "http/1.1"}}
	cases := []struct {
		name string
		ua   string
		fp   *TLSFingerprint
	}{
		{"chrome-win", ua, chromeFP},
		{"edge", ua + " Edg/130.0.0.0", chromeFP},
		{"firefox", "Mozilla/5.0 (Windows NT 10.0; Win64; x64; rv:131.0) Gecko/20100101 Firefox/131.0", firefoxFP},
		{"safari-mac", "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/17.5 Safari/605.1.15", safariFP},
		{"safari-ios", "Mozilla/5.0 (iPhone; CPU iPhone OS 17_5 like Mac OS X) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/17.5 Mobile/15E148 Safari/604.1", safariFP},
		{"chrome-android", "Mozilla/5.0 (Linux; Android 14; Pixel 8) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/130.0.0.0 Mobile Safari/537.36", chromeFP},
		{"chrome-ios", "Mozilla/5.0 (iPhone; CPU iPhone OS 17_5 like Mac OS X) AppleWebKit/605.1.15 (KHTML, like Gecko) CriOS/130.0.0.0 Mobile/15E148 Safari/604.1", safariFP},
		{"no-tls-info", ua, nil},
	}
	for _, c := range cases {
		r := req("User-Agent", c.ua, "Accept", "text/html,application/xhtml+xml,application/xml;q=0.9,*/*;q=0.8",
			"Accept-Language", "en-US,en;q=0.9", "Sec-Fetch-Mode", "navigate", "Sec-Fetch-Dest", "document")
		r.Proto, r.ProtoMajor, r.ProtoMinor = "HTTP/2.0", 2, 0
		if res := Score(r, c.fp); res.Suspicious() || res.Score != 0 {
			t.Errorf("%s: gercek tarayici puan aldi: %+v", c.name, res)
		}
	}
	// Eksik Accept-Language tek basina esigi asmaz (gizlilik eklentileri).
	r := req("User-Agent", ua, "Accept", "text/html")
	if Score(r, nil).Suspicious() {
		t.Error("tek basina Accept-Language eksigi supheli olmamali")
	}
	// XHR/fetch (Accept */*) Accept-Language eksigi sayilmaz.
	r = req("User-Agent", ua, "Accept", "application/json", "Sec-Fetch-Mode", "cors")
	if Score(r, chromeFP).Score != 0 {
		t.Error("XHR puan almamali")
	}
}

func TestScoreBotsSuspicious(t *testing.T) {
	good := "text/html,application/xhtml+xml;q=0.9,*/*;q=0.8"
	cases := map[string]*http.Request{
		"empty-ua":    req("Accept", good),
		"curl":        req("User-Agent", "curl/8.5.0", "Accept", "*/*"),
		"wget":        req("User-Agent", "Wget/1.21.4"),
		"python":      req("User-Agent", "python-requests/2.31.0", "Accept", "*/*"),
		"go":          req("User-Agent", "Go-http-client/2.0"),
		"axios":       req("User-Agent", "axios/1.6.0"),
		"node-fetch":  req("User-Agent", "node-fetch/1.0 (+https://github.com/bitinn/node-fetch)"),
		"scrapy":      req("User-Agent", "Scrapy/2.11 (+https://scrapy.org)"),
		"headless":    req("User-Agent", "Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/537.36 (KHTML, like Gecko) HeadlessChrome/130.0.0.0 Safari/537.36", "Accept", good, "Accept-Language", "en"),
		"phantom":     req("User-Agent", "Mozilla/5.0 (Unknown; Linux x86_64) AppleWebKit/538.1 (KHTML, like Gecko) PhantomJS/2.1.1 Safari/538.1"),
		"sqlmap":      req("User-Agent", "sqlmap/1.7", "Accept", good),
		"crawler":     req("User-Agent", "Mozilla/5.0 (compatible; AhrefsBot/7.0; +http://ahrefs.com/robot/)", "Accept", good, "Accept-Language", "en"),
		"fake-chrome": req("User-Agent", ua), // Accept ve Accept-Language yok: 3+3
	}
	for name, r := range cases {
		if res := Score(r, nil); !res.Suspicious() {
			t.Errorf("%s supheli sayilmadi: %+v", name, res)
		}
	}
	// HTTP/1.0 + Accept-Language yok
	r := req("User-Agent", ua, "Accept", good)
	r.ProtoMajor, r.ProtoMinor = 1, 0
	if !Score(r, nil).Suspicious() {
		t.Error("HTTP/1.0 + Accept-Language eksigi supheli olmali")
	}
	// Chrome UA'si, GREASE'siz ve ALPN'siz TLS (kutuphane taklidi) + dogru basliklar
	r = req("User-Agent", ua, "Accept", good, "Accept-Language", "en-US")
	lib := &TLSFingerprint{CipherCount: 5, HasTLS13: false, ALPN: nil}
	if res := Score(r, lib); !res.Suspicious() {
		t.Errorf("taklit TLS supheli olmali: %+v", res)
	}
	// Tek basina GREASE eksigi (Go TLS + dogru basliklar) esigin altinda kalir (dusuk yanlis pozitif tercihi).
	goTLS := &TLSFingerprint{CipherCount: 12, HasTLS13: true, ALPN: []string{"h2"}}
	if Score(r, goTLS).Suspicious() {
		t.Error("tek sinyal esigi asmamali")
	}
}

type fakeResolver struct {
	mu     sync.Mutex
	ptr    map[string][]string
	fwd    map[string][]string
	delay  time.Duration
	calls  int
	failIP map[string]bool
}

func (f *fakeResolver) LookupAddr(ctx context.Context, ip string) ([]string, error) {
	f.mu.Lock()
	f.calls++
	d := f.delay
	f.mu.Unlock()
	if d > 0 {
		select {
		case <-time.After(d):
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	if v, ok := f.ptr[ip]; ok {
		return v, nil
	}
	return nil, errors.New("nxdomain")
}
func (f *fakeResolver) LookupHost(ctx context.Context, h string) ([]string, error) {
	if v, ok := f.fwd[h]; ok {
		return v, nil
	}
	return nil, errors.New("nxdomain")
}

func TestVerifiedBots(t *testing.T) {
	res := &fakeResolver{
		ptr: map[string][]string{
			"66.249.66.1":  {"crawl-66-249-66-1.googlebot.com."},
			"203.0.113.9":  {"crawl-1.googlebot.com."}, // ileri DNS baska IP -> sahte PTR
			"198.51.100.5": {"evil.example.com."},      // sonek uymuyor
			"157.55.39.1":  {"msnbot-157-55-39-1.search.msn.com."},
			"17.58.0.1":    {"17-58-0-1.applebot.apple.com."},
			"2001:db8::7":  {"x.yandex.ru."},
			"66.249.66.99": {"crawl.notgooglebot.com."}, // ".googlebot.com" sonegi degil
		},
		fwd: map[string][]string{
			"crawl-66-249-66-1.googlebot.com":   {"66.249.66.1"},
			"crawl-1.googlebot.com":             {"203.0.113.77"},
			"msnbot-157-55-39-1.search.msn.com": {"157.55.39.1"},
			"17-58-0-1.applebot.apple.com":      {"17.58.0.1"},
			"x.yandex.ru":                       {"2001:db8:0:0:0:0:0:7"},
			"crawl.notgooglebot.com":            {"66.249.66.99"},
		},
	}
	v := NewVerifiedBots(res)
	v.Wait = time.Second
	gb := "Mozilla/5.0 (compatible; Googlebot/2.1; +http://www.google.com/bot.html)"
	if ok, claimed := v.Verify("66.249.66.1", gb); !ok || !claimed {
		t.Fatal("gercek Googlebot dogrulanmali")
	}
	if ok, claimed := v.Verify("203.0.113.9", gb); ok || !claimed {
		t.Fatal("ileri DNS tutmayan sahte")
	}
	if ok, _ := v.Verify("198.51.100.5", gb); ok {
		t.Fatal("sonek uyusmayan")
	}
	if ok, _ := v.Verify("66.249.66.99", gb); ok {
		t.Fatal("notgooglebot.com googlebot.com sayilmamali")
	}
	if ok, _ := v.Verify("157.55.39.1", "Mozilla/5.0 (compatible; bingbot/2.0)"); !ok {
		t.Fatal("bingbot")
	}
	if ok, _ := v.Verify("17.58.0.1", "Mozilla/5.0 (Macintosh) Applebot/0.1"); !ok {
		t.Fatal("applebot")
	}
	if ok, _ := v.Verify("2001:db8::7", "Mozilla/5.0 (compatible; YandexBot/3.0)"); !ok {
		t.Fatal("yandex IPv6")
	}
	// UA baska bot iddiasi: Googlebot IP'si Bingbot UA'siyla dogrulanmaz
	if ok, _ := v.Verify("66.249.66.1", "Mozilla/5.0 (compatible; bingbot/2.0)"); ok {
		t.Fatal("bot adi IP ile uyusmali")
	}
	// bot iddiasi yok -> claimed=false, DNS yok
	before := res.calls
	if ok, claimed := v.Verify("66.249.66.1", ua); ok || claimed || res.calls != before {
		t.Fatal("bot olmayan UA icin arama yapilmamali")
	}
	// onbellek: ikinci cagri DNS'e gitmez
	before = res.calls
	v.Verify("66.249.66.1", gb)
	if res.calls != before {
		t.Fatal("sonuc onbelleklenmeli")
	}
	// olumsuz onbellek
	v.Verify("198.51.100.5", gb)
	if res.calls != before {
		t.Fatal("olumsuz sonuc de onbelleklenmeli")
	}
}

func TestVerifiedBotsSlowDoesNotBlock(t *testing.T) {
	res := &fakeResolver{ptr: map[string][]string{"66.249.66.1": {"a.googlebot.com"}}, fwd: map[string][]string{"a.googlebot.com": {"66.249.66.1"}}, delay: 400 * time.Millisecond}
	v := NewVerifiedBots(res)
	v.Wait = 30 * time.Millisecond
	gb := "Googlebot/2.1"
	start := time.Now()
	if ok, claimed := v.Verify("66.249.66.1", gb); ok || !claimed {
		t.Fatal("bekleme suresi dolunca dogrulanmamis sayilmali")
	}
	if time.Since(start) > 200*time.Millisecond {
		t.Fatal("cagiran uzun bekletildi")
	}
	time.Sleep(600 * time.Millisecond)
	if ok, _ := v.Verify("66.249.66.1", gb); !ok {
		t.Fatal("arka plan sonucu onbellege yazilmali")
	}
}

func TestPickLang(t *testing.T) {
	cases := map[string]string{"": "en", "tr-TR,tr;q=0.9,en;q=0.8": "tr", "ja,en;q=0.5": "ja", "de,fr": "en", "en;q=0.5,ja;q=0.9": "ja", "ja;q=0": "en"}
	for in, want := range cases {
		r := httptest.NewRequest("GET", "/", nil)
		r.Header.Set("Accept-Language", in)
		if got := PickLang(r); got != want {
			t.Errorf("%q -> %q, %q bekleniyordu", in, got, want)
		}
	}
}

func TestRenderPage(t *testing.T) {
	for _, lang := range []string{"en", "tr", "ja"} {
		r := httptest.NewRequest("GET", "https://x/p?a=1", nil)
		r.Header.Set("Accept-Language", lang)
		r.Header.Set("Accept", "text/html")
		w := httptest.NewRecorder()
		RenderPage(w, r, "TOKEN.MAC", "NONCE", 18, "/p?a=1&b=<script>")
		body := w.Body.String()
		if w.Code != 403 || w.Header().Get("X-Zorven-Challenge") != "required" {
			t.Fatalf("durum/baslik: %d", w.Code)
		}
		csp := w.Header().Get("Content-Security-Policy")
		if !strings.Contains(csp, "nonce-") || strings.Contains(csp, "unsafe-inline") {
			t.Fatalf("CSP: %s", csp)
		}
		nonce := csp[strings.Index(csp, "nonce-")+6:]
		nonce = nonce[:strings.Index(nonce, "'")]
		if strings.Count(body, `nonce="`+nonce+`"`) != 2 {
			t.Fatal("script ve style nonce tasimali")
		}
		for _, must := range []string{"<noscript>", `lang="` + lang + `"`, "crypto.subtle", "/.zorven/challenge", "prefers-color-scheme", `data-t="TOKEN.MAC"`, `data-b="18"`} {
			if !strings.Contains(body, must) {
				t.Errorf("%s: %q eksik", lang, must)
			}
		}
		if strings.Contains(body, "<script>alert") || strings.Contains(body, "b=<script>") {
			t.Fatal("next kacirilmadi")
		}
		for _, ext := range []string{"http://", "https://", "src=", "href="} {
			if strings.Contains(body, ext) {
				t.Errorf("harici kaynak izi: %s", ext)
			}
		}
	}
	// HEAD govde yazmaz
	r := httptest.NewRequest("HEAD", "https://x/", nil)
	w := httptest.NewRecorder()
	RenderPage(w, r, "t", "n", 12, "/")
	if w.Body.Len() != 0 {
		t.Fatal("HEAD govde yazdi")
	}
}

func TestTLSFingerprintCapture(t *testing.T) {
	store := NewTLSFPStore()
	cert, err := selfSigned()
	if err != nil {
		t.Fatal(err)
	}
	ln, err := tls.Listen("tcp", "127.0.0.1:0", &tls.Config{
		Certificates: []tls.Certificate{cert},
		NextProtos:   []string{"h2", "http/1.1"},
		GetConfigForClient: func(h *tls.ClientHelloInfo) (*tls.Config, error) {
			store.Record(h)
			return nil, nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	got := make(chan string, 1)
	go func() {
		c, err := ln.Accept()
		if err != nil {
			return
		}
		defer c.Close()
		tc := c.(*tls.Conn)
		if tc.Handshake() == nil {
			got <- c.RemoteAddr().String()
		}
	}()
	cc, err := tls.Dial("tcp", ln.Addr().String(), &tls.Config{InsecureSkipVerify: true, NextProtos: []string{"h2"}})
	if err != nil {
		t.Fatal(err)
	}
	defer cc.Close()
	var remote string
	select {
	case remote = <-got:
	case <-time.After(3 * time.Second):
		t.Fatal("el sikisma tamamlanmadi")
	}
	fp := store.Get(remote)
	if fp == nil {
		t.Fatal("parmak izi yakalanmadi")
	}
	if !fp.HasTLS13 || len(fp.ALPN) != 1 || fp.ALPN[0] != "h2" || fp.CipherCount < 3 || fp.Summary == "" {
		t.Fatalf("parmak izi: %+v", fp)
	}
	store.Forget(remote)
	if store.Get(remote) != nil {
		t.Fatal("Forget calismadi")
	}
	var _ net.Conn
}
