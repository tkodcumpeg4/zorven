package mail

import (
	"context"
	"encoding/xml"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"
	"time"
)

func testClientConfig() ClientConfig {
	return ClientConfig{MailDomain: "mail.zorven.app", PlatformDomain: "zorven.app", Host: "mail.zorven.app",
		IMAPPort: 993, SubmissionPort: 587, SubmissionsTLS: 465, DisplayName: "Zorven Mail"}
}

func TestAutoconfigXML(t *testing.T) {
	h := NewAutoConfigHandler(testClientConfig())
	for _, tc := range []struct{ host, path string }{
		{"autoconfig.mail.zorven.app", "/mail/config-v1.1.xml"},
		{"autoconfig.zorven.app", "/mail/config-v1.1.xml"},
		{"zorven.app", "/.well-known/autoconfig/mail/config-v1.1.xml"},
		{"mail.zorven.app", "/.well-known/autoconfig/mail/config-v1.1.xml"},
	} {
		r := httptest.NewRequest(http.MethodGet, "https://"+tc.host+tc.path+"?emailaddress=info@zorven.app", nil)
		if !h.Match(r) {
			t.Fatalf("%s%s eslesmedi", tc.host, tc.path)
		}
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != 200 || !strings.Contains(w.Header().Get("Content-Type"), "xml") {
			t.Fatalf("%s: %d %s", tc.host, w.Code, w.Header().Get("Content-Type"))
		}
		var cfg struct {
			XMLName  xml.Name `xml:"clientConfig"`
			Provider struct {
				Domains []string `xml:"domain"`
				In      struct {
					Type string `xml:"type,attr"`
					Host string `xml:"hostname"`
					Port int    `xml:"port"`
					Sock string `xml:"socketType"`
					User string `xml:"username"`
				} `xml:"incomingServer"`
				Out []struct {
					Host string `xml:"hostname"`
					Port int    `xml:"port"`
					Sock string `xml:"socketType"`
				} `xml:"outgoingServer"`
			} `xml:"emailProvider"`
		}
		if err := xml.Unmarshal(w.Body.Bytes(), &cfg); err != nil {
			t.Fatalf("gecersiz XML: %v\n%s", err, w.Body.String())
		}
		p := cfg.Provider
		if p.In.Type != "imap" || p.In.Host != "mail.zorven.app" || p.In.Port != 993 || p.In.Sock != "SSL" || p.In.User != "%EMAILADDRESS%" {
			t.Fatalf("incoming: %+v", p.In)
		}
		if len(p.Out) != 2 || p.Out[0].Port != 587 || p.Out[0].Sock != "STARTTLS" || p.Out[1].Port != 465 || p.Out[1].Sock != "SSL" {
			t.Fatalf("outgoing: %+v", p.Out)
		}
		if len(p.Domains) != 2 || p.Domains[0] != "mail.zorven.app" || p.Domains[1] != "zorven.app" {
			t.Fatalf("domains: %v", p.Domains)
		}
	}
	// Yabanci host veya yol eslesmez (tunel trafigi etkilenmez).
	for _, u := range []string{
		"https://tunnel.example.com/mail/config-v1.1.xml",
		"https://zorven.app/mail/other.xml",
	} {
		if h.Match(httptest.NewRequest(http.MethodGet, u, nil)) {
			t.Fatalf("%s eslesmemeli", u)
		}
	}
	if h.Match(httptest.NewRequest(http.MethodPost, "https://zorven.app/mail/config-v1.1.xml", nil)) {
		t.Fatal("POST autoconfig'e eslesmemeli")
	}
}

func TestAutodiscoverXML(t *testing.T) {
	h := NewAutoConfigHandler(testClientConfig())
	body := `<?xml version="1.0" encoding="utf-8"?><Autodiscover xmlns="http://schemas.microsoft.com/exchange/autodiscover/outlook/requestschema/2006"><Request><EMailAddress>Acme@Mail.Zorven.app</EMailAddress><AcceptableResponseSchema>http://schemas.microsoft.com/exchange/autodiscover/outlook/responseschema/2006a</AcceptableResponseSchema></Request></Autodiscover>`
	for _, path := range []string{"/autodiscover/autodiscover.xml", "/Autodiscover/Autodiscover.xml"} {
		r := httptest.NewRequest(http.MethodPost, "https://autodiscover.mail.zorven.app"+path, strings.NewReader(body))
		if !h.Match(r) {
			t.Fatalf("%s eslesmedi", path)
		}
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		out := w.Body.String()
		for _, want := range []string{
			"<Type>IMAP</Type>", "<Server>mail.zorven.app</Server>", "<Port>993</Port>",
			"<LoginName>acme@mail.zorven.app</LoginName>", "<Type>SMTP</Type>", "<Port>587</Port>", "<Encryption>TLS</Encryption>",
		} {
			if !strings.Contains(out, want) {
				t.Fatalf("%q yok:\n%s", want, out)
			}
		}
		if err := xml.Unmarshal(w.Body.Bytes(), new(struct{ XMLName xml.Name })); err != nil {
			t.Fatalf("gecersiz XML: %v", err)
		}
	}
	// GET ?emailaddress=
	r := httptest.NewRequest(http.MethodGet, "https://autodiscover.zorven.app/autodiscover/autodiscover.xml?emailaddress=info@zorven.app", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if !strings.Contains(w.Body.String(), "<LoginName>info@zorven.app</LoginName>") {
		t.Fatalf("GET yaniti: %s", w.Body.String())
	}
	// Bilinmeyen alan adi hata yaniti.
	r = httptest.NewRequest(http.MethodGet, "https://autodiscover.zorven.app/autodiscover/autodiscover.xml?emailaddress=x@example.net", nil)
	w = httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if !strings.Contains(w.Body.String(), "<Error") || strings.Contains(w.Body.String(), "<Server>") {
		t.Fatalf("yabanci alan adi hata donmeli: %s", w.Body.String())
	}
}

func TestMobileConfig(t *testing.T) {
	out := string(testClientConfig().MobileConfig("acme@mail.zorven.app", "Acme"))
	for _, want := range []string{"com.apple.mail.managed", "<string>acme@mail.zorven.app</string>",
		"<key>IncomingMailServerPortNumber</key><integer>993</integer>",
		"<key>OutgoingMailServerPortNumber</key><integer>587</integer>", "mail.zorven.app"} {
		if !strings.Contains(out, want) {
			t.Fatalf("%q yok:\n%s", want, out)
		}
	}
	if strings.Contains(strings.ToLower(out), "password</key><string>") {
		t.Fatal("profil parola icermemeli")
	}
	if err := xml.Unmarshal([]byte(out), new(struct{ XMLName xml.Name })); err != nil {
		t.Fatalf("gecersiz plist: %v", err)
	}
}

func TestGenerateAppPasswordFormat(t *testing.T) {
	re := regexp.MustCompile(`^[a-z2-9]{4}(-[a-z2-9]{4}){3}$`)
	seen := map[string]bool{}
	for i := 0; i < 50; i++ {
		p := GenerateAppPassword()
		if !re.MatchString(p) {
			t.Fatalf("bicim: %q", p)
		}
		if seen[p] {
			t.Fatal("tekrar eden parola")
		}
		seen[p] = true
	}
	if NormalizeAppPassword("ABCD efgh-2345 6789") != "abcdefgh23456789" {
		t.Fatal("normalize")
	}
}

func TestAuthenticator_RateLimitAndLastUsed(t *testing.T) {
	st := newMemMailStore()
	st.addTenant(testTenant, "acme", "pro")
	pw := GenerateAppPassword()
	rec := st.addAppPassword(testTenant, testBox, pw)
	a := NewAuthenticator(st, "mail.zorven.app", "zorven.app", []string{"info"}, nil)
	now := time.Now()
	a.now = func() time.Time { return now }
	ctx := context.Background()

	for i := 0; i < failLimitPair; i++ {
		if _, err := a.Authenticate(ctx, "imap", testBox, "yanlis-"+string(rune('a'+i)), "9.9.9.9"); err != ErrAuthFailed {
			t.Fatalf("%d. deneme: %v", i, err)
		}
	}
	// Siniri asinca dogru parola bile reddedilir (ayni IP+kullanici).
	if _, err := a.Authenticate(ctx, "imap", testBox, pw, "9.9.9.9"); err != ErrAuthRateLimited {
		t.Fatalf("hiz siniri bekleniyordu: %v", err)
	}
	// Baska IP etkilenmez ve basarili giris son kullanimi isler.
	acct, err := a.Authenticate(ctx, "smtp", testBox, pw, "8.8.8.8")
	if err != nil || acct.Address != testBox || acct.TenantID != testTenant {
		t.Fatalf("baska IP: %v %+v", err, acct)
	}
	if got := st.passwordByID(rec.ID); got.LastUsedIP != "8.8.8.8" || got.LastUsedAt == nil {
		t.Fatalf("last_used: %+v", got)
	}
	// Pencere dolunca engel kalkar.
	now = now.Add(failWindow + time.Second)
	if _, err := a.Authenticate(ctx, "imap", testBox, pw, "9.9.9.9"); err != nil {
		t.Fatalf("pencere sonrasi: %v", err)
	}
}
