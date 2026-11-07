package mailauth

import (
	"bytes"
	"net/mail"
	"strings"
)

// hdrField, ham iletinin baslik bolumundeki tek bir alandir (katlanmis devam
// satirlari dahil).
type hdrField struct {
	name       string // iki nokta oncesi, sagdaki bosluklar kirpilmis ("" = iki noktasiz satir)
	start, end int    // ham iletideki [start, end) araligi; end satir sonunu da kapsar
}

// scanHeader, ham iletinin baslik bolumunu (ilk bos satira kadar) alanlara boler.
// CRLF ve yalin LF satir sonlarini kabul eder. Alan degerleri burada kopyalanmaz:
// yalniz konumlar tutulur, deger gerektiginde value ile acilir (cok uzun
// devam satiri zincirlerinde ikinci dereceden maliyet olmasin).
func scanHeader(raw []byte) []hdrField {
	var fields []hdrField
	pos := 0
	for pos < len(raw) {
		next := len(raw)
		lineEnd := len(raw)
		if nl := bytes.IndexByte(raw[pos:], '\n'); nl >= 0 {
			lineEnd = pos + nl
			next = pos + nl + 1
		}
		line := bytes.TrimSuffix(raw[pos:lineEnd], []byte("\r"))
		if len(line) == 0 {
			break // baslik bolumunun sonu
		}
		if (line[0] == ' ' || line[0] == '\t') && len(fields) > 0 {
			fields[len(fields)-1].end = next // katlanmis devam satiri
		} else {
			name := ""
			if i := bytes.IndexByte(line, ':'); i >= 0 {
				name = strings.TrimRight(string(line[:i]), " \t")
			}
			fields = append(fields, hdrField{name: name, start: pos, end: next})
		}
		pos = next
	}
	return fields
}

// value, alanin acilmis (katlama satir sonlari kaldirilmis) degerini doner.
func (f hdrField) value(raw []byte) string {
	seg := raw[f.start:f.end]
	i := bytes.IndexByte(seg, ':')
	if i < 0 {
		return ""
	}
	seg = seg[i+1:]
	seg = bytes.ReplaceAll(seg, []byte("\r"), nil)
	seg = bytes.ReplaceAll(seg, []byte("\n"), nil)
	return string(seg)
}

// fromInfo, RFC5322.From basliginin DMARC icin cozulmus halidir.
type fromInfo struct {
	domain string // tek, gecerli ASCII alan adi
	err    string // "" = tamam; aksi halde kisa sabit gerekce
}

// parseFrom, iletinin tek From basligindan alan adini cikarir. Bas yok, birden
// fazla, ayristirilamayan ya da farkli alan adlarina isaret eden basliklar DMARC
// icin degerlendirilemez sayilir (RFC 7489 madde 6.6.1).
func parseFrom(raw []byte) fromInfo {
	var vals []string
	for _, f := range scanHeader(raw) {
		if strings.EqualFold(f.name, "From") {
			vals = append(vals, f.value(raw))
		}
	}
	switch {
	case len(vals) == 0:
		return fromInfo{err: "no From header"}
	case len(vals) > 1:
		return fromInfo{err: "multiple From headers"}
	case len(vals[0]) > 8192:
		return fromInfo{err: "From header too long"}
	}
	addrs, err := mail.ParseAddressList(vals[0])
	if err != nil || len(addrs) == 0 {
		return fromInfo{err: "unparseable From header"}
	}
	dom := ""
	for _, a := range addrs {
		_, d, ok := splitAddr(a.Address)
		if !ok {
			return fromInfo{err: "invalid From domain"}
		}
		if dom != "" && d != dom {
			return fromInfo{err: "multiple From domains"}
		}
		dom = d
	}
	return fromInfo{domain: dom}
}

const arHeaderName = "Authentication-Results"

// skipCFWS, bastaki bosluklari ve (ic ice olabilen) yorumlari atlar.
func skipCFWS(s string) string {
	for {
		s = strings.TrimLeft(s, " \t")
		if !strings.HasPrefix(s, "(") {
			return s
		}
		depth, i := 0, 0
		for ; i < len(s); i++ {
			switch s[i] {
			case '\\':
				i++
			case '(':
				depth++
			case ')':
				depth--
			}
			if depth == 0 {
				break
			}
		}
		if depth != 0 || i >= len(s) {
			return "" // kapanmamis yorum
		}
		s = s[i+1:]
	}
}

// authservIDOf, Authentication-Results alan degerindeki authserv-id'yi doner
// (kucuk harf, sondaki nokta atilmis). RFC 8601: "Authentication-Results:" ve
// ardindan [CFWS] authserv-id.
func authservIDOf(value string) string {
	if len(value) > 512 {
		value = value[:512]
	}
	s := skipCFWS(value)
	end := strings.IndexAny(s, " \t;(")
	if end < 0 {
		end = len(s)
	}
	id := strings.Trim(s[:end], `"`)
	return strings.TrimSuffix(strings.ToLower(id), ".")
}

// StripAuthResults, ham iletide verilen authserv-id'lerden biriyle gelmis
// Authentication-Results basliklarini (katlanmis devam satirlariyla birlikte)
// siler. Disaridan gelen ileti bizim adimizi tasiyan sahte bir sonuc basligi
// getirip alicinin istemcisini "dkim=pass" demeye kandirabilir (RFC 8601 madde
// 5); kendi kimligimizle gelenler guvenilmez ve atilir. Baska kimliklerle gelen
// basliklara dokunulmaz.
func StripAuthResults(raw []byte, authservIDs ...string) []byte {
	want := make(map[string]bool, len(authservIDs))
	for _, id := range authservIDs {
		id = strings.TrimSuffix(strings.ToLower(strings.TrimSpace(id)), ".")
		if id != "" {
			want[id] = true
		}
	}
	if len(want) == 0 {
		return raw
	}
	var out []byte
	last := 0
	removed := false
	for _, f := range scanHeader(raw) {
		if !strings.EqualFold(f.name, arHeaderName) || !want[authservIDOf(f.value(raw))] {
			continue
		}
		out = append(out, raw[last:f.start]...)
		last = f.end
		removed = true
	}
	if !removed {
		return raw
	}
	return append(out, raw[last:]...)
}

// Prepend, iletinin en ustune r sonucunu Authentication-Results basligi olarak
// ekler. Oncesinde, ayni authserv-id'yi (ve stripIDs'teki esdegerlerini)
// tasiyan sahte basliklar silinir.
func Prepend(raw []byte, authservID string, r *Result, stripIDs ...string) []byte {
	ids := append([]string{authservID}, stripIDs...)
	clean := StripAuthResults(raw, ids...)
	hdr := r.HeaderField(authservID)
	out := make([]byte, 0, len(hdr)+len(clean))
	out = append(out, hdr...)
	return append(out, clean...)
}
