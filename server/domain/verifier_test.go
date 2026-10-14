package domain

import (
	"context"
	"errors"
	"net"
	"strings"
	"testing"
)

type mockDNSVerifier struct {
	cnames map[string]string
	txts   map[string][]string
}

func (m *mockDNSVerifier) LookupCNAME(ctx context.Context, host string) (string, error) {
	host = strings.ToLower(strings.TrimSuffix(host, "."))
	if target, ok := m.cnames[host]; ok {
		return target, nil
	}
	return "", errors.New("cname not found")
}

func (m *mockDNSVerifier) LookupTXT(ctx context.Context, name string) ([]string, error) {
	name = strings.ToLower(strings.TrimSuffix(name, "."))
	if list, ok := m.txts[name]; ok {
		return list, nil
	}
	return nil, errors.New("txt not found")
}

// F-36: IP literal, localhost ve ic ag son ekleri reddedilir; .test/.example
// (e2e) ve sondaki nokta kabul edilir.
func TestValidateDomain_ReservedAndIP(t *testing.T) {
	bad := []string{"1.2.3.4", "127.0.0.1", "::1", "[::1]", "localhost", "localhost.", "app.localhost", "printer.local", "db.internal", "a.b.internal."}
	for _, d := range bad {
		if err := ValidateDomain(d, "rpshell.app"); err == nil {
			t.Errorf("ValidateDomain(%q) reddedilmeliydi", d)
		}
	}
	good := []string{"a.example.test", "foo.example.com", "api.example.com.", "API.Example.COM"}
	for _, d := range good {
		if err := ValidateDomain(d, "rpshell.app"); err != nil {
			t.Errorf("ValidateDomain(%q) kabul edilmeliydi: %v", d, err)
		}
	}
}

func TestValidateDomain(t *testing.T) {
	platform := "rpshell.app"

	tests := []struct {
		domain  string
		wantErr bool
	}{
		{"api.example.com", false},
		{"sub.domain.co.uk", false},
		{"my-site.org", false},
		{"app.rpshell.app", true},          // platform subdomain
		{"rpshell.app", true},              // platform domain kendisi
		{"test--tenant.rpshell.app", true}, // platform scoped subdomain
		{"localhost", true},                // nokta yok
		{"-invalid.com", true},             // tire ile basliyor
		{"invalid-.com", true},             // tire ile bitiyor
		{"in valid.com", true},             // bosluk var
		{"", true},                         // bos
	}

	for _, tt := range tests {
		err := ValidateDomain(tt.domain, platform)
		if (err != nil) != tt.wantErr {
			t.Errorf("ValidateDomain(%q) err = %v, wantErr = %v", tt.domain, err, tt.wantErr)
		}
	}
}

func TestVerifyCustomDomain_CNAME(t *testing.T) {
	ctx := context.Background()
	platform := "rpshell.app"

	mock := &mockDNSVerifier{
		cnames: map[string]string{
			"api.example.com": "cname.rpshell.app.",
			"web.example.com": "other.domain.com.",
		},
		txts: map[string][]string{},
	}

	// 1. Dogru CNAME -> basarili
	err := VerifyCustomDomain(ctx, "api.example.com", "rpsh-verify-123", platform, mock)
	if err != nil {
		t.Fatalf("beklenmeyen hata: %v", err)
	}

	// 2. Yanlis CNAME -> hata
	err = VerifyCustomDomain(ctx, "web.example.com", "rpsh-verify-123", platform, mock)
	if !errors.Is(err, ErrVerificationFailed) {
		t.Fatalf("beklenen ErrVerificationFailed, gelen: %v", err)
	}
}

func TestVerifyCustomDomain_TXT(t *testing.T) {
	ctx := context.Background()
	platform := "rpshell.app"

	mock := &mockDNSVerifier{
		cnames: map[string]string{},
		txts: map[string][]string{
			"_rpsh-challenge.secure.example.com": {"rpsh-verify-token-xyz"},
		},
	}

	// 1. Dogru TXT -> basarili
	err := VerifyCustomDomain(ctx, "secure.example.com", "rpsh-verify-token-xyz", platform, mock)
	if err != nil {
		t.Fatalf("beklenmeyen hata: %v", err)
	}

	// 2. Yanlis token -> hata
	err = VerifyCustomDomain(ctx, "secure.example.com", "wrong-token", platform, mock)
	if !errors.Is(err, ErrVerificationFailed) {
		t.Fatalf("beklenen ErrVerificationFailed, gelen: %v", err)
	}

	// 3. Olmayan TXT -> hata
	err = VerifyCustomDomain(ctx, "other.example.com", "rpsh-verify-token-xyz", platform, mock)
	if !errors.Is(err, ErrVerificationFailed) {
		t.Fatalf("beklenen ErrVerificationFailed, gelen: %v", err)
	}
}

func TestGetInstructions(t *testing.T) {
	inst := GetInstructions("api.musteri.com", "zrv-verify-abc", "zorven.app")
	if inst.CNAMETarget != "cname.zorven.app" {
		t.Errorf("CNAMETarget hatali: %s", inst.CNAMETarget)
	}
	if inst.TXTRecord != "_zorven-challenge.api.musteri.com" {
		t.Errorf("TXTRecord hatali: %s", inst.TXTRecord)
	}
	if inst.TXTValue != "zrv-verify-abc" {
		t.Errorf("TXTValue hatali: %s", inst.TXTValue)
	}

	// Bos platform domaininde varsayilan zorven.app olmali
	instDef := GetInstructions("api.musteri.com", "zrv-verify-abc", "")
	if instDef.CNAMETarget != "cname.zorven.app" {
		t.Errorf("varsayilan CNAMETarget cname.zorven.app olmali, got: %s", instDef.CNAMETarget)
	}
}

func TestVerifyCustomDomain_CNAMEExactOnly(t *testing.T) {
	ctx := context.Background()
	platform := "rpshell.app"
	mock := &mockDNSVerifier{
		cnames: map[string]string{
			"ok.example.com":    "cname.rpshell.app.",
			"upper.example.com": "CNAME.RPShell.app",
			"other.example.com": "victim-tunnel.rpshell.app.",
			"apex.example.com":  "rpshell.app.",
			"deep.example.com":  "x.cname.rpshell.app.",
		},
		txts: map[string][]string{},
	}
	for _, h := range []string{"ok.example.com", "upper.example.com"} {
		if err := VerifyCustomDomain(ctx, h, "tok", platform, mock); err != nil {
			t.Errorf("%s kabul edilmeli: %v", h, err)
		}
	}
	for _, h := range []string{"other.example.com", "apex.example.com", "deep.example.com"} {
		if err := VerifyCustomDomain(ctx, h, "tok", platform, mock); !errors.Is(err, ErrVerificationFailed) {
			t.Errorf("%s reddedilmeli, got %v", h, err)
		}
	}
}

func TestIsApex(t *testing.T) {
	for fqdn, want := range map[string]bool{
		"example.org":       true,
		"EXAMPLE.org.":      true,
		"app.example.org":   false,
		"ornek.com.tr":      true, // com.tr kamu soneki
		"api.ornek.com.tr":  false,
		"example.net":       true,
		"a.b.example.co.uk": false,
		"example.co.uk":     true,
	} {
		if got := IsApex(fqdn); got != want {
			t.Errorf("IsApex(%q) = %v, beklenen %v", fqdn, got, want)
		}
	}
	if !GetInstructions("example.org", "tok", "zorven.app").Apex || GetInstructions("app.example.org", "tok", "zorven.app").Apex {
		t.Fatal("GetInstructions Apex alanini dogru doldurmali")
	}
}

type fakeIPResolver map[string][]net.IPAddr

func (f fakeIPResolver) LookupIPAddr(_ context.Context, h string) ([]net.IPAddr, error) {
	return f[h], nil
}

func TestPlatformIPv4s(t *testing.T) {
	r := fakeIPResolver{"cname.zorven.app": {{IP: net.ParseIP("203.0.113.7")}, {IP: net.ParseIP("2001:db8::1")}}}
	got := PlatformIPv4s(context.Background(), r, "zorven.app")
	if len(got) != 1 || got[0] != "203.0.113.7" {
		t.Fatalf("yalniz IPv4 donmeli: %v", got)
	}
}

func TestRecordLabel(t *testing.T) {
	for fqdn, want := range map[string]string{
		"example.org": "@", "app.example.org": "app", "a.b.example.org": "a.b",
		"api.ornek.com.tr": "api", "ornek.com.tr": "@",
	} {
		if got := RecordLabel(fqdn); got != want {
			t.Errorf("RecordLabel(%q) = %q, beklenen %q", fqdn, got, want)
		}
	}
}
