package api

import "fmt"

// Metrik uyarisi e-postasi (duz metin): tr / en / ja / zh. Dil, uyariyi kuran kullanicinin
// dilidir ve kurulum aninda tunnel_alerts.notify_lang'a yazilir (mail_i18n.go mailLangHint);
// kayit yoksa (eski uyarilar) tr.

// alertStrings, bir dilin uyari metni. Bicim argumanlari (explicit indeks):
// 1 tunel, 2 oran (tam sayi, metin), 3 oran (1 ondalik, metin), 4 pencere (dk), 5 istek,
// 6 hata, 7 esik (%), 8 platform alan adi. Yuzde isareti dile gore sayinin onunde ya da
// sonunda durur, bu yuzden sayilar metin olarak verilir.
type alertStrings struct {
	Subject string
	Body    string
}

var alertTexts = map[string]alertStrings{
	"tr": {
		Subject: "[Zorven] Uyarı: %[1]s hata oranı %%%[2]s",
		Body: "Zorven metrik uyarısı tetiklendi.\r\n\r\n" +
			"Tünel: %[1]s\r\n" +
			"Son %[4]d dakika: %[5]d istek, %[6]d hata (5xx)\r\n" +
			"Hata oranı: %%%[3]s (eşik: %%%[7]d)\r\n\r\n" +
			"Panelden inceleyin: https://panel.%[8]s\r\n",
	},
	"en": {
		Subject: "[Zorven] Alert: %[1]s error rate %[2]s%%",
		Body: "A Zorven metric alert was triggered.\r\n\r\n" +
			"Tunnel: %[1]s\r\n" +
			"Last %[4]d minutes: %[5]d requests, %[6]d errors (5xx)\r\n" +
			"Error rate: %[3]s%% (threshold: %[7]d%%)\r\n\r\n" +
			"Review it in the dashboard: https://panel.%[8]s\r\n",
	},
}

// alertMail, uyari e-postasinin konusunu ve duz metnini uretir (lang bilinmiyorsa tr).
func alertMail(lang, tunnelID string, windowMin int, total, errors int64, rate float64, thresholdPct int, domain string) (subject, body string) {
	t := alertTexts[mailLangOrTR(lang)]
	rate0 := fmt.Sprintf("%.0f", rate)
	rate1 := fmt.Sprintf("%.1f", rate)
	subject = fmt.Sprintf(t.Subject, tunnelID, rate0)
	body = fmt.Sprintf(t.Body, tunnelID, rate0, rate1, windowMin, total, errors, thresholdPct, domain)
	return subject, body
}
