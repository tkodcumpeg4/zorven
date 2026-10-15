// Package updates, "Guncellemeler" (surum/guncelleme/bakim duyurulari) ozelligini
// tasir: guvenli Markdown alt kumesi, kamuya acik sayfalar, RSS ve e-posta kuyrugu.
package updates

import (
	"html"
	"net/url"
	"strings"
)

// RenderMarkdown, Markdown alt kumesini GUVENLI HTML'e cevirir.
//
// Desteklenen: # / ## / ### basliklar (h2/h3/h4), "-" "*" ve "1." listeleri,
// "> " alinti, "---" cizgi, ``` kod bloklari, `kod`, **kalin**, *italik*,
// [metin](url). HAM HTML YOKTUR: tum metin once kacirilir. Baglanti semalari
// yalniz http, https ve mailto; digerleri (javascript:, data: vb.) duz metne
// duser. Baglantilar rel="noopener noreferrer nofollow" tasir.
func RenderMarkdown(src string) string {
	src = strings.ReplaceAll(src, "\r\n", "\n")
	src = strings.ReplaceAll(src, "\r", "\n")
	lines := strings.Split(src, "\n")
	var out strings.Builder
	var para []string
	listKind := "" // "ul" | "ol" | ""

	flushPara := func() {
		if len(para) > 0 {
			out.WriteString("<p>" + renderInline(strings.Join(para, " "), true) + "</p>\n")
			para = nil
		}
	}
	closeList := func() {
		if listKind != "" {
			out.WriteString("</" + listKind + ">\n")
			listKind = ""
		}
	}

	for i := 0; i < len(lines); i++ {
		line := strings.TrimRight(lines[i], " \t")
		trim := strings.TrimSpace(line)

		// Kod bloku
		if strings.HasPrefix(trim, "```") {
			flushPara()
			closeList()
			var code []string
			i++
			for ; i < len(lines); i++ {
				if strings.HasPrefix(strings.TrimSpace(lines[i]), "```") {
					break
				}
				code = append(code, lines[i])
			}
			out.WriteString("<pre><code>" + html.EscapeString(strings.Join(code, "\n")) + "</code></pre>\n")
			continue
		}
		if trim == "" {
			flushPara()
			closeList()
			continue
		}
		if trim == "---" || trim == "***" || trim == "___" {
			flushPara()
			closeList()
			out.WriteString("<hr>\n")
			continue
		}
		if lvl, text := heading(trim); lvl > 0 {
			flushPara()
			closeList()
			tag := []string{"", "h2", "h3", "h4"}[lvl]
			out.WriteString("<" + tag + ">" + renderInline(text, true) + "</" + tag + ">\n")
			continue
		}
		if strings.HasPrefix(trim, ">") {
			flushPara()
			closeList()
			out.WriteString("<blockquote><p>" + renderInline(strings.TrimSpace(trim[1:]), true) + "</p></blockquote>\n")
			continue
		}
		if kind, text := listItem(trim); kind != "" {
			flushPara()
			if listKind != kind {
				closeList()
				out.WriteString("<" + kind + ">\n")
				listKind = kind
			}
			out.WriteString("<li>" + renderInline(text, true) + "</li>\n")
			continue
		}
		closeList()
		para = append(para, trim)
	}
	flushPara()
	closeList()
	return out.String()
}

func heading(s string) (int, string) {
	n := 0
	for n < len(s) && s[n] == '#' {
		n++
	}
	if n == 0 || n > 3 || len(s) <= n || s[n] != ' ' {
		return 0, ""
	}
	return n, strings.TrimSpace(s[n:])
}

func listItem(s string) (string, string) {
	if len(s) > 2 && (s[0] == '-' || s[0] == '*') && s[1] == ' ' {
		return "ul", strings.TrimSpace(s[2:])
	}
	j := 0
	for j < len(s) && s[j] >= '0' && s[j] <= '9' {
		j++
	}
	if j > 0 && j <= 9 && j+1 < len(s) && s[j] == '.' && s[j+1] == ' ' {
		return "ol", strings.TrimSpace(s[j+2:])
	}
	return "", ""
}

// SafeURL, baglanti hedefini dogrular: yalniz http, https, mailto.
func SafeURL(raw string) (string, bool) {
	raw = strings.TrimSpace(raw)
	if raw == "" || strings.ContainsAny(raw, "\x00\r\n\t \"<>") {
		return "", false
	}
	u, err := url.Parse(raw)
	if err != nil {
		return "", false
	}
	switch strings.ToLower(u.Scheme) {
	case "http", "https":
		if u.Host == "" {
			return "", false
		}
	case "mailto":
		if u.Opaque == "" && u.Path == "" {
			return "", false
		}
	default:
		return "", false
	}
	return u.String(), true
}

// parseLink, s'in basindaki "[metin](hedef)" yapisini ayristirir. Koseli ve
// yuvarlak parantezler dengeli sayilir (hedefte ic ice parantez olabilir);
// ters bolu ile kacirilan parantez sayilmaz. n, tuketilen bayt sayisidir.
func parseLink(s string) (label, target string, n int, ok bool) {
	depth := 0
	j := 0
	for ; j < len(s); j++ {
		if s[j] == '\\' {
			j++
			continue
		}
		if s[j] == '[' {
			depth++
		} else if s[j] == ']' {
			depth--
			if depth == 0 {
				break
			}
		}
	}
	if j >= len(s) || j+1 >= len(s) || s[j+1] != '(' {
		return "", "", 0, false
	}
	depth = 0
	k := j + 1
	for ; k < len(s); k++ {
		if s[k] == '\\' {
			k++
			continue
		}
		if s[k] == '(' {
			depth++
		} else if s[k] == ')' {
			depth--
			if depth == 0 {
				break
			}
		}
	}
	if k >= len(s) {
		return "", "", 0, false
	}
	return s[1:j], s[j+2 : k], k + 1, true
}

func renderInline(s string, links bool) string {
	var b strings.Builder
	var pending strings.Builder // kacirilmamis duz metin
	flush := func() {
		if pending.Len() > 0 {
			b.WriteString(html.EscapeString(pending.String()))
			pending.Reset()
		}
	}
	for i := 0; i < len(s); {
		c := s[i]
		switch {
		case c == '`':
			if j := strings.IndexByte(s[i+1:], '`'); j > 0 {
				flush()
				b.WriteString("<code>" + html.EscapeString(s[i+1:i+1+j]) + "</code>")
				i += j + 2
				continue
			}
		case c == '*' && strings.HasPrefix(s[i:], "**"):
			if j := strings.Index(s[i+2:], "**"); j > 0 {
				flush()
				b.WriteString("<strong>" + renderInline(s[i+2:i+2+j], links) + "</strong>")
				i += j + 4
				continue
			}
		case c == '*':
			if i+1 < len(s) && s[i+1] != ' ' {
				if j := strings.IndexByte(s[i+1:], '*'); j > 0 && s[i+j] != ' ' {
					flush()
					b.WriteString("<em>" + renderInline(s[i+1:i+1+j], links) + "</em>")
					i += j + 2
					continue
				}
			}
		case c == '[':
			if label, target, n, ok := parseLink(s[i:]); ok {
				flush()
				if u, safe := SafeURL(target); safe && links {
					ext := ""
					if strings.HasPrefix(u, "http") {
						ext = ` target="_blank"`
					}
					b.WriteString(`<a href="` + html.EscapeString(u) + `" rel="noopener noreferrer nofollow"` + ext + `>` + renderInline(label, false) + `</a>`)
				} else {
					// Reddedilen baglanti: yalniz kendi araligi kacirilmis metin olur.
					b.WriteString(renderInline(label, false))
				}
				i += n
				continue
			}
		}
		pending.WriteByte(c)
		i++
	}
	flush()
	return b.String()
}

// PlainText, Markdown'u e-postanin duz-metin parcasi icin okunur metne indirger.
func PlainText(src string) string {
	src = strings.ReplaceAll(src, "\r\n", "\n")
	var out []string
	for _, l := range strings.Split(src, "\n") {
		t := strings.TrimSpace(l)
		if strings.HasPrefix(t, "```") {
			continue
		}
		if n, txt := heading(t); n > 0 {
			t = strings.ToUpper(txt)
		}
		t = strings.NewReplacer("**", "", "`", "").Replace(t)
		out = append(out, t)
	}
	return strings.TrimSpace(strings.Join(out, "\n"))
}
