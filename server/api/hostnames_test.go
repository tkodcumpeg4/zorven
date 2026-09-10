package api

import "testing"

// "--" yasagi bir GUVENLIK kuralidir: kiraci ayraci odur (<tunel>--<kiraci>).
// Serbest birakilirsa bir kullanici "api--baskakiraci" alip baska kiracinin
// kapsamli adini taklit edebilir.
func TestValidateLabelRejectsTenantSeparator(t *testing.T) {
	for _, bad := range []string{
		"api--acme", // dogrudan taklit
		"a--b",
		"x--",   // sonda
		"--x",   // basta
		"a---b", // uc tire de "--" icerir
	} {
		if err := validateLabel(bad); err == nil {
			t.Errorf("validateLabel(%q) reddetmeliydi", bad)
		}
	}
}

func TestValidateLabel(t *testing.T) {
	gecerli := []string{"api", "a", "benim-projem", "x1", "1a", "a-b-c"}
	for _, s := range gecerli {
		if err := validateLabel(s); err != nil {
			t.Errorf("validateLabel(%q) kabul etmeliydi: %v", s, err)
		}
	}

	gecersiz := []string{
		"",            // bos
		"-api",        // tire ile baslar
		"api-",        // tire ile biter
		"API",         // buyuk harf (cagiran once kucultmeli)
		"api.example", // nokta: tek ETIKET olmali
		"api_x",       // alt cizgi DNS'te gecerli degil
		"api example", // bosluk
		"*",           // joker
		"_acme-challenge",
	}
	for _, s := range gecersiz {
		if err := validateLabel(s); err == nil {
			t.Errorf("validateLabel(%q) reddetmeliydi", s)
		}
	}

	// RFC 1123: etiket en fazla 63 karakter.
	uzun := make([]byte, 64)
	for i := range uzun {
		uzun[i] = 'a'
	}
	if err := validateLabel(string(uzun)); err == nil {
		t.Error("64 karakterlik etiket reddedilmeliydi")
	}
	if err := validateLabel(string(uzun[:63])); err != nil {
		t.Errorf("63 karakterlik etiket kabul edilmeliydi: %v", err)
	}
}

// Kapsamli ad TEK ETIKET olmali: noktali bir bicim kiraci basina ayri wildcard
// sertifika gerektirir ve Let's Encrypt'in haftalik sinirina takilir.
func TestScopedFQDNIsSingleLabel(t *testing.T) {
	const platform = ".rpshell.app"
	got := scopedFQDN("api", "acme", "rpshell.app")
	if want := "api--acme" + platform; got != want {
		t.Fatalf("scopedFQDN = %q, beklenen %q", got, want)
	}

	// Platform domaini disindaki kisimda nokta OLMAMALI.
	etiket := got[:len(got)-len(platform)]
	for _, c := range etiket {
		if c == '.' {
			t.Fatalf("kapsamli ad birden fazla etiket iceriyor: %q", got)
		}
	}
}

// Iki kiraci ayni tunel adini kullanabilmeli; kapsamli adlar CAKISMAMALI.
func TestScopedFQDNSeparatesTenants(t *testing.T) {
	a := scopedFQDN("api", "alice", "rpshell.app")
	b := scopedFQDN("api", "bob", "rpshell.app")
	if a == b {
		t.Fatalf("iki kiraci ayni adi aldi: %q", a)
	}
}
