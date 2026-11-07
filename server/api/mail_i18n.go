package api

import (
	"html"
	"net/http"
	"sort"
	"strconv"
	"strings"

	"github.com/tkodcumpeg4/zorven/server/store"
)

// Islem e-postalarinin (ekip daveti, metrik uyarisi, webmail deneme sablonu) dili:
// tr / en / ja / zh. Siralama web servisindekiyle AYNIDIR (web/server/lib/email-lang.ts):
//
//  1. kullanicinin kayitli dil tercihi (announcement_prefs.lang)
//  2. panel dili cerezi (zorven_lang; panel her dil degisiminde ve ilk acilista yazar)
//  3. istegin Accept-Language basligi
//  4. tr
//
// Duyuru e-postalari bunun disindadir: onlar kendi 4 dilli altyapisini kullanir
// (server/updates).

// panelLangCookie, panelin dil tercihini sunucuya tasiyan cerez (web/app/plugins/i18n.ts yazar).
const panelLangCookie = "zorven_lang"

// normMailLang, "tr", "EN-us", "ja_JP", "zh-Hant-TW" gibi degerleri tr|en|ja|zh'ye indirger;
// tanimsiz/bos ise "" doner (updates.NormLang gibi tr'ye DUSMEZ: cagiran sirayi surdurur).
func normMailLang(v string) string {
	v = strings.ToLower(strings.TrimSpace(v))
	if i := strings.IndexAny(v, "-_"); i >= 0 {
		v = v[:i]
	}
	switch v {
	case "tr", "en":
		return v
	}
	return ""
}

// mailLangFromAcceptLanguage, q degerine gore (esitte yazim sirasi) ilk desteklenen dili secer.
// Hicbiri desteklenmiyor ama bir dil acikca isteniyorsa "en" (panel de bilinmeyen tarayici
// dillerinde Ingilizceye duser); baslik bos/yalniz "*" ise "" (cagiran tr'ye duser).
func mailLangFromAcceptLanguage(h string) string {
	type cand struct {
		tag string
		q   float64
	}
	var cs []cand
	for _, part := range strings.Split(h, ",") {
		fields := strings.Split(part, ";")
		tag := strings.TrimSpace(fields[0])
		if tag == "" || tag == "*" {
			continue
		}
		q := 1.0
		for _, p := range fields[1:] {
			p = strings.ToLower(strings.ReplaceAll(p, " ", ""))
			if v, ok := strings.CutPrefix(p, "q="); ok {
				if f, err := strconv.ParseFloat(v, 64); err == nil {
					q = f
				}
			}
		}
		if q > 0 {
			cs = append(cs, cand{tag, q})
		}
	}
	if len(cs) == 0 {
		return ""
	}
	sort.SliceStable(cs, func(a, b int) bool { return cs[a].q > cs[b].q })
	for _, c := range cs {
		if l := normMailLang(c.tag); l != "" {
			return l
		}
	}
	return "en"
}

// mailLangFromCookie, panel dili cerezini (zorven_lang) okur; yok/gecersiz ise "".
func mailLangFromCookie(r *http.Request) string {
	for _, c := range r.Cookies() {
		if c.Name == panelLangCookie {
			if l := normMailLang(c.Value); l != "" {
				return l
			}
		}
	}
	return ""
}

// mailLangHint, istekten dil ipucu: kayitli tercih > cerez > Accept-Language. Hicbir sinyal
// yoksa "" doner (kayitli bir dili ezmemek isteyen cagiranlar icin, or. uyari yapilandirmasi).
// userID bos olabilir (yonetici anahtari ya da API belirteci).
func (s *Server) mailLangHint(r *http.Request, userID string) string {
	if r == nil {
		return ""
	}
	if userID != "" {
		if us, ok := s.Store.(store.UpdatesStore); ok {
			if pref, err := us.GetAnnouncementPref(r.Context(), userID); err == nil {
				if l := normMailLang(pref.Lang); l != "" {
					return l
				}
			}
		}
	}
	if l := mailLangFromCookie(r); l != "" {
		return l
	}
	return mailLangFromAcceptLanguage(r.Header.Get("Accept-Language"))
}

// mailLangFor, mailLangHint'tir; sinyal yoksa "tr".
func (s *Server) mailLangFor(r *http.Request, userID string) string {
	if l := s.mailLangHint(r, userID); l != "" {
		return l
	}
	return "tr"
}

// actingUserID, istegi yapan Better Auth kullanicisinin kimligi ("" = yonetici anahtari vb.).
func actingUserID(r *http.Request) string {
	if u, ok := userFromContext(r.Context()); ok && u != nil {
		return u.ID
	}
	return ""
}

// ---- ortak e-posta kabugu (web/server/lib/email-templates.ts ile AYNI marka/duzen) ----

var mailFooterText = map[string]string{
	"tr": "Zorven · Kendi sunucunda barındırılan tünel platformu",
	"en": "Zorven · Self-hosted tunnel platform",
}

var mailFallbackText = map[string]string{
	"tr": "Buton çalışmazsa bu bağlantıyı tarayıcına yapıştır:",
	"en": "If the button doesn't work, paste this link into your browser:",
}

// mailLangOrTR, bilinmeyen dili "tr"ye indirir (tablo aramalari icin).
func mailLangOrTR(lang string) string {
	if l := normMailLang(lang); l != "" {
		return l
	}
	return "tr"
}

// mailHTMLLang, <html lang> degeri (Cince icin zh-CN; duyuru e-postalariyla ayni).
func mailHTMLLang(lang string) string {
	if mailLangOrTR(lang) == "zh" {
		return "zh-CN"
	}
	return mailLangOrTR(lang)
}

// mailShellHTML, marka kabugu: logo + kart + alt bilgi. inner kartin govdesidir (guvenilir HTML).
func mailShellHTML(lang, inner string) string {
	lang = mailLangOrTR(lang)
	return `<!doctype html><html lang="` + mailHTMLLang(lang) + `"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1"></head>
<body style="margin:0;padding:0;background:#0b0e12;">
<table role="presentation" width="100%" cellpadding="0" cellspacing="0" style="background:#0b0e12;padding:32px 0;"><tr><td align="center">
<table role="presentation" width="480" cellpadding="0" cellspacing="0" style="width:480px;max-width:92%;background:#12161c;border:1px solid #232a33;border-radius:16px;overflow:hidden;">
<tr><td style="padding:28px 32px 8px 32px;">
<table role="presentation" cellpadding="0" cellspacing="0"><tr>
<td style="vertical-align:middle;"><img src="https://zorven.app/logo-email.png" width="34" height="34" alt="Zorven" style="display:block;width:34px;height:34px;border-radius:9px;border:0;"></td>
<td style="vertical-align:middle;padding-left:12px;font-family:Arial,Helvetica,sans-serif;font-size:19px;font-weight:700;color:#e6edf3;letter-spacing:-0.3px;">Zorven</td>
</tr></table></td></tr>
<tr><td style="padding:12px 32px 32px 32px;font-family:Arial,Helvetica,sans-serif;color:#e6edf3;">
` + inner + `
</td></tr></table>
<div style="font-family:Arial,Helvetica,sans-serif;font-size:11px;color:#8b98a5;padding:18px 0;">` + html.EscapeString(mailFooterText[lang]) + `</div>
</td></tr></table></body></html>`
}

// mailBoldHTML, kacisli kalin parca.
func mailBoldHTML(s string) string {
	return `<b style="color:#e6edf3;">` + html.EscapeString(s) + `</b>`
}

// mailButtonHTML, marka renkli dugme.
func mailButtonHTML(url, label string) string {
	return `<table role="presentation" cellpadding="0" cellspacing="0" style="margin:22px 0;"><tr><td style="border-radius:10px;background:#3ddc84;"><a href="` +
		html.EscapeString(url) + `" style="display:inline-block;padding:12px 26px;font-family:Arial,Helvetica,sans-serif;font-size:14px;font-weight:700;color:#0b0e12;text-decoration:none;border-radius:10px;">` +
		html.EscapeString(label) + `</a></td></tr></table>`
}

// mailFallbackHTML, "buton calismazsa" baglanti blogu.
func mailFallbackHTML(lang, url string) string {
	return `<p style="font-size:12px;color:#8b98a5;line-height:1.5;margin:6px 0 0 0;">` + html.EscapeString(mailFallbackText[mailLangOrTR(lang)]) +
		`<br><a href="` + html.EscapeString(url) + `" style="color:#3ddc84;word-break:break-all;">` + html.EscapeString(url) + `</a></p>`
}
