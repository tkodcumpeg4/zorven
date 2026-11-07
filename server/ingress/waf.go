package ingress

// WAF-lite (FAZ 1 Part 2 / F02).
//
// Basit kalip bloklama: SQL injection, XSS, dizin gezinme, komut enjeksiyonu ve
// bilinen tarayici (scanner) user-agent'lari.
//
// Tasarim sinirlari (bilincli):
//   - GOVDE TARANMAZ. Govde tamponlamak sicak yol kuralini bozar; yalnizca F05
//     webhook dogrulamasi govdeyi okur, o da sadece eslesen kuralda.
//   - Taranan alanlar: URL yolu, sorgu dizesi (ham + URL-decode edilmis) ve secili
//     basliklar. Saldirilar sik sik URL-encode edildigi icin ikisi de taranir.
//   - Alanlar TAMAMEN taranir (kesme yok; kesmek dolguyla atlatmaya izin verirdi).
//     CPU korumasi icin tek bir alan wafHardLimit'i (64 KB) asarsa taranmadan
//     403 ile reddedilir.
//   - Hangi kuralin eslestigi YANITA YAZILMAZ (bilgi sizdirmaz), yalnizca loglanir.

import (
	"net/http"
	"net/url"
	"regexp"
	"strings"
)

// wafHardLimit, tek bir alanin azami bayt boyu. Alan bunu asarsa istek
// reddedilir (wafOversizeRule); bunun altindaki her alan tamamen taranir.
const wafHardLimit = 64 << 10 // 64 KB

// wafOversizeRule, tarama sinirini asan alanlar icin loglanan sahte kural adi.
const wafOversizeRule = "oversize-field"

// wafScannedHeaders, taranacak baslik listesi. Tum basliklari taramak pahali ve
// gereksiz; saldiri yuku pratikte bu uclunde tasinir.
var wafScannedHeaders = []string{"User-Agent", "Referer", "Cookie"}

// owaspLite, yerlesik kural kumesi. Paket yuklenirken BIR KEZ derlenir.
var owaspLite = []*regexp.Regexp{
	// --- SQL injection ---
	regexp.MustCompile(`(?i)union\s+(all\s+)?select`),
	regexp.MustCompile(`(?i)'\s*or\s+'?1'?\s*=\s*'?1`),
	regexp.MustCompile(`(?i)\bor\s+1\s*=\s*1\b`),
	regexp.MustCompile(`(?i);\s*drop\s+table\b`),
	regexp.MustCompile(`(?i)\binformation_schema\b`),
	regexp.MustCompile(`(?i)\b(sleep|benchmark|pg_sleep)\s*\(`),
	regexp.MustCompile(`(?i)\bwaitfor\s+delay\b`),

	// --- XSS ---
	regexp.MustCompile(`(?i)<\s*script\b`),
	regexp.MustCompile(`(?i)<\s*iframe\b`),
	regexp.MustCompile(`(?i)javascript:`),
	regexp.MustCompile(`(?i)\bon(error|load|click|mouseover)\s*=`),
	regexp.MustCompile(`(?i)document\s*\.\s*cookie`),

	// --- Dizin gezinme (path traversal) ---
	regexp.MustCompile(`\.\.[/\\]`),
	regexp.MustCompile(`(?i)/etc/(passwd|shadow)\b`),
	regexp.MustCompile(`(?i)\bwin\.ini\b`),

	// --- Komut enjeksiyonu ---
	regexp.MustCompile(`(?i)[;|&]\s*(cat|wget|curl|nc|bash|sh|powershell)\s`),
	regexp.MustCompile(`\$\(.+\)`),

	// --- Bilinen tarayicilar (scanner) ---
	regexp.MustCompile(`(?i)\b(sqlmap|nikto|nmap|masscan|acunetix|nessus|dirbuster|wpscan|havij)\b`),
}

// wafHintSrc, yerlesik kurallar icin ucuz on-suzgec: kural ancak alanin kucuk
// harfli halinde LISTELENEN alt dizgelerden en az biri varsa calistirilir.
// Davranis DEGISMEZ: her liste, ilgili regex'in eslesebilmesi icin zorunlu olan
// alt dizgelerdir (bunlar yoksa regex de eslesmez). Yalnizca ASCII alanlara
// uygulanir (Go'nun (?i) kati katlamasi ASCII disi karakterleri de eslestirir;
// ASCII disi alanlar suzgecsiz taranir). Regex taramasi temiz bir istekte
// ~70 us'nin neredeyse tamamiydi.
var wafHintSrc = map[string][]string{
	`(?i)union\s+(all\s+)?select`:                         {"union"},
	`(?i)'\s*or\s+'?1'?\s*=\s*'?1`:                        {"'"},
	`(?i)\bor\s+1\s*=\s*1\b`:                              {"or"},
	`(?i);\s*drop\s+table\b`:                              {"drop"},
	`(?i)\binformation_schema\b`:                          {"information_schema"},
	`(?i)\b(sleep|benchmark|pg_sleep)\s*\(`:               {"sleep", "benchmark"},
	`(?i)\bwaitfor\s+delay\b`:                             {"waitfor"},
	`(?i)<\s*script\b`:                                    {"script"},
	`(?i)<\s*iframe\b`:                                    {"iframe"},
	`(?i)javascript:`:                                     {"javascript:"},
	`(?i)\bon(error|load|click|mouseover)\s*=`:            {"="},
	`(?i)document\s*\.\s*cookie`:                          {"cookie"},
	`\.\.[/\\]`:                                           {".."},
	`(?i)/etc/(passwd|shadow)\b`:                          {"/etc/"},
	`(?i)\bwin\.ini\b`:                                    {"win.ini"},
	`(?i)[;|&]\s*(cat|wget|curl|nc|bash|sh|powershell)\s`: {";", "|", "&"},
	`\$\(.+\)`: {"$("},
	`(?i)\b(sqlmap|nikto|nmap|masscan|acunetix|nessus|dirbuster|wpscan|havij)\b`: {"sqlmap", "nikto", "nmap", "masscan", "acunetix", "nessus", "dirbuster", "wpscan", "havij"},
}

// wafHints, derlenmis kural isaretcisi -> on-suzgec listesi.
var wafHints = func() map[*regexp.Regexp][]string {
	m := make(map[*regexp.Regexp][]string, len(owaspLite))
	for _, re := range owaspLite {
		if h, ok := wafHintSrc[re.String()]; ok {
			m[re] = h
		}
	}
	return m
}()

func isASCII(s string) bool {
	for i := 0; i < len(s); i++ {
		if s[i] >= 0x80 {
			return false
		}
	}
	return true
}

func hintHit(lower string, hints []string) bool {
	for _, h := range hints {
		if strings.Contains(lower, h) {
			return true
		}
	}
	return false
}

// wafRuleset, ad -> yerlesik kume. Bilinmeyen ad compileAction'da kurali dusurur.
func wafRuleset(name string) ([]*regexp.Regexp, bool) {
	switch strings.ToLower(strings.TrimSpace(name)) {
	case "", "owasp-lite":
		return owaspLite, true
	}
	return nil, false
}

// compileWAFPatterns, kurumun kendi ekledigi kaliplari derler.
// Gecersiz regex kurali DUSURUR (ok=false) — sessizce yok saymak, kullanicinin
// korundugunu sanmasina yol acardi.
func compileWAFPatterns(pats []string) ([]*regexp.Regexp, bool) {
	out := make([]*regexp.Regexp, 0, len(pats))
	for _, p := range pats {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		re, err := regexp.Compile(p)
		if err != nil {
			return nil, false
		}
		out = append(out, re)
	}
	return out, true
}

// wafScan, istegin taranacak alanlarini kurallardan gecirir.
// Eslesme varsa eslesme kaynagini (loglamak icin) ve true doner.
func wafScan(r *http.Request, rules []*regexp.Regexp) (string, bool) {
	if len(rules) == 0 {
		return "", false
	}

	fields := make([]string, 0, 3+len(wafScannedHeaders))
	fields = append(fields, r.URL.Path)
	if q := r.URL.RawQuery; q != "" {
		fields = append(fields, q)
		// URL-decode edilmis hali: saldirilar sik sik encode edilerek gecirilir.
		if dec, err := url.QueryUnescape(q); err == nil && dec != q {
			fields = append(fields, dec)
		}
	}
	for _, h := range wafScannedHeaders {
		if v := r.Header.Get(h); v != "" {
			fields = append(fields, v)
		}
	}

	// Ust sinir: bunun altinda her sey tamamen taranir; ustu fail-closed.
	for _, f := range fields {
		if len(f) > wafHardLimit {
			return wafOversizeRule, true
		}
	}

	// Kucuk harfli kopya + ASCII bayragi (on-suzgec icin); tek seferlik.
	lower := make([]string, len(fields))
	ascii := make([]bool, len(fields))
	for i, f := range fields {
		ascii[i] = isASCII(f)
		if ascii[i] {
			lower[i] = strings.ToLower(f)
		}
	}

	for _, re := range rules {
		hints := wafHints[re]
		for i, f := range fields {
			if hints != nil && ascii[i] && !hintHit(lower[i], hints) {
				continue
			}
			if re.MatchString(f) {
				return re.String(), true
			}
		}
	}
	return "", false
}
