package tunsec

// Tunel WAF'i (ZTNA Z3): OWASP CRS'nin kucuk, dusuk yanlis-pozitifli bir alt kumesi.
//
// TASARIM
//   - Taranan alanlar: yol, sorgu anahtar/degerleri (tek tek, URL-decode edilmis,
//     gerekirse iki kez), bazi basliklar (User-Agent tarayici imzasi icin; log4shell /
//     shellshock / PHP sarmalayicilari icin TUM basliklar). Govde varsayilan olarak
//     TARANMAZ; Config.WAF.Body ile ilk 8 KB (yalniz form/JSON/metin) taranir.
//   - Her kural kucuk harfe cevrilmis girdiye uygulanir (regex'ler kucuk harf yazilir).
//   - Her kuralin "kapi" (gate) alt dizgileri vardir: hicbiri yoksa regex hic
//     calistirilmaz. Normal bir istekte maliyet birkac mikrosaniyedir (bkz. benchmark).
//   - Degerler tek tek taranir; boylece "q=javascript: ogren" gibi sorgular,
//     anahtar=deger birlesimi yuzunden yanlis eslesmez.
//   - Hangi kuralin eslestigi YANITA yazilmaz; yalniz log/reqlog'a.

import (
	"bytes"
	"io"
	"mime"
	"net/http"
	"net/url"
	"regexp"
	"strings"
)

// Kategoriler.
const (
	CatSQLi      = "sqli"
	CatXSS       = "xss"
	CatTraversal = "traversal"
	CatProbe     = "probe"
	CatCmdi      = "cmdi"
	CatLog4Shell = "log4shell"
	CatPHP       = "php"
	CatSSRF      = "ssrf"
	CatJava      = "java"
	CatScanner   = "scanner"
	CatProtocol  = "protocol"
)

// Hedef bitleri: kural hangi alanlarda calisir.
type target uint8

const (
	tURL  target = 1 << iota // yol + sorgu anahtar/degerleri
	tBody                    // govde (opsiyonel)
	tUA                      // User-Agent
	tHdr                     // tum baslik degerleri
)

// BodyScanLimit, govde taramasinin ust siniri (bayt).
const BodyScanLimit = 8 << 10

// FieldHardLimit, tek alan bu boyu asarsa tarama yapilmadan "oversize-field" eslesmesi sayilir.
const FieldHardLimit = 64 << 10

// Hit, bir WAF eslesmesi.
type Hit struct {
	Rule     string // kural kimligi (or. "sqli-union")
	Category string
}

type rule struct {
	id, cat string
	tg      target
	gates   []string // kucuk harf; herhangi biri varsa regex calisir. Bos = her zaman
	re      *regexp.Regexp
}

func mk(id, cat string, tg target, gates []string, expr string) rule {
	return rule{id: id, cat: cat, tg: tg, gates: gates, re: regexp.MustCompile(expr)}
}

// ScannerUATokens, bilinen tarayici/saldiri araci User-Agent sozcukleri (kucuk harf).
// Bot korumasi (botguard) skorlamasi da ayni listeyi kullanir.
var ScannerUATokens = []string{"sqlmap", "nikto", "nmap", "masscan", "acunetix", "nessus", "dirbuster", "wpscan", "havij", "nuclei", "zgrab", "gobuster", "ffuf", "feroxbuster", "wfuzz", "openvas", "arachni", "w3af", "whatweb", "jaeles", "burp", "hydra", "metasploit"}

// rules, yerlesik kume. Paket yuklenirken BIR KEZ derlenir.
var rules = []rule{
	// --- SQL injection ---
	mk("sqli-union", CatSQLi, tURL|tBody, []string{"union"},
		`\bunion\b(\s|/\*.*?\*/)+(all(\s|/\*.*?\*/)+|distinct(\s|/\*.*?\*/)+)?select\b`),
	mk("sqli-tautology", CatSQLi, tURL|tBody, []string{"'", "\"", " or ", " and ", "||", "&&"},
		`['"]\s*(or|and|\|\||&&)\s*['"(]?\s*[\w.]+['")]?\s*(=|<|>|like\b)\s*['"(]?\s*[\w.]+|\b\d+\s+(or|and)\s+\d+\s*=\s*\d+\b`),
	mk("sqli-stacked", CatSQLi, tURL|tBody, []string{";"},
		`;\s*(drop|alter|truncate|create|insert\s+into|delete\s+from|update\s+\w+\s+set|exec|execute|shutdown|select)\b`),
	mk("sqli-comment", CatSQLi, tURL, []string{"'"},
		`'\s*(--|#|/\*)`),
	mk("sqli-time", CatSQLi, tURL|tBody, []string{"sleep", "benchmark", "waitfor"},
		`(select\s+|\bor\b\s*|\band\b\s*|;\s*|['")]\s*)(pg_)?sleep\s*\(\s*\d|\bbenchmark\s*\(\s*\d+\s*,|\bwaitfor\s+delay\b`),
	mk("sqli-meta", CatSQLi, tURL|tBody, []string{"information_schema", "sqlite_master", "pg_catalog", "sys.", "mysql.user"},
		`\binformation_schema\b|\bsqlite_master\b|\bpg_catalog\b|\bsys\.(tables|columns|objects)\b|\bmysql\.user\b`),
	mk("sqli-func", CatSQLi, tURL|tBody, []string{"load_file", "xp_cmdshell", "extractvalue", "updatexml", "dbms_pipe", "outfile", "dumpfile"},
		`\b(load_file|xp_cmdshell|extractvalue|updatexml|dbms_pipe\.\w+)\s*\(|\binto\s+(outfile|dumpfile)\b`),

	// --- XSS ---
	mk("xss-script", CatXSS, tURL|tBody, []string{"script"},
		`<\s*/?\s*script\b`),
	mk("xss-handler", CatXSS, tURL|tBody, []string{"<"},
		`<\s*[a-z][a-z0-9]*\b[^>]*?[\s/"'\x60]on[a-z]{3,}\s*=`),
	mk("xss-jsuri", CatXSS, tURL|tBody, []string{"javascript", "vbscript", "data:"},
		"(^|[\\s\"'=(])(javascript|vbscript)\\s*:\\s*[\\w.$]+\\s*[(\\[.]|data\\s*:\\s*text/html"),
	mk("xss-embed", CatXSS, tURL|tBody, []string{"<"},
		`<\s*(iframe|object|embed|frame|frameset|applet|meta\s+http-equiv|base\s+href|link\s+rel)\b`),
	mk("xss-dom", CatXSS, tURL|tBody, []string{"document", "window", "innerhtml"},
		`\bdocument\s*\.\s*(cookie|domain|write)\b|\bwindow\s*\.\s*location\s*=|\.innerhtml\s*=`),

	// --- Dizin gezinme ---
	mk("traversal-dotdot", CatTraversal, tURL|tBody, []string{".."},
		`(^|[/\\])\.\.([/\\]|$)`),
	mk("traversal-overlong", CatTraversal, tURL, []string{"%c0%af", "%c1%9c", "%e0%80%af", "%252e", "%2e%2e"},
		`%c0%af|%c1%9c|%e0%80%af|%252e%252e|%2e%2e(%2f|%5c)`),
	mk("traversal-files", CatTraversal, tURL|tBody, []string{"etc/", "proc/", ".ini", "windows"},
		`(^|/)etc/(passwd|shadow|group|hosts|sudoers)\b|/proc/(self|\d+)/(environ|cmdline|maps|fd)|\b(boot|win)\.ini\b|(^|[/\\])windows[/\\](system32|win\.ini)`),

	// --- Bilinen kesif yollari (yalniz URL yolu) ---
	mk("probe-paths", CatProbe, tURL, []string{"/."},
		`/\.(git|svn|hg|bzr)(/|$)|/\.env(\.[\w-]+)?(/|$)|/\.aws/|/\.ssh/|/\.htpasswd\b|/wp-config\.php\b|/vendor/phpunit/`),

	// --- Komut enjeksiyonu ---
	mk("cmdi-chain", CatCmdi, tURL|tBody, []string{";", "|", "&", "`"},
		"[;|&\\x60]\\s*(whoami|uname|printenv|ifconfig|ipconfig|netstat)\\b|[;|&\\x60]\\s*(cat|ls|rm|chmod|id|env|pwd)\\s+[-/~.$]|[;|&\\x60]\\s*(wget|curl|nc|ncat|netcat|bash|sh|zsh|python[23]?|perl|ruby|php|powershell|cmd|certutil|busybox|telnet|ping|nslookup)\\s"),
	mk("cmdi-subst", CatCmdi, tURL|tBody, []string{"$(", "`", "${ifs"},
		"\\$\\(\\s*(cat|ls|id|whoami|uname|wget|curl|nc|bash|sh|echo|sleep|ping|printenv|env|pwd)\\b|\\x60\\s*(cat|ls|id|whoami|uname|wget|curl|nc|bash|sh|echo|sleep|ping)\\b[^\\x60]*\\x60|\\$\\{ifs\\}"),
	mk("shellshock", CatCmdi, tHdr|tURL, []string{"()"},
		`\(\s*\)\s*\{[^}]*\}\s*;`),

	// --- Log4Shell ve sablon/ifade enjeksiyonu ---
	mk("log4shell", CatLog4Shell, tURL|tBody|tHdr, []string{"${", "$%7b", "%24%7b"},
		`\$\{\s*(jndi|lower|upper|env|sys|java|ctx|bundle|k8s|docker|date|main|spring|web)\s*:|\$\{\s*:+-`),

	// --- PHP ---
	mk("php-wrapper", CatPHP, tURL|tBody|tHdr, []string{"://", "<?"},
		`\b(php|phar|zip|expect)://|data://text|<\?(php|=)`),
	mk("php-eval", CatPHP, tURL|tBody, []string{"eval"},
		`\beval\s*\(\s*(base64_decode|gzinflate|str_rot13|\$_(get|post|request|cookie))`),

	// --- SSRF (bulut metadata) ---
	mk("ssrf-metadata", CatSSRF, tURL, []string{"169.254.", "metadata.", "100.100.100.200", "meta-data"},
		`169\.254\.169\.254|metadata\.google\.internal|100\.100\.100\.200|/latest/meta-data`),

	// --- Java / Spring / Struts ---
	mk("java-classloader", CatJava, tURL|tBody|tHdr, []string{"classloader", "class.module", "#"},
		`\bclass\.(module\.)?classloader\b|\(#\w+\s*=\s*@|[%$]\{\s*\(?\s*#\w+`),

	// --- Bilinen tarayici / saldiri araclari (User-Agent) ---
	mk("scanner-ua", CatScanner, tUA, ScannerUATokens,
		`\b(sqlmap|nikto|nmap|masscan|acunetix|nessus|dirbuster|wpscan|havij|nuclei|zgrab|gobuster|ffuf|feroxbuster|wfuzz|openvas|arachni|w3af|whatweb|jaeles|burpcollaborator|hydra|metasploit)\b`),
}

// protocol kurali (regex gerektirmez): null bayt.
const ruleNullByte = "protocol-nullbyte"
const ruleOversize = "oversize-field"

// RuleInfo, panel/dokuman icin kural ozeti.
type RuleInfo struct {
	ID       string `json:"id"`
	Category string `json:"category"`
}

// Rules, yerlesik kural listesi.
func Rules() []RuleInfo {
	out := make([]RuleInfo, 0, len(rules)+2)
	for _, r := range rules {
		out = append(out, RuleInfo{ID: r.id, Category: r.cat})
	}
	out = append(out, RuleInfo{ID: ruleNullByte, Category: CatProtocol}, RuleInfo{ID: ruleOversize, Category: CatProtocol})
	return out
}

// KnownRuleOrCategory, disabled_rules girdisi dogrulamasi.
func KnownRuleOrCategory(id string) bool {
	for _, r := range Rules() {
		if r.ID == id || r.Category == id {
			return true
		}
	}
	return false
}

func (c *Compiled) disabled(r *rule) bool {
	if len(c.off) == 0 {
		return false
	}
	return c.off[r.id] || c.off[r.cat]
}

func (c *Compiled) disabledID(id, cat string) bool {
	if len(c.off) == 0 {
		return false
	}
	return c.off[id] || c.off[cat]
}

// scanString, tek bir (kucuk harfli) alan degerini verilen hedef icin kurallardan gecirir.
func (c *Compiled) scanString(s string, tg target) (Hit, bool) {
	if s == "" {
		return Hit{}, false
	}
	low := strings.ToLower(s)
	for i := range rules {
		r := &rules[i]
		if r.tg&tg == 0 || c.disabled(r) {
			continue
		}
		if len(r.gates) > 0 {
			hit := false
			for _, g := range r.gates {
				if strings.Contains(low, g) {
					hit = true
					break
				}
			}
			if !hit {
				continue
			}
		}
		if r.re.MatchString(low) {
			return Hit{Rule: r.id, Category: r.cat}, true
		}
	}
	return Hit{}, false
}

// scanDecoded, degeri ham, bir ve iki kez URL-decode edilmis haliyle tarar.
func (c *Compiled) scanDecoded(raw string, tg target, plusAsSpace bool) (Hit, bool) {
	if len(raw) > FieldHardLimit {
		if c.disabledID(ruleOversize, CatProtocol) {
			return Hit{}, false
		}
		return Hit{Rule: ruleOversize, Category: CatProtocol}, true
	}
	if h, ok := c.scanString(raw, tg); ok {
		return h, true
	}
	if !strings.ContainsRune(raw, '%') && !(plusAsSpace && strings.ContainsRune(raw, '+')) {
		return Hit{}, false
	}
	cur := raw
	for pass := 0; pass < 2; pass++ {
		var dec string
		var err error
		if plusAsSpace {
			dec, err = url.QueryUnescape(cur)
		} else {
			dec, err = url.PathUnescape(cur)
		}
		if err != nil || dec == cur {
			break
		}
		if strings.IndexByte(dec, 0) >= 0 && !c.disabledID(ruleNullByte, CatProtocol) {
			return Hit{Rule: ruleNullByte, Category: CatProtocol}, true
		}
		if h, ok := c.scanString(dec, tg); ok {
			return h, true
		}
		if !strings.ContainsRune(dec, '%') {
			break
		}
		cur = dec
	}
	return Hit{}, false
}

// scanPairs, "a=b&c=d" bicimli diziyi anahtar/deger olarak tek tek tarar.
func (c *Compiled) scanPairs(q string, tg target) (Hit, bool) {
	for q != "" {
		var pair string
		if i := strings.IndexByte(q, '&'); i >= 0 {
			pair, q = q[:i], q[i+1:]
		} else {
			pair, q = q, ""
		}
		if pair == "" {
			continue
		}
		k, v, _ := strings.Cut(pair, "=")
		if h, ok := c.scanDecoded(k, tg, true); ok {
			return h, true
		}
		if h, ok := c.scanDecoded(v, tg, true); ok {
			return h, true
		}
	}
	return Hit{}, false
}

// ScanRequest, URL (yol + sorgu) ve basliklari tarar. Govdeye DOKUNMAZ.
func (c *Compiled) ScanRequest(r *http.Request) (Hit, bool) {
	if !c.WAFActive() {
		return Hit{}, false
	}
	if h, ok := c.scanDecoded(r.URL.Path, tURL, false); ok {
		return h, true
	}
	if q := r.URL.RawQuery; q != "" {
		if h, ok := c.scanPairs(q, tURL); ok {
			return h, true
		}
	}
	for name, vals := range r.Header {
		switch name {
		case "Authorization", "Proxy-Authorization":
			continue // tasiyici belirtecler (base64/JWT) taranmaz
		}
		for _, v := range vals {
			if len(v) > FieldHardLimit {
				if !c.disabledID(ruleOversize, CatProtocol) {
					return Hit{Rule: ruleOversize, Category: CatProtocol}, true
				}
				continue
			}
			tg := tHdr
			if name == "User-Agent" {
				tg |= tUA
			}
			if h, ok := c.scanDecoded(v, tg, false); ok {
				return h, true
			}
		}
	}
	return Hit{}, false
}

// bodyReadCloser, okunmus onbellegi + kalan govdeyi birlestirir.
type bodyReadCloser struct {
	io.Reader
	io.Closer
}

// ScanBody, WAF.Body aciksa govdenin ilk BodyScanLimit baytini tarar ve govdeyi
// akisa bozulmadan geri koyar. Yalniz BILINEN Content-Length > 0 ve metin/form/JSON
// icerik turunde okunur (boylece bilinmeyen uzunluktaki akislar bloklanmaz).
func (c *Compiled) ScanBody(r *http.Request) (Hit, bool) {
	if !c.WAFActive() || !c.Body || r.Body == nil || r.Body == http.NoBody || r.ContentLength <= 0 {
		return Hit{}, false
	}
	mt, _, _ := mime.ParseMediaType(r.Header.Get("Content-Type"))
	form := false
	switch mt {
	case "application/x-www-form-urlencoded":
		form = true
	case "application/json", "text/plain", "application/xml", "text/xml", "application/graphql":
	default:
		return Hit{}, false
	}
	n := int(min(r.ContentLength, int64(BodyScanLimit)))
	buf := make([]byte, n)
	got, err := io.ReadFull(r.Body, buf)
	buf = buf[:got]
	r.Body = bodyReadCloser{Reader: io.MultiReader(bytes.NewReader(buf), r.Body), Closer: r.Body}
	if err != nil && err != io.ErrUnexpectedEOF && err != io.EOF {
		return Hit{}, false
	}
	if got == 0 {
		return Hit{}, false
	}
	s := string(buf)
	if form {
		return c.scanPairs(s, tBody)
	}
	return c.scanDecoded(s, tBody, false)
}
