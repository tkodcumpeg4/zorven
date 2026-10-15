package updates

import (
	"fmt"
	"html"
	"net/url"
	"strings"

	"github.com/tkodcumpeg4/zorven/server/store"
)

// emailStrings, e-posta sablonunun dile gore sabit metinleri.
type emailStrings struct {
	ViewOnline, Why, Unsub, Window, Footer, Lang, Version string
}

var emailTexts = map[string]emailStrings{
	LangTR: {"Web'de görüntüle", "Bu e-postayı Zorven ürün duyurularına abone olduğun için alıyorsun.", "Duyuruları almayı bırak", "Bakım penceresi", "Zorven · Kendi sunucunda barındırılan tünel platformu", "tr", "Sürüm"},
	LangEN: {"View on the web", "You are receiving this because you subscribed to Zorven product announcements.", "Unsubscribe from announcements", "Maintenance window", "Zorven · Self-hosted tunnel platform", "en", "Version"},
	LangJA: {"Webで表示", "Zorven の製品お知らせを購読しているため、このメールをお送りしています。", "お知らせメールの配信を停止", "メンテナンス時間", "Zorven · セルフホスト型トンネルプラットフォーム", "ja", "バージョン"},
}

// Email, bir duyuru e-postasinin parcalari.
type Email struct {
	Subject, Text, HTML string
	Lang                string // kullanilan icerik dili
	Headers             []string
}

// UnsubURL, abonelikten cikma baglantisi.
func UnsubURL(baseURL string, key []byte, userID, lang string) string {
	return strings.TrimRight(baseURL, "/") + "/u/announcements?token=" + url.QueryEscape(UnsubToken(key, userID)) +
		"&lang=" + NormLang(lang)
}

// BuildEmail, yazi icin bir aliciya ozel e-postayi uretir (userID bossa test
// e-postasi: abonelik baglantisi yine eklenir ama jeton verilmezse genel sayfa).
func BuildEmail(p store.UpdatePost, lang, baseURL string, key []byte, userID string) Email {
	title, body, used := Pick(p, lang)
	tx := emailTexts[NormLang(lang)]
	subject := title
	if p.Kind == store.UpdateKindMaintenance {
		subject = KindLabel(p.Kind, lang) + ": " + title
	} else if p.Version != "" && !strings.Contains(title, p.Version) {
		subject = "Zorven " + p.Version + " — " + title
	}
	postURL := strings.TrimRight(baseURL, "/") + PostPath(lang, p.Slug)
	unsub := UnsubURL(baseURL, key, userID, lang)

	window := WindowUTC(p.MaintenanceStart, p.MaintenanceEnd)
	var text strings.Builder
	text.WriteString(title + "\r\n\r\n")
	if window != "" {
		text.WriteString(tx.Window + ": " + window + "\r\n\r\n")
	}
	text.WriteString(PlainText(body) + "\r\n\r\n")
	text.WriteString(tx.ViewOnline + ": " + postURL + "\r\n\r\n--\r\n" + tx.Why + "\r\n" + tx.Unsub + ": " + unsub + "\r\n")

	return Email{
		Subject: subject,
		Text:    text.String(),
		HTML:    emailHTML(p, tx, title, body, window, postURL, unsub),
		Lang:    used,
		Headers: []string{
			"List-Unsubscribe: <" + unsub + ">",
			"List-Unsubscribe-Post: List-Unsubscribe=One-Click",
			"Precedence: bulk",
		},
	}
}

// emailStyler, RenderMarkdown ciktisindaki (ozniteliksiz) etiketlere e-posta
// istemcileri icin satir ici stil ekler.
var emailStyler = strings.NewReplacer(
	"<h2>", `<h2 style="margin:18px 0 8px;font-size:17px;color:#e6edf3;">`,
	"<h3>", `<h3 style="margin:16px 0 6px;font-size:15px;color:#e6edf3;">`,
	"<h4>", `<h4 style="margin:14px 0 6px;font-size:14px;color:#e6edf3;">`,
	"<p>", `<p style="margin:0 0 12px;font-size:14px;line-height:1.65;color:#b6c1cc;">`,
	"<ul>", `<ul style="margin:0 0 12px;padding-left:20px;color:#b6c1cc;font-size:14px;line-height:1.65;">`,
	"<ol>", `<ol style="margin:0 0 12px;padding-left:20px;color:#b6c1cc;font-size:14px;line-height:1.65;">`,
	"<li>", `<li style="margin:0 0 4px;">`,
	"<code>", `<code style="background:#1b222b;color:#e6edf3;padding:1px 5px;border-radius:4px;font-family:Consolas,monospace;font-size:13px;">`,
	"<pre>", `<pre style="background:#0b0e12;border:1px solid #232a33;border-radius:8px;padding:10px;overflow:auto;">`,
	"<blockquote>", `<blockquote style="margin:0 0 12px;padding-left:12px;border-left:3px solid #3ddc84;">`,
	"<a ", `<a style="color:#3ddc84;" `,
	"<strong>", `<strong style="color:#e6edf3;">`,
	"<hr>", `<hr style="border:0;border-top:1px solid #232a33;margin:16px 0;">`,
)

func emailHTML(p store.UpdatePost, tx emailStrings, title, body, window, postURL, unsub string) string {
	e := html.EscapeString
	badge := e(KindLabel(p.Kind, tx.Lang))
	if p.Version != "" {
		badge += " · " + e(p.Version)
	}
	win := ""
	if window != "" {
		win = `<p style="margin:0 0 14px;font-size:13px;color:#e6edf3;background:#1b222b;border-radius:8px;padding:8px 12px;"><b>` + e(tx.Window) + `:</b> ` + e(window) + `</p>`
	}
	return fmt.Sprintf(`<!doctype html><html lang="%s"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1"></head>
<body style="margin:0;padding:0;background:#0b0e12;">
<table role="presentation" width="100%%" cellpadding="0" cellspacing="0" style="background:#0b0e12;padding:32px 0;"><tr><td align="center">
<table role="presentation" width="560" cellpadding="0" cellspacing="0" style="width:560px;max-width:94%%;background:#12161c;border:1px solid #232a33;border-radius:16px;overflow:hidden;">
<tr><td style="padding:28px 32px 8px 32px;">
<table role="presentation" cellpadding="0" cellspacing="0"><tr>
<td style="vertical-align:middle;"><img src="https://zorven.app/logo-email.png" width="34" height="34" alt="Zorven" style="display:block;width:34px;height:34px;border-radius:9px;border:0;"></td>
<td style="vertical-align:middle;padding-left:12px;font-family:Arial,Helvetica,sans-serif;font-size:19px;font-weight:700;color:#e6edf3;letter-spacing:-0.3px;">Zorven</td>
</tr></table></td></tr>
<tr><td style="padding:12px 32px 28px 32px;font-family:Arial,Helvetica,sans-serif;color:#e6edf3;">
<p style="margin:0 0 6px;font-size:12px;font-weight:700;letter-spacing:.06em;text-transform:uppercase;color:#3ddc84;">%s</p>
<h1 style="margin:0 0 14px;font-size:21px;font-weight:700;color:#e6edf3;">%s</h1>
%s%s
<table role="presentation" cellpadding="0" cellspacing="0" style="margin:18px 0 6px;"><tr><td style="border-radius:10px;background:#3ddc84;"><a href="%s" style="display:inline-block;padding:11px 24px;font-family:Arial,Helvetica,sans-serif;font-size:14px;font-weight:700;color:#0b0e12;text-decoration:none;border-radius:10px;">%s</a></td></tr></table>
</td></tr></table>
<div style="font-family:Arial,Helvetica,sans-serif;font-size:11px;line-height:1.6;color:#8b98a5;padding:18px 12px;max-width:520px;">%s<br><a href="%s" style="color:#8b98a5;">%s</a><br>%s</div>
</td></tr></table></body></html>`,
		tx.Lang, badge, e(title), win, emailStyler.Replace(RenderMarkdown(body)),
		e(postURL), e(tx.ViewOnline), e(tx.Why), e(unsub), e(tx.Unsub), e(tx.Footer))
}
