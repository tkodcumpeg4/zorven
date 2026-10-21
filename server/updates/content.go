package updates

import (
	"regexp"
	"strings"
	"time"

	"github.com/tkodcumpeg4/zorven/server/store"
)

// Desteklenen diller.
const (
	LangTR = "tr"
	LangEN = "en"
	LangJA = "ja"
)

// NormLang, serbest metni tr/en/ja'ya indirger (bilinmiyorsa tr).
func NormLang(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	switch {
	case strings.HasPrefix(s, "ja"):
		return LangJA
	case strings.HasPrefix(s, "en"):
		return LangEN
	default:
		return LangTR
	}
}

// Pick, istenen dil icin baslik/govde secer. Eksik ceviri sirasiyla
// ja -> en -> tr, en -> tr yoluyla doner; used gercekte kullanilan dildir.
func Pick(p store.UpdatePost, lang string) (title, body, used string) {
	lang = NormLang(lang)
	type cand struct{ l, t, b string }
	all := map[string]cand{
		LangTR: {LangTR, p.TitleTR, p.BodyTR},
		LangEN: {LangEN, p.TitleEN, p.BodyEN},
		LangJA: {LangJA, p.TitleJA, p.BodyJA},
	}
	order := []string{LangTR}
	switch lang {
	case LangJA:
		order = []string{LangJA, LangEN, LangTR}
	case LangEN:
		order = []string{LangEN, LangTR}
	}
	for _, l := range order {
		if c := all[l]; strings.TrimSpace(c.t) != "" {
			return c.t, c.b, c.l
		}
	}
	return p.TitleTR, p.BodyTR, LangTR
}

// Has, yazinin verilen dilde kendi cevirisi var mi.
func Has(p store.UpdatePost, lang string) bool {
	switch NormLang(lang) {
	case LangEN:
		return strings.TrimSpace(p.TitleEN) != ""
	case LangJA:
		return strings.TrimSpace(p.TitleJA) != ""
	}
	return strings.TrimSpace(p.TitleTR) != ""
}

var slugRe = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,79}$`)

// ValidSlug, URL parcasi olarak guvenli slug kontrolu. "rss" ve "rss.xml" ayrilmis.
func ValidSlug(s string) bool {
	return slugRe.MatchString(s) && s != "rss"
}

// ValidKind, gecerli tur mu.
func ValidKind(k string) bool {
	return k == store.UpdateKindRelease || k == store.UpdateKindUpdate || k == store.UpdateKindMaintenance
}

// KindLabel, turun dile gore etiketi.
func KindLabel(kind, lang string) string {
	l := NormLang(lang)
	m := map[string][3]string{
		store.UpdateKindRelease:     {"Sürüm", "Release", "リリース"},
		store.UpdateKindUpdate:      {"Güncelleme", "Update", "アップデート"},
		store.UpdateKindMaintenance: {"Bakım", "Maintenance", "メンテナンス"},
	}
	v, ok := m[kind]
	if !ok {
		v = m[store.UpdateKindUpdate]
	}
	switch l {
	case LangEN:
		return v[1]
	case LangJA:
		return v[2]
	}
	return v[0]
}

// WindowUTC, bakim penceresinin UTC metnini uretir ("2026-10-20 01:00 - 03:00 UTC").
func WindowUTC(start, end *time.Time) string {
	if start == nil {
		return ""
	}
	s := start.UTC()
	out := s.Format("2006-01-02 15:04")
	if end != nil {
		e := end.UTC()
		if e.Format("2006-01-02") == s.Format("2006-01-02") {
			out += " - " + e.Format("15:04")
		} else {
			out += " - " + e.Format("2006-01-02 15:04")
		}
	}
	return out + " UTC"
}

// PostPath, yazinin dile ozgu yolu (slug bos ise liste sayfasi).
func PostPath(lang, slug string) string {
	base := ListPath(lang)
	if slug == "" {
		return base
	}
	return base + "/" + slug
}

// ListPath, dile ozgu liste yolu.
func ListPath(lang string) string {
	switch NormLang(lang) {
	case LangEN:
		return "/en/updates"
	case LangJA:
		return "/ja/updates"
	}
	return "/updates"
}

// LegacyListPathTR, eski Turkce liste yolu; /updates altina kalici yonlendirilir
// (gonderilmis e-postalardaki ve disaridaki eski linkler kirilmasin).
const LegacyListPathTR = "/guncellemeler"

// Excerpt, govdeden kisa duz-metin ozet cikarir.
func Excerpt(md string, max int) string {
	t := PlainText(md)
	t = strings.Join(strings.Fields(strings.ReplaceAll(t, "\n", " ")), " ")
	r := []rune(t)
	if len(r) <= max {
		return t
	}
	return strings.TrimSpace(string(r[:max])) + "…"
}
