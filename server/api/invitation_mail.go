package api

import (
	"fmt"
	"html"
)

// Ekip daveti e-postasi: tr / en / ja / zh. Dil, daveti GONDERENIN dilidir
// (mail_i18n.go mailLangFor: kayitli tercih > panel dili cerezi > Accept-Language > tr).
// Gorsel tasarim web/server/lib/email-templates.ts ile aynidir (mailShellHTML).

// invitationStrings, bir dilin davet metinleri. %[n]s sirali bicim argumanlari:
// 1 = davet eden, 2 = organizasyon adi, 3 = rol; Accept/HTMLAccept icin 1 = davetli e-posta.
// Argumanlarin siralamasi dile gore degisebilir (explicit indeks).
type invitationStrings struct {
	Hello      string
	Subject    string // %[1]s = organizasyon adi
	DefaultWho string // davet eden adi bilinmiyorsa
	RoleMember string
	RoleAdmin  string
	TextLead   string // duz metin cumle (organizasyon tirnakli)
	TextAccept string // baglantidan onceki satir (%[1]s = e-posta)
	Expiry     string
	Title      string
	HTMLLead   string // HTML cumle (kalin parcalarla)
	HTMLAccept string // %[1]s = e-posta (kalin)
	Button     string
}

var invitationTexts = map[string]invitationStrings{
	"tr": {
		Hello:      "Merhaba,",
		Subject:    "Zorven — %[1]s ekibine davet edildin",
		DefaultWho: "Bir ekip yöneticisi",
		RoleMember: "üye",
		RoleAdmin:  "yönetici",
		TextLead:   `%[1]s seni Zorven'deki "%[2]s" organizasyonuna %[3]s olarak davet etti.`,
		TextAccept: "Daveti kabul etmek için bu adrese gidip %[1]s adresine ait hesabınla giriş yap (hesabın yoksa ücretsiz oluştur):",
		Expiry:     "Davet 7 gün geçerlidir. Bu daveti beklemiyorsan e-postayı yok sayabilirsin.",
		Title:      "Ekibe davet edildin",
		HTMLLead:   "%[1]s seni %[2]s organizasyonuna %[3]s olarak davet etti.",
		HTMLAccept: "Kabul etmek için %[1]s adresine ait hesabınla giriş yap. Hesabın yoksa bu adresle ücretsiz oluşturabilirsin.",
		Button:     "Daveti Görüntüle",
	},
	"en": {
		Hello:      "Hi,",
		Subject:    "Zorven — You've been invited to join %[1]s",
		DefaultWho: "A team admin",
		RoleMember: "a member",
		RoleAdmin:  "an admin",
		TextLead:   `%[1]s invited you to join the "%[2]s" organization on Zorven as %[3]s.`,
		TextAccept: "To accept the invitation, go to this address and sign in with the account for %[1]s (if you don't have one, create one for free):",
		Expiry:     "The invitation is valid for 7 days. If you weren't expecting it, you can ignore this email.",
		Title:      "You've been invited to a team",
		HTMLLead:   "%[1]s invited you to join the %[2]s organization as %[3]s.",
		HTMLAccept: "To accept, sign in with the account for %[1]s. If you don't have one, you can create one for free with this address.",
		Button:     "View invitation",
	},
}

// invitationMail, davet e-postasinin konusunu, duz metnini (CRLF) ve HTML'ini uretir.
// inviter bos olabilir (dile gore "bir ekip yoneticisi" denir); role "admin" ya da "member".
func invitationMail(lang, inviter, org, role, email, url string) (subject, text, htmlBody string) {
	lang = mailLangOrTR(lang)
	t := invitationTexts[lang]
	who := inviter
	if who == "" {
		who = t.DefaultWho
	}
	roleLabel := t.RoleMember
	if role == "admin" {
		roleLabel = t.RoleAdmin
	}

	subject = fmt.Sprintf(t.Subject, org)
	text = t.Hello + "\r\n\r\n" +
		fmt.Sprintf(t.TextLead, who, org, roleLabel) + "\r\n\r\n" +
		fmt.Sprintf(t.TextAccept, email) + "\r\n" + url + "\r\n\r\n" +
		t.Expiry + "\r\n"

	// HTML: sozluk metni once kacislanir (apostrof vb.), sonra kalin parcalar yerlestirilir.
	e := html.EscapeString
	inner := `<h1 style="margin:0 0 10px 0;font-size:20px;font-weight:700;color:#e6edf3;">` + e(t.Title) + `</h1>
<p style="margin:0;font-size:14px;line-height:1.6;color:#8b98a5;">` +
		fmt.Sprintf(e(t.HTMLLead), mailBoldHTML(who), mailBoldHTML(org), mailBoldHTML(roleLabel)) + `</p>
<p style="margin:10px 0 0 0;font-size:14px;line-height:1.6;color:#8b98a5;">` +
		fmt.Sprintf(e(t.HTMLAccept), mailBoldHTML(email)) + `</p>
` + mailButtonHTML(url, t.Button) + `
<p style="margin:0;font-size:12px;line-height:1.6;color:#8b98a5;">` + e(t.Expiry) + `</p>
` + mailFallbackHTML(lang, url)
	return subject, text, mailShellHTML(lang, inner)
}
