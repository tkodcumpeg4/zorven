package domain

import (
	"context"
	"errors"
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
