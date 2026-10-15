package updates

import (
	"fmt"
	"html"
	"io/fs"
	"log/slog"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/tkodcumpeg4/zorven/server/store"
)

// Site, tanitim sitesi hostunda Guncellemeler sayfalarini, RSS'i, site haritasini
// ve abonelikten cikma sayfasini sunar. Diger yollar next'e gecer.
//
// Sayfa iskeleti (head/header/footer) ELLE KOPYALANMAZ: scripts/sync_marketing_shell.py
// ile guncel tutulan sablon sayfalardan (guncellemeler.html, en/updates.html,
// ja/updates.html) okunur ve <main> govdesi + baslik/canonical doldurulur.
type Site struct {
	Store   store.UpdatesStore
	FS      fs.FS // marketing kok dizini
	Key     []byte
	BaseURL string // https://zorven.app
	Log     *slog.Logger
}

const pageSize = 20

type routeKind int

const (
	routeNone routeKind = iota
	routeList
	routePost
	routeRSS
	routeSitemap
	routeUnsub
)

func parseRoute(p string) (routeKind, string, string) {
	p = strings.TrimRight(p, "/")
	switch p {
	case "/u/announcements":
		return routeUnsub, "", ""
	case "/sitemap-updates.xml":
		return routeSitemap, "", ""
	}
	for _, lang := range []string{LangTR, LangEN, LangJA} {
		base := ListPath(lang)
		if p == base {
			return routeList, lang, ""
		}
		if rest, ok := strings.CutPrefix(p, base+"/"); ok && rest != "" && !strings.Contains(rest, "/") {
			if rest == "rss.xml" {
				return routeRSS, lang, ""
			}
			return routePost, lang, rest
		}
	}
	return routeNone, "", ""
}

// Wrap, Guncellemeler yollarini yakalar; gerisini next'e verir.
func (s *Site) Wrap(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		kind, lang, slug := parseRoute(r.URL.Path)
		if kind == routeNone || s.Store == nil {
			next.ServeHTTP(w, r)
			return
		}
		if kind == routeUnsub {
			s.serveUnsub(w, r)
			return
		}
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			w.Header().Set("Allow", "GET, HEAD")
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		switch kind {
		case routeList:
			s.serveList(w, r, lang)
		case routePost:
			s.servePost(w, r, lang, slug)
		case routeRSS:
			s.serveRSS(w, r, lang)
		case routeSitemap:
			s.serveSitemap(w, r)
		}
	})
}

func (s *Site) logger() *slog.Logger {
	if s.Log != nil {
		return s.Log
	}
	return slog.Default()
}

// ---- sablon ----

var (
	reTitle    = regexp.MustCompile(`(?s)<title>.*?</title>`)
	reDesc     = regexp.MustCompile(`<meta name="description" content="[^"]*">`)
	reHeadLink = regexp.MustCompile(`\s*<link rel="(?:canonical|alternate)"[^>]*>`)
	reHeadOG   = regexp.MustCompile(`\s*<meta property="og:(?:url|title|description|locale)"[^>]*>`)
	reMain     = regexp.MustCompile(`(?s)<main[^>]*>.*?</main>`)
)

func (s *Site) template(lang string) (string, bool) {
	names := map[string]string{LangTR: "guncellemeler.html", LangEN: "en/updates.html", LangJA: "ja/updates.html"}
	if b, err := fs.ReadFile(s.FS, names[lang]); err == nil {
		return string(b), true
	}
	if lang == LangJA {
		// ja iskeleti henuz yoksa en iskeleti kullanilir (dil niteligi duzeltilir).
		if b, err := fs.ReadFile(s.FS, names[LangEN]); err == nil {
			return strings.Replace(string(b), `<html lang="en">`, `<html lang="ja">`, 1), true
		}
	}
	return "", false
}

type headInfo struct {
	title, desc, canonical string
	alts                   [][2]string // hreflang, mutlak url
	ogLocale               string
	rss                    string
	noindex                bool
}

func (s *Site) render(w http.ResponseWriter, r *http.Request, lang string, status int, hi headInfo, main string) {
	tpl, ok := s.template(lang)
	if !ok {
		http.Error(w, "sablon yok", http.StatusInternalServerError)
		return
	}
	e := html.EscapeString
	tpl = reTitle.ReplaceAllLiteralString(tpl, "<title>"+e(hi.title)+"</title>")
	tpl = reDesc.ReplaceAllLiteralString(tpl, `<meta name="description" content="`+e(hi.desc)+`">`)
	tpl = reHeadLink.ReplaceAllString(tpl, "")
	tpl = reHeadOG.ReplaceAllString(tpl, "")
	var hb strings.Builder
	hb.WriteString(`  <link rel="canonical" href="` + e(hi.canonical) + `">` + "\n")
	for _, a := range hi.alts {
		hb.WriteString(`  <link rel="alternate" hreflang="` + a[0] + `" href="` + e(a[1]) + `">` + "\n")
	}
	if hi.rss != "" {
		hb.WriteString(`  <link rel="alternate" type="application/rss+xml" title="Zorven" href="` + e(hi.rss) + `">` + "\n")
	}
	hb.WriteString(`  <meta property="og:url" content="` + e(hi.canonical) + `">` + "\n")
	hb.WriteString(`  <meta property="og:title" content="` + e(hi.title) + `">` + "\n")
	hb.WriteString(`  <meta property="og:description" content="` + e(hi.desc) + `">` + "\n")
	hb.WriteString(`  <meta property="og:locale" content="` + hi.ogLocale + `">` + "\n")
	if hi.noindex {
		hb.WriteString(`  <meta name="robots" content="noindex">` + "\n")
	}
	if i := strings.Index(tpl, "<!-- /zv:head -->"); i >= 0 {
		tpl = tpl[:i] + strings.TrimLeft(hb.String(), " ") + "  " + tpl[i:]
	} else {
		tpl = strings.Replace(tpl, "</head>", hb.String()+"</head>", 1)
	}
	tpl = reMain.ReplaceAllLiteralString(tpl, "<main>\n"+main+"\n  </main>")

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "public, max-age=60")
	w.WriteHeader(status)
	if r.Method != http.MethodHead {
		_, _ = w.Write([]byte(tpl))
	}
}

// ---- arayuz metinleri ----

type uiText struct {
	Eyebrow, H1, Lead, Title, Desc, Back, Empty, Prev, Next, RSS, Window, Fallback, NotFound, OGLocale, Subscribe string
}

var ui = map[string]uiText{
	LangTR: {"Güncellemeler", "Sürümler, güncellemeler ve bakımlar", "Zorven'de ne değişti, ne zaman bakım yapacağız: tüm duyurular tek yerde.",
		"Güncellemeler — Sürüm notları ve bakım duyuruları | Zorven",
		"Zorven sürüm notları, güncellemeler ve planlı bakım duyuruları.",
		"Tüm güncellemeler", "Henüz yayınlanmış bir güncelleme yok.", "Yeni", "Eski", "RSS ile takip et",
		"Bakım penceresi", "Bu yazı henüz bu dile çevrilmedi; %s sürümü gösteriliyor.", "Güncelleme bulunamadı", "tr_TR",
		"E-posta duyurularını panelinizin Profil sayfasından yönetebilirsiniz."},
	LangEN: {"Updates", "Releases, updates and maintenance", "What changed in Zorven and when we will perform maintenance: all announcements in one place.",
		"Updates — Release notes and maintenance notices | Zorven",
		"Zorven release notes, product updates and planned maintenance notices.",
		"All updates", "No updates have been published yet.", "Newer", "Older", "Follow via RSS",
		"Maintenance window", "This post has not been translated into this language yet; showing the %s version.", "Update not found", "en_US",
		"You can manage announcement emails on the Profile page of your dashboard."},
	LangJA: {"更新情報", "リリース、アップデート、メンテナンス", "Zorven の変更内容と今後のメンテナンス予定をまとめてお知らせします。",
		"更新情報 — リリースノートとメンテナンス告知 | Zorven",
		"Zorven のリリースノート、アップデート、計画メンテナンスのお知らせ。",
		"すべての更新情報", "公開された更新情報はまだありません。", "新しい記事", "古い記事", "RSS で購読",
		"メンテナンス時間", "この記事はまだこの言語に翻訳されていません。%s 版を表示しています。", "更新情報が見つかりません", "ja_JP",
		"お知らせメールの設定はダッシュボードのプロフィールページで変更できます。"},
}

var langName = map[string]map[string]string{
	LangTR: {LangTR: "Türkçe", LangEN: "İngilizce", LangJA: "Japonca"},
	LangEN: {LangTR: "Turkish", LangEN: "English", LangJA: "Japanese"},
	LangJA: {LangTR: "トルコ語", LangEN: "英語", LangJA: "日本語"},
}

func (s *Site) abs(p string) string { return strings.TrimRight(s.BaseURL, "/") + p }

func (s *Site) listAlts() [][2]string {
	return [][2]string{{LangTR, s.abs(ListPath(LangTR))}, {LangEN, s.abs(ListPath(LangEN))}, {LangJA, s.abs(ListPath(LangJA))}, {"x-default", s.abs(ListPath(LangTR))}}
}

func windowHTML(p store.UpdatePost, lang string) string {
	if p.MaintenanceStart == nil {
		return ""
	}
	t := ui[lang]
	end := ""
	if p.MaintenanceEnd != nil {
		end = ` data-end="` + p.MaintenanceEnd.UTC().Format(time.RFC3339) + `"`
	}
	return `<p class="upd-window"><strong>` + html.EscapeString(t.Window) + `:</strong> ` +
		`<time data-zv-window data-lang="` + lang + `" data-start="` + p.MaintenanceStart.UTC().Format(time.RFC3339) + `"` + end + `>` +
		html.EscapeString(WindowUTC(p.MaintenanceStart, p.MaintenanceEnd)) + `</time></p>`
}

func metaHTML(p store.UpdatePost, lang string) string {
	var b strings.Builder
	b.WriteString(`<div class="upd-meta"><span class="upd-badge upd-` + html.EscapeString(p.Kind) + `">` + html.EscapeString(KindLabel(p.Kind, lang)) + `</span>`)
	if p.Version != "" {
		b.WriteString(` <span class="upd-ver mono">` + html.EscapeString(p.Version) + `</span>`)
	}
	if p.PublishedAt != nil {
		d := p.PublishedAt.UTC()
		b.WriteString(` <time datetime="` + d.Format(time.RFC3339) + `">` + d.Format("2006-01-02") + `</time>`)
	}
	b.WriteString(`</div>`)
	return b.String()
}

// ---- liste ----

func (s *Site) serveList(w http.ResponseWriter, r *http.Request, lang string) {
	page, _ := strconv.Atoi(r.URL.Query().Get("page"))
	if page < 1 {
		page = 1
	}
	if page > 10000 {
		page = 10000
	}
	posts, total, err := s.Store.ListUpdatePosts(r.Context(), true, pageSize, (page-1)*pageSize)
	if err != nil {
		s.logger().Error("guncellemeler listelenemedi", "hata", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	t := ui[lang]
	var b strings.Builder
	b.WriteString(`    <section class="page-hero"><div class="container">` +
		`<span class="eyebrow">` + html.EscapeString(t.Eyebrow) + `</span><h1>` + html.EscapeString(t.H1) + `</h1>` +
		`<p class="lead">` + html.EscapeString(t.Lead) + `</p>` +
		`<p><a class="upd-rss" href="` + ListPath(lang) + `/rss.xml">` + html.EscapeString(t.RSS) + `</a></p></div></section>` + "\n")
	b.WriteString(`    <section class="section-wrap page-body" style="padding-top:24px"><div class="container upd-list">` + "\n")
	if len(posts) == 0 {
		b.WriteString(`<p class="upd-empty">` + html.EscapeString(t.Empty) + `</p>`)
	}
	for _, p := range posts {
		title, body, used := Pick(p, lang)
		b.WriteString(`<article class="upd-card">` + metaHTML(p, lang))
		b.WriteString(`<h2><a href="` + PostPath(lang, p.Slug) + `">` + html.EscapeString(title) + `</a></h2>`)
		b.WriteString(windowHTML(p, lang))
		b.WriteString(`<p>` + html.EscapeString(Excerpt(body, 240)) + `</p>`)
		if used != lang {
			b.WriteString(`<p class="upd-note">` + html.EscapeString(fmt.Sprintf(t.Fallback, langName[lang][used])) + `</p>`)
		}
		b.WriteString(`</article>` + "\n")
	}
	if page > 1 || total > page*pageSize {
		b.WriteString(`<nav class="upd-pager" aria-label="pagination">`)
		if page > 1 {
			b.WriteString(`<a href="` + ListPath(lang) + `?page=` + strconv.Itoa(page-1) + `">&larr; ` + html.EscapeString(t.Prev) + `</a>`)
		}
		if total > page*pageSize {
			b.WriteString(`<a href="` + ListPath(lang) + `?page=` + strconv.Itoa(page+1) + `">` + html.EscapeString(t.Next) + ` &rarr;</a>`)
		}
		b.WriteString(`</nav>`)
	}
	b.WriteString(`</div></section>`)

	s.render(w, r, lang, http.StatusOK, headInfo{
		title: t.Title, desc: t.Desc, canonical: s.abs(ListPath(lang)), alts: s.listAlts(),
		ogLocale: t.OGLocale, rss: s.abs(ListPath(lang) + "/rss.xml"),
	}, b.String())
}

// ---- yazi ----

func (s *Site) servePost(w http.ResponseWriter, r *http.Request, lang, slug string) {
	t := ui[lang]
	if !ValidSlug(slug) {
		s.notFound(w, r, lang)
		return
	}
	p, err := s.Store.GetUpdatePostBySlug(r.Context(), slug, true)
	if err != nil {
		s.notFound(w, r, lang)
		return
	}
	title, body, used := Pick(p, lang)
	var b strings.Builder
	b.WriteString(`    <section class="page-hero"><div class="container">` + metaHTML(p, lang) +
		`<h1>` + html.EscapeString(title) + `</h1></div></section>` + "\n")
	b.WriteString(`    <section class="section-wrap page-body" style="padding-top:24px"><div class="container"><article class="upd-article">`)
	b.WriteString(windowHTML(p, lang))
	if used != lang {
		b.WriteString(`<p class="upd-note">` + html.EscapeString(fmt.Sprintf(t.Fallback, langName[lang][used])) + `</p>`)
	}
	b.WriteString(`<div class="prose">` + RenderMarkdown(body) + `</div>`)
	b.WriteString(`<p class="upd-back"><a href="` + ListPath(lang) + `">&larr; ` + html.EscapeString(t.Back) + `</a></p>`)
	b.WriteString(`</article></div></section>`)

	// hreflang yalniz gercek cevirisi olan dillerde.
	var alts [][2]string
	for _, l := range []string{LangTR, LangEN, LangJA} {
		if Has(p, l) {
			alts = append(alts, [2]string{l, s.abs(PostPath(l, p.Slug))})
		}
	}
	alts = append(alts, [2]string{"x-default", s.abs(PostPath(LangTR, p.Slug))})
	canon := s.abs(PostPath(lang, p.Slug))
	if used != lang {
		canon = s.abs(PostPath(used, p.Slug)) // ceviri yoksa kanonik asil dildir
	}
	s.render(w, r, lang, http.StatusOK, headInfo{
		title: title + " | Zorven", desc: Excerpt(body, 160), canonical: canon, alts: alts,
		ogLocale: t.OGLocale, rss: s.abs(ListPath(lang) + "/rss.xml"),
	}, b.String())
}

func (s *Site) notFound(w http.ResponseWriter, r *http.Request, lang string) {
	t := ui[lang]
	main := `    <section class="page-hero"><div class="container"><h1>` + html.EscapeString(t.NotFound) + `</h1>` +
		`<p><a href="` + ListPath(lang) + `">&larr; ` + html.EscapeString(t.Back) + `</a></p></div></section>`
	s.render(w, r, lang, http.StatusNotFound, headInfo{
		title: t.NotFound + " | Zorven", desc: t.Desc, canonical: s.abs(ListPath(lang)), alts: s.listAlts(),
		ogLocale: t.OGLocale, noindex: true,
	}, main)
}

// ---- RSS ----

var xmlBad = regexp.MustCompile("[\x00-\x08\x0b\x0c\x0e-\x1f]")

func xmlEsc(s string) string { return html.EscapeString(xmlBad.ReplaceAllString(s, "")) }

func (s *Site) serveRSS(w http.ResponseWriter, r *http.Request, lang string) {
	posts, _, err := s.Store.ListUpdatePosts(r.Context(), true, 50, 0)
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	t := ui[lang]
	var b strings.Builder
	b.WriteString(`<?xml version="1.0" encoding="UTF-8"?>` + "\n")
	b.WriteString(`<rss version="2.0" xmlns:atom="http://www.w3.org/2005/Atom"><channel>` + "\n")
	b.WriteString(`<title>Zorven — ` + xmlEsc(t.Eyebrow) + `</title>` + "\n")
	b.WriteString(`<link>` + xmlEsc(s.abs(ListPath(lang))) + `</link>` + "\n")
	b.WriteString(`<description>` + xmlEsc(t.Desc) + `</description>` + "\n")
	b.WriteString(`<language>` + lang + `</language>` + "\n")
	b.WriteString(`<atom:link href="` + xmlEsc(s.abs(ListPath(lang)+"/rss.xml")) + `" rel="self" type="application/rss+xml"/>` + "\n")
	if len(posts) > 0 && posts[0].PublishedAt != nil {
		b.WriteString(`<lastBuildDate>` + posts[0].PublishedAt.UTC().Format(time.RFC1123Z) + `</lastBuildDate>` + "\n")
	}
	for _, p := range posts {
		title, body, _ := Pick(p, lang)
		link := s.abs(PostPath(lang, p.Slug))
		b.WriteString(`<item><title>` + xmlEsc(title) + `</title><link>` + xmlEsc(link) + `</link>` +
			`<guid isPermaLink="true">` + xmlEsc(link) + `</guid>`)
		if p.PublishedAt != nil {
			b.WriteString(`<pubDate>` + p.PublishedAt.UTC().Format(time.RFC1123Z) + `</pubDate>`)
		}
		b.WriteString(`<category>` + xmlEsc(KindLabel(p.Kind, lang)) + `</category>`)
		b.WriteString(`<description>` + xmlEsc(RenderMarkdown(body)) + `</description></item>` + "\n")
	}
	b.WriteString(`</channel></rss>` + "\n")
	w.Header().Set("Content-Type", "application/rss+xml; charset=utf-8")
	w.Header().Set("Cache-Control", "public, max-age=300")
	if r.Method != http.MethodHead {
		_, _ = w.Write([]byte(b.String()))
	}
}

// ---- site haritasi ----

func (s *Site) serveSitemap(w http.ResponseWriter, r *http.Request) {
	posts, _, err := s.Store.ListUpdatePosts(r.Context(), true, 200, 0)
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	var b strings.Builder
	b.WriteString(`<?xml version="1.0" encoding="UTF-8"?>` + "\n" +
		`<urlset xmlns="http://www.sitemaps.org/schemas/sitemap/0.9">` + "\n")
	for _, l := range []string{LangTR, LangEN, LangJA} {
		b.WriteString(`<url><loc>` + xmlEsc(s.abs(ListPath(l))) + `</loc></url>` + "\n")
	}
	for _, p := range posts {
		for _, l := range []string{LangTR, LangEN, LangJA} {
			if !Has(p, l) {
				continue
			}
			b.WriteString(`<url><loc>` + xmlEsc(s.abs(PostPath(l, p.Slug))) + `</loc>`)
			if p.PublishedAt != nil {
				b.WriteString(`<lastmod>` + p.PublishedAt.UTC().Format("2006-01-02") + `</lastmod>`)
			}
			b.WriteString(`</url>` + "\n")
		}
	}
	b.WriteString(`</urlset>` + "\n")
	w.Header().Set("Content-Type", "application/xml; charset=utf-8")
	w.Header().Set("Cache-Control", "public, max-age=3600")
	if r.Method != http.MethodHead {
		_, _ = w.Write([]byte(b.String()))
	}
}
