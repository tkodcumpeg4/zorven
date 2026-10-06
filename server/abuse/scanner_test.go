package abuse

import "testing"

func TestScanFQDN(t *testing.T) {
	cases := []struct {
		name    string
		fqdn    string
		wantSus bool
	}{
		// Meşru / zararsız — işaretlenMEmeli.
		{"basit hobi", "myblog.zorven.app", false},
		{"api", "api.acme.zorven.app", false},
		{"tek marka olmayan kelime", "login.zorven.app", false}, // tek deception, marka yok
		{"proje adı", "finagent.zorven.app", false},
		{"rastgele", "x7f2q.zorven.app", false},

		// Phishing örüntüleri — işaretlenMELİ.
		{"marka+deception", "paypal-login.zorven.app", true},
		{"apple id", "secure-apple-id-verify.zorven.app", true},
		{"banka tr", "garanti-giris-dogrula.zorven.app", true},
		{"kripto", "connect-wallet.zorven.app", true},
		{"seed", "metamask-seed-phrase.zorven.app", true},
		{"çoklu deception + marka", "microsoft-account-verify.zorven.app", true},
		{"standalone", "kyc-verify.zorven.app", true},

		// Custom domain (platformDomain'siz).
		{"custom phishing", "login.paypal-secure.example.com", true},
		{"custom meşru", "shop.example.com", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			pd := "zorven.app"
			// Custom domain testleri example.com ile bitiyor; platform sonekini
			// uygulamak zarar vermez ama gerçekçi olması için boş geç.
			if !hasSuffix(c.fqdn, "zorven.app") {
				pd = ""
			}
			got := ScanFQDN(c.fqdn, pd)
			if got.Suspicious != c.wantSus {
				t.Errorf("ScanFQDN(%q) suspicious=%v (score=%d, reason=%q); istenen %v",
					c.fqdn, got.Suspicious, got.Score, got.Reason, c.wantSus)
			}
		})
	}
}

func hasSuffix(s, suf string) bool {
	return len(s) >= len(suf) && s[len(s)-len(suf):] == suf
}
