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
//   - Tarama wafScanLimit ile sinirlanir; cok uzun sorgu dizeleri CPU yakamaz.
//   - Hangi kuralin eslestigi YANITA YAZILMAZ (bilgi sizdirmaz), yalnizca loglanir.

import (
	"net/http"
	"net/url"
	"regexp"
	"strings"
)

// wafScanLimit, tek bir alandan taranacak azami bayt.
const wafScanLimit = 8 << 10 // 8 KB

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

// clip, taranacak metni sinira kirpar.
func clip(s string) string {
	if len(s) > wafScanLimit {
		return s[:wafScanLimit]
	}
	return s
}

// wafScan, istegin taranacak alanlarini kurallardan gecirir.
// Eslesme varsa eslesme kaynagini (loglamak icin) ve true doner.
func wafScan(r *http.Request, rules []*regexp.Regexp) (string, bool) {
	if len(rules) == 0 {
		return "", false
	}

	fields := make([]string, 0, 3+len(wafScannedHeaders))
	fields = append(fields, clip(r.URL.Path))
	if q := r.URL.RawQuery; q != "" {
		fields = append(fields, clip(q))
		// URL-decode edilmis hali: saldirilar sik sik encode edilerek gecirilir.
		if dec, err := url.QueryUnescape(q); err == nil && dec != q {
			fields = append(fields, clip(dec))
		}
	}
	for _, h := range wafScannedHeaders {
		if v := r.Header.Get(h); v != "" {
			fields = append(fields, clip(v))
		}
	}

	for _, re := range rules {
		for _, f := range fields {
			if re.MatchString(f) {
				return re.String(), true
			}
		}
	}
	return "", false
}
