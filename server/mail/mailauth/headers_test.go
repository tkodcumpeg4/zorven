package mailauth

import (
	"bytes"
	"strings"
	"testing"
)

// umlaut, IDN testleri icin "u" harfinin noktali halidir (kaynak ASCII kalsin diye
// rune'dan uretilir).
var umlaut = string(rune(0xfc))

func TestHeaderFieldFormat(t *testing.T) {
	ed := newEdKey(t)

	t.Run("spf_dkim_dmarc_pass", func(t *testing.T) {
		s := newScenario()
		s.fr.txt["example.com"] = []string{"v=spf1 ip4:203.0.113.7 -all"}
		s.fr.txt["_dmarc.example.com"] = []string{"v=DMARC1; p=reject"}
		s.fr.txt["sel._domainkey.example.com"] = []string{edTXT(ed)}
		s.msg = signed(t, s.msg, "example.com", "sel", ed)
		got := s.run().HeaderField("mail.zorven.app")
		want := "Authentication-Results: mail.zorven.app;\r\n" +
			"\tspf=pass (client-ip=203.0.113.7) smtp.mailfrom=bounce@example.com;\r\n" +
			"\tdkim=pass header.d=example.com;\r\n" +
			"\tdmarc=pass (p=reject dis=none) header.from=example.com\r\n"
		if got != want {
			t.Fatalf("baslik:\n%q\nbeklenen:\n%q", got, want)
		}
	})

	t.Run("dmarc_fail_reject", func(t *testing.T) {
		s := newScenario()
		s.fr.txt["example.com"] = []string{"v=spf1 -all"}
		s.fr.txt["_dmarc.example.com"] = []string{"v=DMARC1; p=reject"}
		got := s.run().HeaderField("mail.zorven.app")
		want := "Authentication-Results: mail.zorven.app;\r\n" +
			"\tspf=fail (client-ip=203.0.113.7) smtp.mailfrom=bounce@example.com;\r\n" +
			"\tdkim=none;\r\n" +
			"\tdmarc=fail (p=reject dis=reject) header.from=example.com\r\n"
		if got != want {
			t.Fatalf("baslik:\n%q\nbeklenen:\n%q", got, want)
		}
	})

	t.Run("dkim_fail_nedenli", func(t *testing.T) {
		s := newScenario()
		s.fr.txt["sel._domainkey.example.com"] = []string{edTXT(ed)}
		raw := signed(t, s.msg, "example.com", "sel", ed)
		s.msg = []byte(strings.Replace(string(raw), "govde satiri", "baska", 1))
		got := s.run().HeaderField("mail.zorven.app")
		if !strings.Contains(got, "\tdkim=fail (body hash did not verify) header.d=example.com;\r\n") {
			t.Fatalf("dkim=fail satiri yok:\n%s", got)
		}
	})

	t.Run("spf_atlandi_ve_dmarc_notu", func(t *testing.T) {
		s := newScenario()
		s.ip = nil
		s.fr.txt["_dmarc.example.com"] = []string{"v=DMARC1; p=reject"}
		got := s.run().HeaderField("mail.zorven.app")
		want := "Authentication-Results: mail.zorven.app;\r\n" +
			"\tspf=none (skipped: non-public client address) smtp.mailfrom=bounce@example.com;\r\n" +
			"\tdkim=none;\r\n" +
			"\tdmarc=none (p=reject spf skipped) header.from=example.com\r\n"
		if got != want {
			t.Fatalf("baslik:\n%q\nbeklenen:\n%q", got, want)
		}
	})

	t.Run("bos_gonderici_smtp_helo", func(t *testing.T) {
		s := newScenario()
		s.mailFrom = ""
		s.helo = "mx.sender.example"
		s.fr.txt["mx.sender.example"] = []string{"v=spf1 ip4:203.0.113.7 -all"}
		got := s.run().HeaderField("mail.zorven.app")
		if !strings.Contains(got, "\tspf=pass (client-ip=203.0.113.7) smtp.helo=mx.sender.example;\r\n") {
			t.Fatalf("smtp.helo satiri yok:\n%s", got)
		}
	})

	t.Run("dmarc_temperror", func(t *testing.T) {
		s := newScenario()
		s.fr.errs["_dmarc.example.com"] = errTempDNS
		got := s.run().HeaderField("mail.zorven.app")
		if !strings.Contains(got, "\tdmarc=temperror (dns lookup failed) header.from=example.com\r\n") {
			t.Fatalf("dmarc=temperror satiri yok:\n%s", got)
		}
	})

	t.Run("gecersiz_authserv_id_localhost_olur", func(t *testing.T) {
		got := (&Result{SPF: SPFResult{Result: None}, DKIM: []DKIMResult{{Result: None}}, DMARC: DMARCResult{Result: None}}).
			HeaderField("kotu id; dkim=pass")
		if !strings.HasPrefix(got, "Authentication-Results: localhost;\r\n") {
			t.Fatalf("baslik:\n%s", got)
		}
	})
}

// Gonderici denetimindeki degerler (MAIL FROM, HELO) basliga sonuc enjekte
// edemez: ";" / bosluk / tirnak / satir sonu iceren degerler yazilmaz.
func TestHeaderFieldSanitizesAttackerInput(t *testing.T) {
	hostile := []string{
		`"x; dkim=pass header.d=bank.example"@evil.example`,
		"a b@evil.example",
		"a(b)@evil.example",
		"x\r\nAuthentication-Results: mail.zorven.app; dkim=pass@evil.example",
		`x"y@evil.example`,
		"x;spf=pass@evil.example",
	}
	for _, mf := range hostile {
		s := newScenario()
		s.mailFrom = mf
		s.helo = "evil.example; dmarc=pass"
		r := s.run()
		hdr := r.HeaderField("mail.zorven.app")

		// Basliktaki yontem sonuclari tam olarak spf, dkim, dmarc olmalidir.
		unfolded := strings.ReplaceAll(strings.ReplaceAll(hdr, "\r\n\t", " "), "\r\n", "")
		parts := strings.Split(unfolded, ";")
		var methods []string
		for _, p := range parts[1:] {
			p = strings.TrimSpace(p)
			methods = append(methods, strings.SplitN(p, "=", 2)[0])
		}
		if strings.Join(methods, ",") != "spf,dkim,dmarc" {
			t.Errorf("mailfrom=%q: yontemler %v\n%s", mf, methods, hdr)
		}
		if strings.Count(hdr, "\r\n") != 4 || !strings.HasSuffix(hdr, "\r\n") {
			t.Errorf("mailfrom=%q: beklenmeyen satir sonu sayisi:\n%q", mf, hdr)
		}
		if strings.Contains(hdr, "dkim=pass") || strings.Contains(hdr, "dmarc=pass") {
			t.Errorf("mailfrom=%q: sahte sonuc enjekte edildi:\n%s", mf, hdr)
		}
	}
}

func TestSafeMailFrom(t *testing.T) {
	cases := map[string]string{
		"bounce@example.com":                       "bounce@example.com",
		"Bounce+tag=x@EXAMPLE.com":                 "Bounce+tag=x@example.com",
		"a.b-c_d@sub.example.com":                  "a.b-c_d@sub.example.com",
		`"quoted local"@example.com`:               "@example.com",
		"a b@example.com":                          "@example.com",
		"a;b@example.com":                          "@example.com",
		"a(b)@example.com":                         "@example.com",
		".baslangic.nokta@example.com":             "@example.com",
		"cift..nokta@example.com":                  "@example.com",
		"@example.com":                             "@example.com",
		"user@":                                    "",
		"user":                                     "",
		"user@exa mple.com":                        "",
		"user@" + strings.Repeat("a", 64) + ".com": "",
		strings.Repeat("a", 65) + "@example.com":   "@example.com",
	}
	for in, want := range cases {
		if got := safeMailFrom(in); got != want {
			t.Errorf("safeMailFrom(%q) = %q, beklenen %q", in, got, want)
		}
	}
}

func TestCleanDomain(t *testing.T) {
	cases := map[string]string{
		"Example.COM":                         "example.com",
		"example.com.":                        "example.com",
		"  mx.example.org ":                   "mx.example.org",
		"a-b.example.com":                     "a-b.example.com",
		"_dmarc.example.com":                  "_dmarc.example.com",
		"[203.0.113.7]":                       "",
		"exa mple.com":                        "",
		"-baslangic.example.com":              "",
		"bitis-.example.com":                  "",
		"cift..nokta.com":                     "",
		"":                                    "",
		"a;b.example.com":                     "",
		"munchen.example":                     "munchen.example",
		"m" + umlaut + "nchen.example":        "xn--mnchen-3ya.example",
		strings.Repeat("a", 64) + ".example":  "",
		strings.Repeat("a.", 130) + "example": "",
	}
	for in, want := range cases {
		if got := cleanDomain(in); got != want {
			t.Errorf("cleanDomain(%q) = %q, beklenen %q", in, got, want)
		}
	}
}

func TestCleanComment(t *testing.T) {
	if got := cleanComment("ok; (x) \"y\"\r\n z", 100); got != "ok? ?x? ?y??? z" {
		t.Errorf("cleanComment = %q", got)
	}
	if got := cleanComment(strings.Repeat("a", 500), 10); len(got) != 10 {
		t.Errorf("uzunluk siniri: %d", len(got))
	}
}

func TestAuthservIDOf(t *testing.T) {
	cases := map[string]string{
		" mail.zorven.app; spf=pass":                    "mail.zorven.app",
		" MAIL.Zorven.App.; spf=pass":                   "mail.zorven.app",
		" mail.zorven.app 1; spf=pass":                  "mail.zorven.app",
		" (yorum) mail.zorven.app; none":                "mail.zorven.app",
		" (ic (ice) yorum) \"mail.zorven.app\"; none":   "mail.zorven.app",
		"mail.zorven.app;":                              "mail.zorven.app",
		" other.example; dkim=pass":                     "other.example",
		" (kapanmamis yorum mail.zorven.app; dkim=pass": "",
		"":        "",
		" ; none": "",
	}
	for in, want := range cases {
		if got := authservIDOf(in); got != want {
			t.Errorf("authservIDOf(%q) = %q, beklenen %q", in, got, want)
		}
	}
}

func TestStripAuthResults(t *testing.T) {
	const id = "mail.zorven.app"

	t.Run("ayni_kimlikli_sahte_baslik_silinir", func(t *testing.T) {
		raw := "Received: from x\r\n" +
			"Authentication-Results: mail.zorven.app; dkim=pass header.d=bank.example\r\n" +
			"From: a@evil.example\r\n" +
			"\r\n" +
			"Authentication-Results: mail.zorven.app; govdedeki satir korunur\r\n"
		got := string(StripAuthResults([]byte(raw), id))
		want := "Received: from x\r\n" +
			"From: a@evil.example\r\n" +
			"\r\n" +
			"Authentication-Results: mail.zorven.app; govdedeki satir korunur\r\n"
		if got != want {
			t.Fatalf("got:\n%q\nwant:\n%q", got, want)
		}
	})

	t.Run("katlanmis_baslik_butunuyle_silinir", func(t *testing.T) {
		raw := "Authentication-Results: mail.zorven.app;\r\n" +
			"\tspf=pass smtp.mailfrom=a@b.example;\r\n" +
			" dkim=pass header.d=b.example\r\n" +
			"Subject: merhaba\r\n" +
			"\r\ngovde\r\n"
		got := string(StripAuthResults([]byte(raw), id))
		if want := "Subject: merhaba\r\n\r\ngovde\r\n"; got != want {
			t.Fatalf("got:\n%q\nwant:\n%q", got, want)
		}
	})

	t.Run("buyuk_kucuk_harf_ve_bosluklu_ad", func(t *testing.T) {
		raw := "authentication-results :  MAIL.ZORVEN.APP ; dkim=pass\r\n" +
			"AUTHENTICATION-RESULTS: (c) Mail.Zorven.App.; spf=pass\r\n" +
			"Subject: x\r\n\r\n"
		got := string(StripAuthResults([]byte(raw), id))
		if want := "Subject: x\r\n\r\n"; got != want {
			t.Fatalf("got:\n%q", got)
		}
	})

	t.Run("baska_kimlikler_korunur", func(t *testing.T) {
		raw := "Authentication-Results: mx.google.com; dkim=pass\r\n" +
			"Authentication-Results: mail.zorven.app; dkim=pass\r\n" +
			"Authentication-Results: mail.zorven.app.evil.example; dkim=pass\r\n" +
			"Subject: x\r\n\r\n"
		got := string(StripAuthResults([]byte(raw), id))
		want := "Authentication-Results: mx.google.com; dkim=pass\r\n" +
			"Authentication-Results: mail.zorven.app.evil.example; dkim=pass\r\n" +
			"Subject: x\r\n\r\n"
		if got != want {
			t.Fatalf("got:\n%q", got)
		}
	})

	t.Run("birden_cok_kimlik", func(t *testing.T) {
		raw := "Authentication-Results: zorven.app; dkim=pass\r\n" +
			"Authentication-Results: mail.zorven.app; dkim=pass\r\n" +
			"Subject: x\r\n\r\n"
		got := string(StripAuthResults([]byte(raw), id, "zorven.app"))
		if want := "Subject: x\r\n\r\n"; got != want {
			t.Fatalf("got:\n%q", got)
		}
	})

	t.Run("authserv_id_katlanmis_ikinci_satirda", func(t *testing.T) {
		raw := "Authentication-Results:\r\n" +
			" mail.zorven.app;\r\n" +
			" dkim=pass header.d=bank.example\r\n" +
			"Subject: x\r\n\r\n"
		got := string(StripAuthResults([]byte(raw), id))
		if want := "Subject: x\r\n\r\n"; got != want {
			t.Fatalf("got:\n%q", got)
		}
	})

	t.Run("yalin_LF_satir_sonlari", func(t *testing.T) {
		raw := "Authentication-Results: mail.zorven.app; dkim=pass\n\tdevam\nSubject: x\n\ngovde\n"
		got := string(StripAuthResults([]byte(raw), id))
		if want := "Subject: x\n\ngovde\n"; got != want {
			t.Fatalf("got:\n%q", got)
		}
	})

	t.Run("degisiklik_yoksa_ayni_dilim", func(t *testing.T) {
		raw := []byte("Subject: x\r\n\r\ngovde\r\n")
		got := StripAuthResults(raw, id)
		if &got[0] != &raw[0] || len(got) != len(raw) {
			t.Fatal("degisiklik yokken yeni dilim uretildi")
		}
		if got := StripAuthResults(raw); &got[0] != &raw[0] {
			t.Fatal("kimlik verilmediginde dilim degisti")
		}
	})
}

func TestPrepend(t *testing.T) {
	s := newScenario()
	s.fr.txt["example.com"] = []string{"v=spf1 ip4:203.0.113.7 -all"}
	body := "govde satiri\r\n"
	s.msg = []byte("Authentication-Results: mail.zorven.app; dkim=pass header.d=example.com\r\n" +
		"Authentication-Results: upstream.example; spf=pass\r\n" +
		"From: Alice <alice@example.com>\r\n" +
		"Subject: test\r\n" +
		"\r\n" + body)
	r := s.run()
	got := Prepend(s.msg, "mail.zorven.app", r)

	if !bytes.HasPrefix(got, []byte("Authentication-Results: mail.zorven.app;\r\n\tspf=pass")) {
		t.Fatalf("baslik en ustte degil:\n%s", got)
	}
	if n := bytes.Count(got, []byte("Authentication-Results: mail.zorven.app")); n != 1 {
		t.Fatalf("kendi kimligimizle %d baslik var, 1 bekleniyordu:\n%s", n, got)
	}
	if bytes.Contains(got, []byte("dkim=pass header.d=example.com")) {
		t.Fatalf("sahte dkim=pass silinmedi:\n%s", got)
	}
	if !bytes.Contains(got, []byte("Authentication-Results: upstream.example; spf=pass\r\n")) {
		t.Fatalf("baska kimlikli baslik korunmadi:\n%s", got)
	}
	if !bytes.HasSuffix(got, []byte("\r\n\r\n"+body)) {
		t.Fatalf("govde degisti:\n%q", got)
	}

	// Prepend, girdi dilimini degistirmez.
	if !bytes.HasPrefix(s.msg, []byte("Authentication-Results: mail.zorven.app; dkim=pass")) {
		t.Fatal("girdi dilimi degisti")
	}

	// Basliksiz (yalnizca govde) ileti de gecerli bir baslik bolumu alir.
	only := Prepend([]byte("\r\ngovde\r\n"), "mail.zorven.app", r)
	if !bytes.HasSuffix(only, []byte("\r\n\r\ngovde\r\n")) || !bytes.HasPrefix(only, []byte("Authentication-Results:")) {
		t.Fatalf("basliksiz ileti:\n%q", only)
	}
}

func TestParseFrom(t *testing.T) {
	cases := []struct {
		name string
		raw  string
		dom  string
		err  string
	}{
		{"basit", "From: a@example.com\r\n\r\n", "example.com", ""},
		{"gorunen_ad_ve_buyuk_harf", "From: \"Ali, Veli\" <Ali@EXAMPLE.com>\r\n\r\n", "example.com", ""},
		{"katlanmis", "From: Ali\r\n <ali@example.com>\r\n\r\n", "example.com", ""},
		{"ayni_alan_adli_iki_adres", "From: a@example.com, b@example.com\r\n\r\n", "example.com", ""},
		{"farkli_alan_adlari", "From: a@example.com, b@example.org\r\n\r\n", "", "multiple From domains"},
		{"iki_from_basligi", "From: a@example.com\r\nFrom: b@example.org\r\n\r\n", "", "multiple From headers"},
		{"from_yok", "Subject: x\r\n\r\n", "", "no From header"},
		{"bozuk", "From: bu bir adres degil\r\n\r\n", "", "unparseable From header"},
		{"bos_grup", "From: undisclosed-recipients:;\r\n\r\n", "", "unparseable From header"},
		{"ad_kucuk_harf_baslik", "from: a@example.com\r\n\r\n", "example.com", ""},
		{"govdedeki_from_sayilmaz", "Subject: x\r\n\r\nFrom: a@example.com\r\n", "", "no From header"},
		{"idn", "From: a@m" + umlaut + "nchen.example\r\n\r\n", "xn--mnchen-3ya.example", ""},
		{"yalin_LF", "From: a@example.com\nSubject: x\n\ngovde\n", "example.com", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := parseFrom([]byte(tc.raw))
			if got.domain != tc.dom || got.err != tc.err {
				t.Fatalf("parseFrom = %+v, beklenen domain=%q err=%q", got, tc.dom, tc.err)
			}
		})
	}
}

func TestScanHeaderTolerance(t *testing.T) {
	// Iki noktasiz satir ve ilk satirda bosluk alan olusturur ama hicbir
	// adla eslesmez; tarama durmaz.
	raw := []byte(" ilk satir bosluklu\r\nFrom bu satirda iki nokta yok\r\nSubject: x\r\n\r\ngovde")
	fs := scanHeader(raw)
	if len(fs) != 3 {
		t.Fatalf("alan sayisi = %d (%+v)", len(fs), fs)
	}
	if fs[2].name != "Subject" {
		t.Fatalf("Subject bulunamadi: %+v", fs)
	}
	// Cok uzun devam satiri zinciri tek alandir ve dogrusal surede taranir.
	long := "X-Uzun: a\r\n" + strings.Repeat(" b\r\n", 200000) + "From: z@example.com\r\n\r\n"
	fs = scanHeader([]byte(long))
	if len(fs) != 2 || fs[1].name != "From" {
		t.Fatalf("uzun zincir: %d alan", len(fs))
	}
}
