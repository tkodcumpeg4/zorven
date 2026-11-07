package mailauth

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"net"
	"strings"
	"testing"
	"time"
)

// Bu dosyadaki testler ag kullanmaz: tum DNS fakeResolver uzerinden gecer.

var testIP = net.ParseIP("203.0.113.7")

func newVerifier(r Resolver) *Verifier {
	return &Verifier{Resolver: r, Timeout: 3 * time.Second}
}

func TestSPFResults(t *testing.T) {
	cases := []struct {
		name     string
		txt      map[string][]string
		errs     map[string]error
		mailFrom string
		helo     string
		ip       net.IP
		want     string
		identity string
		skipped  bool
	}{
		{
			name:     "pass",
			txt:      map[string][]string{"example.com": {"v=spf1 ip4:203.0.113.7 -all"}},
			mailFrom: "bounce@example.com", helo: "mx.example.com", ip: testIP,
			want: Pass, identity: "mailfrom",
		},
		{
			name:     "pass_cidr_ve_buyuk_harf",
			txt:      map[string][]string{"example.com": {"v=spf1 ip4:203.0.113.0/24 -all"}},
			mailFrom: "Bounce@EXAMPLE.com", helo: "mx.example.com", ip: testIP,
			want: Pass, identity: "mailfrom",
		},
		{
			name:     "fail",
			txt:      map[string][]string{"example.com": {"v=spf1 ip4:198.51.100.1 -all"}},
			mailFrom: "bounce@example.com", helo: "mx.example.com", ip: testIP,
			want: Fail, identity: "mailfrom",
		},
		{
			name:     "softfail",
			txt:      map[string][]string{"example.com": {"v=spf1 ip4:198.51.100.1 ~all"}},
			mailFrom: "bounce@example.com", helo: "mx.example.com", ip: testIP,
			want: SoftFail, identity: "mailfrom",
		},
		{
			name:     "neutral",
			txt:      map[string][]string{"example.com": {"v=spf1 ?all"}},
			mailFrom: "bounce@example.com", helo: "mx.example.com", ip: testIP,
			want: Neutral, identity: "mailfrom",
		},
		{
			name:     "none_kayit_yok",
			txt:      map[string][]string{},
			mailFrom: "bounce@example.com", helo: "mx.example.com", ip: testIP,
			want: None, identity: "mailfrom",
		},
		{
			name:     "none_txt_var_spf_yok",
			txt:      map[string][]string{"example.com": {"google-site-verification=abc"}},
			mailFrom: "bounce@example.com", helo: "mx.example.com", ip: testIP,
			want: None, identity: "mailfrom",
		},
		{
			name:     "temperror_gecici_dns",
			errs:     map[string]error{"example.com": errTempDNS},
			mailFrom: "bounce@example.com", helo: "mx.example.com", ip: testIP,
			want: TempError, identity: "mailfrom",
		},
		{
			name:     "temperror_zaman_asimi",
			errs:     map[string]error{"example.com": errTimeout},
			mailFrom: "bounce@example.com", helo: "mx.example.com", ip: testIP,
			want: TempError, identity: "mailfrom",
		},
		{
			name: "permerror_birden_cok_spf",
			txt: map[string][]string{"example.com": {
				"v=spf1 ip4:203.0.113.7 -all", "v=spf1 -all"}},
			mailFrom: "bounce@example.com", helo: "mx.example.com", ip: testIP,
			want: PermError, identity: "mailfrom",
		},
		{
			name:     "permerror_bilinmeyen_terim",
			txt:      map[string][]string{"example.com": {"v=spf1 bogus -all"}},
			mailFrom: "bounce@example.com", helo: "mx.example.com", ip: testIP,
			want: PermError, identity: "mailfrom",
		},
		{
			name: "include_ile_pass",
			txt: map[string][]string{
				"example.com":         {"v=spf1 include:_spf.sender.example -all"},
				"_spf.sender.example": {"v=spf1 ip4:203.0.113.0/24 -all"},
			},
			mailFrom: "bounce@example.com", helo: "mx.example.com", ip: testIP,
			want: Pass, identity: "mailfrom",
		},
		{
			name:     "ipv6_pass",
			txt:      map[string][]string{"example.com": {"v=spf1 ip6:2001:db8::/32 -all"}},
			mailFrom: "bounce@example.com", helo: "mx.example.com", ip: net.ParseIP("2001:db8::25"),
			want: Pass, identity: "mailfrom",
		},
		{
			name:     "bos_gonderici_helo_kullanilir_pass",
			txt:      map[string][]string{"mx.example.org": {"v=spf1 ip4:203.0.113.7 -all"}},
			mailFrom: "", helo: "mx.example.org", ip: testIP,
			want: Pass, identity: "helo",
		},
		{
			name:     "bos_gonderici_helo_fail",
			txt:      map[string][]string{"mx.example.org": {"v=spf1 -all"}},
			mailFrom: "", helo: "mx.example.org", ip: testIP,
			want: Fail, identity: "helo",
		},
		{
			name:     "bos_gonderici_gecersiz_helo_none",
			mailFrom: "", helo: "[203.0.113.7]", ip: testIP,
			want: None, identity: "helo",
		},
		{
			name:     "alan_adsiz_gonderici_helo_kullanilir",
			txt:      map[string][]string{"mx.example.org": {"v=spf1 ip4:203.0.113.7 -all"}},
			mailFrom: "postmaster", helo: "mx.example.org", ip: testIP,
			want: Pass, identity: "helo",
		},
		{
			name:     "gecersiz_gonderici_alan_adi_none",
			mailFrom: "a@exa mple.com", helo: "mx.example.org", ip: testIP,
			want: None, identity: "mailfrom",
		},
		{
			name:     "ozel_ip_atlanir",
			txt:      map[string][]string{"example.com": {"v=spf1 -all"}},
			mailFrom: "bounce@example.com", helo: "mx.example.com", ip: net.ParseIP("10.1.2.3"),
			want: None, identity: "mailfrom", skipped: true,
		},
		{
			name:     "loopback_atlanir",
			txt:      map[string][]string{"example.com": {"v=spf1 -all"}},
			mailFrom: "bounce@example.com", helo: "mx.example.com", ip: net.ParseIP("127.0.0.1"),
			want: None, identity: "mailfrom", skipped: true,
		},
		{
			name:     "ip_yok_atlanir",
			txt:      map[string][]string{"example.com": {"v=spf1 -all"}},
			mailFrom: "bounce@example.com", helo: "mx.example.com", ip: nil,
			want: None, identity: "mailfrom", skipped: true,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			fr := newFakeResolver()
			for k, v := range tc.txt {
				fr.txt[k] = v
			}
			for k, v := range tc.errs {
				fr.errs[k] = v
			}
			v := newVerifier(fr)
			got := v.checkSPF(context.Background(), boundedResolver{fr}, Input{
				ClientIP: tc.ip, HELO: tc.helo, MailFrom: tc.mailFrom,
			})
			if got.Result != tc.want {
				t.Fatalf("SPF = %q (%+v), beklenen %q", got.Result, got, tc.want)
			}
			if got.Identity != tc.identity {
				t.Fatalf("kimlik = %q, beklenen %q", got.Identity, tc.identity)
			}
			if got.Skipped != tc.skipped {
				t.Fatalf("Skipped = %v, beklenen %v", got.Skipped, tc.skipped)
			}
		})
	}
}

func TestSPFMXMekanizmasi(t *testing.T) {
	// "mx" mekanizmasi LookupMX + LookupIPAddr yolunu da cozumleyiciden gecirir.
	fr := newFakeResolver()
	fr.txt["example.com"] = []string{"v=spf1 mx -all"}
	fr.mx["example.com"] = []*net.MX{{Host: "mail.example.com.", Pref: 10}}
	fr.ips["mail.example.com."] = []net.IPAddr{{IP: testIP}}
	fr.ips["mail.example.com"] = []net.IPAddr{{IP: testIP}}
	v := newVerifier(fr)
	got := v.checkSPF(context.Background(), boundedResolver{fr}, Input{
		ClientIP: testIP, HELO: "mx.example.com", MailFrom: "a@example.com",
	})
	if got.Result != Pass {
		t.Fatalf("mx mekanizmasi: %+v", got)
	}
}

func TestDKIMResults(t *testing.T) {
	ed := newEdKey(t)

	t.Run("pass_ed25519", func(t *testing.T) {
		fr := newFakeResolver()
		fr.txt["sel._domainkey.example.com"] = []string{edTXT(ed)}
		raw := signed(t, testMsg("alice@example.com", ""), "example.com", "sel", ed)
		got := newVerifier(fr).checkDKIM(context.Background(), boundedResolver{fr}, raw)
		if len(got) != 1 || got[0].Result != Pass || got[0].Domain != "example.com" {
			t.Fatalf("sonuc: %+v", got)
		}
	})

	t.Run("pass_rsa2048", func(t *testing.T) {
		key, err := rsa.GenerateKey(rand.Reader, 2048)
		if err != nil {
			t.Fatal(err)
		}
		fr := newFakeResolver()
		fr.txt["rsa._domainkey.example.com"] = []string{rsaTXT(t, key)}
		raw := signed(t, testMsg("alice@example.com", ""), "example.com", "rsa", key)
		got := newVerifier(fr).checkDKIM(context.Background(), boundedResolver{fr}, raw)
		if len(got) != 1 || got[0].Result != Pass {
			t.Fatalf("sonuc: %+v", got)
		}
	})

	t.Run("fail_govde_degistirildi", func(t *testing.T) {
		fr := newFakeResolver()
		fr.txt["sel._domainkey.example.com"] = []string{edTXT(ed)}
		raw := signed(t, testMsg("alice@example.com", ""), "example.com", "sel", ed)
		raw = []byte(strings.Replace(string(raw), "govde satiri", "DEGISTIRILMIS satir", 1))
		got := newVerifier(fr).checkDKIM(context.Background(), boundedResolver{fr}, raw)
		if len(got) != 1 || got[0].Result != Fail || got[0].Reason != "body hash did not verify" {
			t.Fatalf("sonuc: %+v", got)
		}
	})

	t.Run("fail_baslik_degistirildi", func(t *testing.T) {
		fr := newFakeResolver()
		fr.txt["sel._domainkey.example.com"] = []string{edTXT(ed)}
		raw := signed(t, testMsg("alice@example.com", ""), "example.com", "sel", ed)
		raw = []byte(strings.Replace(string(raw), "Subject: test iletisi", "Subject: sahte konu", 1))
		got := newVerifier(fr).checkDKIM(context.Background(), boundedResolver{fr}, raw)
		if len(got) != 1 || got[0].Result != Fail || got[0].Reason != "signature did not verify" {
			t.Fatalf("sonuc: %+v", got)
		}
	})

	t.Run("fail_yanlis_anahtar", func(t *testing.T) {
		fr := newFakeResolver()
		fr.txt["sel._domainkey.example.com"] = []string{edTXT(newEdKey(t))} // baska anahtar
		raw := signed(t, testMsg("alice@example.com", ""), "example.com", "sel", ed)
		got := newVerifier(fr).checkDKIM(context.Background(), boundedResolver{fr}, raw)
		if len(got) != 1 || got[0].Result != Fail {
			t.Fatalf("sonuc: %+v", got)
		}
	})

	t.Run("none_imza_yok", func(t *testing.T) {
		fr := newFakeResolver()
		got := newVerifier(fr).checkDKIM(context.Background(), boundedResolver{fr}, testMsg("alice@example.com", ""))
		if len(got) != 1 || got[0].Result != None {
			t.Fatalf("sonuc: %+v", got)
		}
	})

	t.Run("permerror_anahtar_yok", func(t *testing.T) {
		fr := newFakeResolver() // anahtar kaydi yok -> NXDOMAIN
		raw := signed(t, testMsg("alice@example.com", ""), "example.com", "sel", ed)
		got := newVerifier(fr).checkDKIM(context.Background(), boundedResolver{fr}, raw)
		if len(got) != 1 || got[0].Result != PermError || got[0].Reason != "no usable key" {
			t.Fatalf("sonuc: %+v", got)
		}
	})

	t.Run("permerror_anahtar_iptal", func(t *testing.T) {
		fr := newFakeResolver()
		fr.txt["sel._domainkey.example.com"] = []string{"v=DKIM1; k=ed25519; p="}
		raw := signed(t, testMsg("alice@example.com", ""), "example.com", "sel", ed)
		got := newVerifier(fr).checkDKIM(context.Background(), boundedResolver{fr}, raw)
		if len(got) != 1 || got[0].Result != PermError {
			t.Fatalf("sonuc: %+v", got)
		}
	})

	t.Run("temperror_gecici_dns", func(t *testing.T) {
		fr := newFakeResolver()
		fr.errs["sel._domainkey.example.com"] = errTempDNS
		raw := signed(t, testMsg("alice@example.com", ""), "example.com", "sel", ed)
		got := newVerifier(fr).checkDKIM(context.Background(), boundedResolver{fr}, raw)
		if len(got) != 1 || got[0].Result != TempError {
			t.Fatalf("sonuc: %+v", got)
		}
	})

	t.Run("iki_imza_biri_gecerli", func(t *testing.T) {
		other := newEdKey(t)
		fr := newFakeResolver()
		fr.txt["sel._domainkey.example.com"] = []string{edTXT(ed)}
		// ikinci imzanin anahtari yok
		raw := signed(t, testMsg("alice@example.com", ""), "example.com", "sel", ed)
		raw = signed(t, raw, "list.example.net", "l1", other)
		got := newVerifier(fr).checkDKIM(context.Background(), boundedResolver{fr}, raw)
		if len(got) != 2 {
			t.Fatalf("2 sonuc bekleniyordu: %+v", got)
		}
		r := &Result{DKIM: got}
		if !r.AnyDKIMPass() {
			t.Fatalf("en az bir gecerli imza bekleniyordu: %+v", got)
		}
		var pass, perm int
		for _, g := range got {
			switch g.Result {
			case Pass:
				pass++
			case PermError:
				perm++
			}
		}
		if pass != 1 || perm != 1 {
			t.Fatalf("pass=%d permerror=%d: %+v", pass, perm, got)
		}
	})

	t.Run("cok_imza_sinirlanir", func(t *testing.T) {
		fr := newFakeResolver()
		raw := testMsg("alice@example.com", "")
		for _, sel := range []string{"s1", "s2", "s3", "s4"} {
			key := newEdKey(t)
			fr.txt[sel+"._domainkey.example.com"] = []string{edTXT(key)}
			raw = signed(t, raw, "example.com", sel, key)
		}
		v := newVerifier(fr)
		v.MaxDKIM = 2
		got := v.checkDKIM(context.Background(), boundedResolver{fr}, raw)
		if len(got) != 2 {
			t.Fatalf("en cok 2 sonuc bekleniyordu, %d geldi: %+v", len(got), got)
		}
	})

	t.Run("permerror_baslik_bozuk", func(t *testing.T) {
		fr := newFakeResolver()
		got := newVerifier(fr).checkDKIM(context.Background(), boundedResolver{fr}, []byte("From: a@example.com\r\nSubject: bos satir yok"))
		if len(got) != 1 || got[0].Result != PermError {
			t.Fatalf("sonuc: %+v", got)
		}
	})
}

// scenario, Verify uctan uca testleri icin ortak girdi uretir.
type scenario struct {
	fr       *fakeResolver
	msg      []byte
	ip       net.IP
	helo     string
	mailFrom string
	rand     int
}

func newScenario() *scenario {
	return &scenario{
		fr:       newFakeResolver(),
		msg:      testMsg("Alice <alice@example.com>", ""),
		ip:       testIP,
		helo:     "mx.sender.example",
		mailFrom: "bounce@example.com",
		rand:     99,
	}
}

func (s *scenario) run() *Result {
	v := newVerifier(s.fr)
	v.Rand = func() int { return s.rand }
	return v.Verify(context.Background(), Input{
		ClientIP: s.ip, HELO: s.helo, MailFrom: s.mailFrom, Message: s.msg,
	})
}

func TestVerifyDMARCDecisions(t *testing.T) {
	ed := newEdKey(t)

	cases := []struct {
		name       string
		setup      func(s *scenario)
		spf        string
		dmarc      string
		action     Action
		disp       string
		noRecord   bool
		spfAligned bool
		dkimAlign  bool
	}{
		{
			name: "spf_hizali_pass_p_reject",
			setup: func(s *scenario) {
				s.fr.txt["example.com"] = []string{"v=spf1 ip4:203.0.113.7 -all"}
				s.fr.txt["_dmarc.example.com"] = []string{"v=DMARC1; p=reject"}
			},
			spf: Pass, dmarc: Pass, action: ActionInbox, spfAligned: true,
		},
		{
			name: "dkim_hizali_pass_spf_fail_p_reject",
			setup: func(s *scenario) {
				s.fr.txt["example.com"] = []string{"v=spf1 -all"}
				s.fr.txt["_dmarc.example.com"] = []string{"v=DMARC1; p=reject"}
				s.fr.txt["sel._domainkey.example.com"] = []string{edTXT(ed)}
				s.msg = signed(t, s.msg, "example.com", "sel", ed)
			},
			spf: Fail, dmarc: Pass, action: ActionInbox, dkimAlign: true,
		},
		{
			name: "reject_spf_fail_dkim_yok_p_reject",
			setup: func(s *scenario) {
				s.fr.txt["example.com"] = []string{"v=spf1 ip4:198.51.100.1 -all"}
				s.fr.txt["_dmarc.example.com"] = []string{"v=DMARC1; p=reject; rua=mailto:r@example.com"}
			},
			spf: Fail, dmarc: Fail, action: ActionReject, disp: "reject",
		},
		{
			name: "reject_spf_softfail_p_reject",
			setup: func(s *scenario) {
				s.fr.txt["example.com"] = []string{"v=spf1 ~all"}
				s.fr.txt["_dmarc.example.com"] = []string{"v=DMARC1; p=reject"}
			},
			spf: SoftFail, dmarc: Fail, action: ActionReject, disp: "reject",
		},
		{
			name: "reject_dkim_bozuk_spf_fail",
			setup: func(s *scenario) {
				s.fr.txt["example.com"] = []string{"v=spf1 -all"}
				s.fr.txt["_dmarc.example.com"] = []string{"v=DMARC1; p=reject"}
				s.fr.txt["sel._domainkey.example.com"] = []string{edTXT(ed)}
				raw := signed(t, s.msg, "example.com", "sel", ed)
				s.msg = []byte(strings.Replace(string(raw), "govde satiri", "baska satir", 1))
			},
			spf: Fail, dmarc: Fail, action: ActionReject, disp: "reject",
		},
		{
			name: "quarantine_junk",
			setup: func(s *scenario) {
				s.fr.txt["example.com"] = []string{"v=spf1 -all"}
				s.fr.txt["_dmarc.example.com"] = []string{"v=DMARC1; p=quarantine"}
			},
			spf: Fail, dmarc: Fail, action: ActionJunk, disp: "quarantine",
		},
		{
			name: "p_none_gelen_kutusu",
			setup: func(s *scenario) {
				s.fr.txt["example.com"] = []string{"v=spf1 -all"}
				s.fr.txt["_dmarc.example.com"] = []string{"v=DMARC1; p=none"}
			},
			spf: Fail, dmarc: Fail, action: ActionInbox, disp: "none",
		},
		{
			name: "gevsek_hizalama_alt_alan_adi_mailfrom",
			setup: func(s *scenario) {
				s.mailFrom = "b@bounces.example.com"
				s.fr.txt["bounces.example.com"] = []string{"v=spf1 ip4:203.0.113.7 -all"}
				s.fr.txt["_dmarc.example.com"] = []string{"v=DMARC1; p=reject"}
			},
			spf: Pass, dmarc: Pass, action: ActionInbox, spfAligned: true,
		},
		{
			name: "siki_hizalama_spf_alt_alan_adi_reject",
			setup: func(s *scenario) {
				s.mailFrom = "b@bounces.example.com"
				s.fr.txt["bounces.example.com"] = []string{"v=spf1 ip4:203.0.113.7 -all"}
				s.fr.txt["_dmarc.example.com"] = []string{"v=DMARC1; p=reject; aspf=s"}
			},
			spf: Pass, dmarc: Fail, action: ActionReject, disp: "reject",
		},
		{
			name: "siki_hizalama_dkim_alt_alan_adi_reject",
			setup: func(s *scenario) {
				s.fr.txt["example.com"] = []string{"v=spf1 -all"}
				s.fr.txt["_dmarc.example.com"] = []string{"v=DMARC1; p=reject; adkim=s"}
				s.fr.txt["sel._domainkey.mail.example.com"] = []string{edTXT(ed)}
				s.msg = signed(t, s.msg, "mail.example.com", "sel", ed)
			},
			spf: Fail, dmarc: Fail, action: ActionReject, disp: "reject",
		},
		{
			name: "ucuncu_taraf_dkim_hizali_degil_reject",
			setup: func(s *scenario) {
				s.fr.txt["example.com"] = []string{"v=spf1 -all"}
				s.fr.txt["_dmarc.example.com"] = []string{"v=DMARC1; p=reject"}
				s.fr.txt["sel._domainkey.esp.example.net"] = []string{edTXT(ed)}
				s.msg = signed(t, s.msg, "esp.example.net", "sel", ed)
			},
			spf: Fail, dmarc: Fail, action: ActionReject, disp: "reject",
		},
		{
			name: "organizasyon_alan_adi_kaydi_alt_alan_adi_icin_sp",
			setup: func(s *scenario) {
				s.msg = testMsg("alice@news.example.com", "")
				s.mailFrom = "b@news.example.com"
				s.fr.txt["news.example.com"] = []string{"v=spf1 -all"}
				s.fr.txt["_dmarc.example.com"] = []string{"v=DMARC1; p=reject; sp=quarantine"}
			},
			spf: Fail, dmarc: Fail, action: ActionJunk, disp: "quarantine",
		},
		{
			name: "organizasyon_alan_adi_kaydi_sp_yoksa_p",
			setup: func(s *scenario) {
				s.msg = testMsg("alice@news.example.com", "")
				s.mailFrom = "b@news.example.com"
				s.fr.txt["news.example.com"] = []string{"v=spf1 -all"}
				s.fr.txt["_dmarc.example.com"] = []string{"v=DMARC1; p=reject"}
			},
			spf: Fail, dmarc: Fail, action: ActionReject, disp: "reject",
		},
		{
			name: "alt_alan_adinin_kendi_kaydi_onceliklidir",
			setup: func(s *scenario) {
				s.msg = testMsg("alice@news.example.com", "")
				s.mailFrom = "b@news.example.com"
				s.fr.txt["news.example.com"] = []string{"v=spf1 -all"}
				s.fr.txt["_dmarc.news.example.com"] = []string{"v=DMARC1; p=none"}
				s.fr.txt["_dmarc.example.com"] = []string{"v=DMARC1; p=reject"}
			},
			spf: Fail, dmarc: Fail, action: ActionInbox, disp: "none",
		},
		{
			name: "pct_ornekleme_disi_bir_alt_politika",
			setup: func(s *scenario) {
				s.fr.txt["example.com"] = []string{"v=spf1 -all"}
				s.fr.txt["_dmarc.example.com"] = []string{"v=DMARC1; p=reject; pct=10"}
				s.rand = 50 // 50 >= 10: ornekleme disi -> quarantine
			},
			spf: Fail, dmarc: Fail, action: ActionJunk, disp: "quarantine",
		},
		{
			name: "pct_ornekleme_icinde_tam_politika",
			setup: func(s *scenario) {
				s.fr.txt["example.com"] = []string{"v=spf1 -all"}
				s.fr.txt["_dmarc.example.com"] = []string{"v=DMARC1; p=reject; pct=10"}
				s.rand = 5 // 5 < 10: politika uygulanir
			},
			spf: Fail, dmarc: Fail, action: ActionReject, disp: "reject",
		},
		{
			name: "pct_quarantine_ornekleme_disi_none",
			setup: func(s *scenario) {
				s.fr.txt["example.com"] = []string{"v=spf1 -all"}
				s.fr.txt["_dmarc.example.com"] = []string{"v=DMARC1; p=quarantine; pct=0"}
				s.rand = 0
			},
			spf: Fail, dmarc: Fail, action: ActionInbox, disp: "none",
		},
		{
			name: "dmarc_yok_spf_fail_dkim_yok_junk",
			setup: func(s *scenario) {
				s.fr.txt["example.com"] = []string{"v=spf1 ip4:198.51.100.1 -all"}
			},
			spf: Fail, dmarc: None, action: ActionJunk, noRecord: true,
		},
		{
			name: "dmarc_yok_spf_fail_ama_gecerli_dkim_var_inbox",
			setup: func(s *scenario) {
				s.fr.txt["example.com"] = []string{"v=spf1 -all"}
				s.fr.txt["sel._domainkey.esp.example.net"] = []string{edTXT(ed)}
				s.msg = signed(t, s.msg, "esp.example.net", "sel", ed)
			},
			spf: Fail, dmarc: None, action: ActionInbox, noRecord: true,
		},
		{
			name: "dmarc_yok_spf_softfail_inbox",
			setup: func(s *scenario) {
				s.fr.txt["example.com"] = []string{"v=spf1 ~all"}
			},
			spf: SoftFail, dmarc: None, action: ActionInbox, noRecord: true,
		},
		{
			name:  "dmarc_yok_spf_yok_inbox",
			setup: func(s *scenario) {},
			spf:   None, dmarc: None, action: ActionInbox, noRecord: true,
		},
		{
			name: "dmarc_dns_gecici_hata_inbox",
			setup: func(s *scenario) {
				s.fr.txt["example.com"] = []string{"v=spf1 -all"}
				s.fr.errs["_dmarc.example.com"] = errTempDNS
			},
			spf: Fail, dmarc: TempError, action: ActionInbox,
		},
		{
			name: "dmarc_org_alan_adi_gecici_hata_inbox",
			setup: func(s *scenario) {
				s.msg = testMsg("alice@news.example.com", "")
				s.mailFrom = "b@news.example.com"
				s.fr.txt["news.example.com"] = []string{"v=spf1 -all"}
				s.fr.errs["_dmarc.example.com"] = errTimeout
			},
			spf: Fail, dmarc: TempError, action: ActionInbox,
		},
		{
			name: "spf_gecici_hata_politika_reject_olsa_da_inbox",
			setup: func(s *scenario) {
				s.fr.errs["example.com"] = errTempDNS
				s.fr.txt["_dmarc.example.com"] = []string{"v=DMARC1; p=reject"}
			},
			spf: TempError, dmarc: TempError, action: ActionInbox,
		},
		{
			name: "dkim_anahtar_gecici_hata_politika_reject_olsa_da_inbox",
			setup: func(s *scenario) {
				s.fr.txt["example.com"] = []string{"v=spf1 -all"}
				s.fr.txt["_dmarc.example.com"] = []string{"v=DMARC1; p=reject"}
				s.fr.errs["sel._domainkey.example.com"] = errTempDNS
				s.msg = signed(t, s.msg, "example.com", "sel", ed)
			},
			spf: Fail, dmarc: TempError, action: ActionInbox,
		},
		{
			name: "spf_gecici_hata_ama_dkim_hizali_pass",
			setup: func(s *scenario) {
				s.fr.errs["example.com"] = errTempDNS
				s.fr.txt["_dmarc.example.com"] = []string{"v=DMARC1; p=reject"}
				s.fr.txt["sel._domainkey.example.com"] = []string{edTXT(ed)}
				s.msg = signed(t, s.msg, "example.com", "sel", ed)
			},
			spf: TempError, dmarc: Pass, action: ActionInbox, dkimAlign: true,
		},
		{
			name: "birden_cok_from_basligi_permerror_inbox",
			setup: func(s *scenario) {
				s.msg = testMsg("alice@example.com", "From: mallory@evil.example\r\n")
				s.fr.txt["example.com"] = []string{"v=spf1 -all"}
				s.fr.txt["_dmarc.example.com"] = []string{"v=DMARC1; p=reject"}
			},
			spf: Fail, dmarc: PermError, action: ActionInbox,
		},
		{
			name: "from_yok_permerror_inbox",
			setup: func(s *scenario) {
				s.msg = []byte("To: acme@mail.zorven.app\r\nSubject: x\r\n\r\ngovde\r\n")
				s.fr.txt["example.com"] = []string{"v=spf1 -all"}
			},
			spf: Fail, dmarc: PermError, action: ActionInbox,
		},
		{
			name: "farkli_alan_adli_cok_adresli_from_permerror",
			setup: func(s *scenario) {
				s.msg = testMsg("alice@example.com, bob@example.org", "")
				s.fr.txt["example.com"] = []string{"v=spf1 -all"}
				s.fr.txt["_dmarc.example.com"] = []string{"v=DMARC1; p=reject"}
			},
			spf: Fail, dmarc: PermError, action: ActionInbox,
		},
		{
			name: "birden_cok_dmarc_kaydi_permerror_inbox",
			setup: func(s *scenario) {
				s.fr.txt["example.com"] = []string{"v=spf1 -all"}
				s.fr.txt["_dmarc.example.com"] = []string{"v=DMARC1; p=reject", "v=DMARC1; p=none"}
			},
			spf: Fail, dmarc: PermError, action: ActionInbox,
		},
		{
			name: "gecersiz_dmarc_kaydi_permerror_inbox",
			setup: func(s *scenario) {
				s.fr.txt["example.com"] = []string{"v=spf1 -all"}
				s.fr.txt["_dmarc.example.com"] = []string{"v=DMARC1; p=bogus"}
			},
			spf: Fail, dmarc: PermError, action: ActionInbox,
		},
		{
			name: "buyuk_harfli_dmarc_kaydi_yine_uygulanir",
			setup: func(s *scenario) {
				s.fr.txt["example.com"] = []string{"v=spf1 -all"}
				s.fr.txt["_dmarc.example.com"] = []string{"v=DMARC1; P=REJECT; ADKIM=S; Aspf=R; PCT=100"}
			},
			spf: Fail, dmarc: Fail, action: ActionReject, disp: "reject",
		},
		{
			name: "dmarc_olmayan_txt_kayitlari_yok_sayilir",
			setup: func(s *scenario) {
				s.fr.txt["example.com"] = []string{"v=spf1 -all"}
				s.fr.txt["_dmarc.example.com"] = []string{"google-site-verification=xyz"}
			},
			spf: Fail, dmarc: None, action: ActionJunk, noRecord: true,
		},
		{
			name: "genel_olmayan_istemci_spf_atlanir_reject_edilmez",
			setup: func(s *scenario) {
				s.ip = net.ParseIP("172.17.0.1") // Docker bridge
				s.fr.txt["example.com"] = []string{"v=spf1 -all"}
				s.fr.txt["_dmarc.example.com"] = []string{"v=DMARC1; p=reject"}
			},
			spf: None, dmarc: None, action: ActionInbox,
		},
		{
			name: "genel_olmayan_istemci_hizali_dkim_yine_pass",
			setup: func(s *scenario) {
				s.ip = net.ParseIP("127.0.0.1")
				s.fr.txt["_dmarc.example.com"] = []string{"v=DMARC1; p=reject"}
				s.fr.txt["sel._domainkey.example.com"] = []string{edTXT(ed)}
				s.msg = signed(t, s.msg, "example.com", "sel", ed)
			},
			spf: None, dmarc: Pass, action: ActionInbox, dkimAlign: true,
		},
		{
			name: "bos_gonderici_helo_hizali_pass",
			setup: func(s *scenario) {
				s.mailFrom = ""
				s.helo = "example.com"
				s.fr.txt["example.com"] = []string{"v=spf1 ip4:203.0.113.7 -all"}
				s.fr.txt["_dmarc.example.com"] = []string{"v=DMARC1; p=reject"}
			},
			spf: Pass, dmarc: Pass, action: ActionInbox, spfAligned: true,
		},
		{
			name: "bos_gonderici_helo_hizali_degil_reject",
			setup: func(s *scenario) {
				s.mailFrom = ""
				s.helo = "mx.sender.example"
				s.fr.txt["mx.sender.example"] = []string{"v=spf1 ip4:203.0.113.7 -all"}
				s.fr.txt["_dmarc.example.com"] = []string{"v=DMARC1; p=reject"}
			},
			spf: Pass, dmarc: Fail, action: ActionReject, disp: "reject",
		},
		{
			name: "from_alan_adi_buyuk_harf",
			setup: func(s *scenario) {
				s.msg = testMsg("Alice <alice@EXAMPLE.com>", "")
				s.fr.txt["example.com"] = []string{"v=spf1 ip4:203.0.113.7 -all"}
				s.fr.txt["_dmarc.example.com"] = []string{"v=DMARC1; p=reject"}
			},
			spf: Pass, dmarc: Pass, action: ActionInbox, spfAligned: true,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s := newScenario()
			tc.setup(s)
			r := s.run()
			if r.SPF.Result != tc.spf {
				t.Errorf("SPF = %q, beklenen %q (%+v)", r.SPF.Result, tc.spf, r.SPF)
			}
			if r.DMARC.Result != tc.dmarc {
				t.Errorf("DMARC = %q, beklenen %q (%+v)", r.DMARC.Result, tc.dmarc, r.DMARC)
			}
			if r.Action != tc.action {
				t.Errorf("karar = %v (%s), beklenen %v", r.Action, r.Why, tc.action)
			}
			if tc.disp != "" && r.DMARC.Disposition != tc.disp {
				t.Errorf("disposition = %q, beklenen %q", r.DMARC.Disposition, tc.disp)
			}
			if r.DMARC.NoRecord != tc.noRecord {
				t.Errorf("NoRecord = %v, beklenen %v", r.DMARC.NoRecord, tc.noRecord)
			}
			if r.DMARC.SPFAligned != tc.spfAligned || r.DMARC.DKIMAligned != tc.dkimAlign {
				t.Errorf("hizalama spf=%v dkim=%v, beklenen spf=%v dkim=%v",
					r.DMARC.SPFAligned, r.DMARC.DKIMAligned, tc.spfAligned, tc.dkimAlign)
			}
			if t.Failed() {
				t.Logf("baslik:\n%s", r.HeaderField("mail.zorven.app"))
			}
		})
	}
}

// Toplam DNS suresi dolunca (tum sorgular takilir) Verify zamaninda doner, tum
// sonuclar temperror olur ve ileti normal teslim edilir.
func TestVerifyDNSTimeout(t *testing.T) {
	s := newScenario()
	s.fr.hangAll = true
	s.fr.txt["_dmarc.example.com"] = []string{"v=DMARC1; p=reject"}
	ed := newEdKey(t)
	s.msg = signed(t, s.msg, "example.com", "sel", ed)

	v := newVerifier(s.fr)
	v.Timeout = 200 * time.Millisecond
	start := time.Now()
	r := v.Verify(context.Background(), Input{ClientIP: testIP, HELO: s.helo, MailFrom: s.mailFrom, Message: s.msg})
	if el := time.Since(start); el > 3*time.Second {
		t.Fatalf("Verify %v surdu; zaman siniri isletilmedi", el)
	}
	if r.SPF.Result != TempError {
		t.Errorf("SPF = %q, temperror bekleniyordu", r.SPF.Result)
	}
	if len(r.DKIM) != 1 || r.DKIM[0].Result != TempError {
		t.Errorf("DKIM = %+v, temperror bekleniyordu", r.DKIM)
	}
	if r.DMARC.Result != TempError {
		t.Errorf("DMARC = %q, temperror bekleniyordu", r.DMARC.Result)
	}
	if r.Action != ActionInbox {
		t.Errorf("karar = %v, normal teslim bekleniyordu", r.Action)
	}
}

// Cagiran baglam iptal edilmisse de sonuc ret/Junk degil temperror/Inbox olur.
func TestVerifyCancelledContext(t *testing.T) {
	s := newScenario()
	s.fr.txt["example.com"] = []string{"v=spf1 -all"}
	s.fr.txt["_dmarc.example.com"] = []string{"v=DMARC1; p=reject"}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	v := newVerifier(s.fr)
	r := v.Verify(ctx, Input{ClientIP: testIP, HELO: s.helo, MailFrom: s.mailFrom, Message: s.msg})
	if r.Action != ActionInbox {
		t.Fatalf("iptal edilmis baglamda karar = %v (%s)", r.Action, r.Why)
	}
}

// Sifir deger Verifier'in varsayilanlari makul olmali (agsiz kontrol).
func TestVerifierDefaults(t *testing.T) {
	var v Verifier
	if v.timeout() != DefaultTimeout || v.maxDKIM() != DefaultMaxDKIM {
		t.Fatalf("varsayilanlar: %v %v", v.timeout(), v.maxDKIM())
	}
	if n := v.rand(); n < 0 || n >= 100 {
		t.Fatalf("rand araligi: %d", n)
	}
	if v.resolver() == nil {
		t.Fatal("cozumleyici nil")
	}
}

func TestIsPublicIP(t *testing.T) {
	cases := map[string]bool{
		"203.0.113.7":     true,
		"198.51.100.1":    true,
		"8.8.8.8":         true,
		"2001:db8::1":     true,
		"2606:4700::1":    true,
		"127.0.0.1":       false,
		"::1":             false,
		"10.0.0.5":        false,
		"172.17.0.1":      false,
		"192.168.1.10":    false,
		"169.254.1.1":     false,
		"fe80::1":         false,
		"fd00::1":         false,
		"100.64.0.5":      false,
		"0.0.0.0":         false,
		"224.0.0.1":       false,
		"::ffff:10.0.0.1": false,
	}
	for s, want := range cases {
		if got := isPublicIP(net.ParseIP(s)); got != want {
			t.Errorf("isPublicIP(%s) = %v, beklenen %v", s, got, want)
		}
	}
	if isPublicIP(nil) {
		t.Error("nil adres genel sayildi")
	}
}

func TestBoundedResolverNormalizesErrors(t *testing.T) {
	ctx := context.Background()

	// Sarilmis DNSError acilir (SPF kutuphanesi tip iddiasi kullanir).
	fr := newFakeResolver()
	fr.errs["a.example"] = &wrapErr{inner: nxdomain("a.example")}
	_, err := boundedResolver{fr}.LookupTXT(ctx, "a.example")
	if de, ok := err.(*net.DNSError); !ok || !de.IsNotFound {
		t.Fatalf("sarilmis NXDOMAIN acilmadi: %#v", err)
	}

	// DNS olmayan hata gecici sayilir.
	fr.errs["b.example"] = context.Canceled
	_, err = boundedResolver{fr}.LookupTXT(ctx, "b.example")
	if de, ok := err.(*net.DNSError); !ok || !de.Temporary() {
		t.Fatalf("DNS olmayan hata gecici olmadi: %#v", err)
	}

	// Baglam bitmisse NXDOMAIN bile olsa gecici (zaman asimi) doner.
	done, cancel := context.WithCancel(ctx)
	cancel()
	_, err = boundedResolver{fr}.LookupTXT(done, "yok.example")
	if de, ok := err.(*net.DNSError); !ok || !de.IsTimeout || !de.Temporary() || de.IsNotFound {
		t.Fatalf("iptal edilmis baglam gecici hataya donmedi: %#v", err)
	}

	// Basarili sorgu aynen gecer.
	fr.txt["ok.example"] = []string{"x"}
	if got, err := (boundedResolver{fr}).LookupTXT(ctx, "ok.example"); err != nil || len(got) != 1 {
		t.Fatalf("basarili sorgu: %v %v", got, err)
	}
}

type wrapErr struct{ inner error }

func (w *wrapErr) Error() string { return "sarilmis: " + w.inner.Error() }
func (w *wrapErr) Unwrap() error { return w.inner }
