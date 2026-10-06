// Package abuse, halka açık *.zorven.app hostname'leri için hafif, bağımlılıksız
// bir kötüye kullanım (phishing) sezgisel tarayıcısı sağlar. Amaç, yeni bir
// hostname oluşturulurken adın phishing örüntülerine uyup uymadığını hızlıca
// değerlendirip şüpheli olanları platform admin incelemesi için işaretlemektir.
//
// Bu tarayıcı KESİN karar vermez: yalnızca sinyal üretir. Otomatik dondurma YOK;
// yanlış-pozitif bir adın hemen kilitlenmesini önlemek için karar admin'e bırakılır
// (bkz. FAZ 4 — panel abuse UI). Harici feed/servis gerektirmez.
package abuse

import (
	"fmt"
	"strings"
)

// brandKeywords, phishing'de en sık taklit edilen marka/servis adları. Bir hostname
// bu adlardan birini + bir aldatma kelimesini (deceptionWords) içeriyorsa şüpheli
// sayılır (ör. "paypal-login", "secure-apple-id").
var brandKeywords = []string{
	"paypal", "apple", "icloud", "microsoft", "office365", "outlook", "google",
	"gmail", "amazon", "netflix", "facebook", "instagram", "whatsapp", "linkedin",
	"binance", "coinbase", "metamask", "trustwallet", "ledger", "blockchain",
	"bankofamerica", "wellsfargo", "chase", "citibank", "hsbc", "revolut",
	"steam", "discord", "roblox", "dhl", "fedex", "ups", "usps",
	// TR bankaları / servisleri
	"garanti", "akbank", "yapikredi", "isbank", "ziraat", "vakifbank", "denizbank",
	"halkbank", "ptt", "turkcell", "vodafone", "eturkiye", "edevlet", "hepsiburada",
	"trendyol", "n11", "papara", "ininal",
}

// deceptionWords, kimlik-avı sayfalarında sık geçen "harekete geçir/güven ver"
// kelimeleri. Tek başına zararsız; bir markayla birleşince sinyal güçlenir.
var deceptionWords = []string{
	"login", "signin", "sign-in", "verify", "verification", "verified", "secure",
	"security", "account", "update", "confirm", "wallet", "billing", "invoice",
	"password", "unlock", "recover", "recovery", "support", "auth", "authenticate",
	"activate", "validation", "giris", "dogrula", "guvenli", "hesap", "sifre",
	"odeme", "fatura", "kimlik", "onay",
}

// standaloneSuspicious, tek başına bile yüksek şüphe taşıyan kalıplar. Genelde
// meşru bir hobi projesi bu kelimeleri subdomain'ine koymaz.
var standaloneSuspicious = []string{
	"webscr", "cgi-bin", "phishing", "free-gift", "giftcard", "airdrop-claim",
	"claim-reward", "seed-phrase", "connect-wallet", "kyc-verify",
}

// Result, bir taramanın çıktısı.
type Result struct {
	// Suspicious, hostname'in incelenmeye değer bulunup bulunmadığı.
	Suspicious bool
	// Score, kaba bir güven puanı (yüksek = daha şüpheli). Sıralama/eşik için.
	Score int
	// Reason, admin'e gösterilecek insan-okur açıklama (TR).
	Reason string
}

// splitLabels, fqdn'i alfasayısal olmayan ayraçlardan bölerek kelime listesi verir
// (ör. "secure-paypal.login.zorven.app" → [secure paypal login zorven app]).
func splitLabels(s string) []string {
	return strings.FieldsFunc(strings.ToLower(s), func(r rune) bool {
		return !(r >= 'a' && r <= 'z') && !(r >= '0' && r <= '9')
	})
}

// ScanFQDN, verilen tam alan adını sezgisel olarak tarar. platformDomain, kendi
// platform son ekimizdir (ör. "zorven.app"); adın içindeki bu etiketler skorlamaya
// katılmaz. Custom domain'lerde platformDomain boş geçilebilir.
func ScanFQDN(fqdn, platformDomain string) Result {
	fqdn = strings.ToLower(strings.TrimSuffix(strings.TrimSpace(fqdn), "."))
	if fqdn == "" {
		return Result{}
	}

	// Ziyaretçiye görünen kısım: platform son ekini at, yalnızca kiracının seçtiği
	// etiketleri incele (ör. "paypal-login" kısmı, "zorven.app" değil).
	subject := fqdn
	if platformDomain != "" {
		subject = strings.TrimSuffix(subject, "."+strings.ToLower(platformDomain))
	}

	labels := splitLabels(subject)
	labelSet := make(map[string]struct{}, len(labels))
	for _, l := range labels {
		labelSet[l] = struct{}{}
	}
	joined := strings.Join(labels, "")

	score := 0
	var reasons []string

	// 1) Tek başına yüksek-şüphe kalıpları.
	for _, p := range standaloneSuspicious {
		if strings.Contains(joined, strings.ReplaceAll(p, "-", "")) {
			score += 3
			reasons = append(reasons, "şüpheli kalıp: "+p)
		}
	}

	// 2) Marka adı geçiyor mu?
	var hitBrands []string
	for _, b := range brandKeywords {
		if strings.Contains(joined, b) {
			hitBrands = append(hitBrands, b)
		}
	}
	// 3) Aldatma kelimesi geçiyor mu?
	var hitWords []string
	for _, wd := range deceptionWords {
		w := strings.ReplaceAll(wd, "-", "")
		if _, ok := labelSet[w]; ok {
			hitWords = append(hitWords, wd)
			continue
		}
		if strings.Contains(joined, w) {
			hitWords = append(hitWords, wd)
		}
	}

	// Marka + aldatma birlikte → klasik phishing imzası (yüksek sinyal).
	if len(hitBrands) > 0 && len(hitWords) > 0 {
		score += 4
		reasons = append(reasons, fmt.Sprintf("marka taklidi + aldatma sözcüğü: %s + %s",
			strings.Join(hitBrands, ","), strings.Join(hitWords, ",")))
	} else if len(hitBrands) > 0 {
		// Sadece marka adı: tek başına şüphe (marka sahibi değilse taklit olası).
		score += 2
		reasons = append(reasons, "taklit edilen marka adı içeriyor: "+strings.Join(hitBrands, ","))
	}

	// 4) Çok sayıda aldatma kelimesi tek başına da sinyal (ör. "secure-login-verify").
	if len(hitWords) >= 2 {
		score += 2
		reasons = append(reasons, "birden çok aldatma sözcüğü: "+strings.Join(hitWords, ","))
	}

	res := Result{Score: score}
	if score >= 3 {
		res.Suspicious = true
		res.Reason = "otomatik tarama (" + fmt.Sprintf("puan %d", score) + "): " + strings.Join(reasons, "; ")
	}
	return res
}
