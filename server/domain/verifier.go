package domain

import (
	"context"
	"errors"
	"fmt"
	"net"
	"strings"
)

var (
	ErrInvalidDomain      = errors.New("gecersiz alan adi formati")
	ErrPlatformSubdomain  = errors.New("platform alan adinin sub-domainleri ozel alan adi olarak eklenemez")
	ErrVerificationFailed = errors.New("DNS dogrulamasi basarisiz: CNAME veya TXT kaydi eslesmedi")
)

// DNSVerifier, DNS sorgularini soyutlar (testlerde mock'lanabilmesi icin).
type DNSVerifier interface {
	LookupCNAME(ctx context.Context, host string) (string, error)
	LookupTXT(ctx context.Context, name string) ([]string, error)
}

// NetDNSVerifier, Go'nun varsayilan sistem DNS cozucusunu kullanir.
type NetDNSVerifier struct {
	Resolver *net.Resolver
}

func NewNetDNSVerifier() *NetDNSVerifier {
	return &NetDNSVerifier{Resolver: net.DefaultResolver}
}

func (v *NetDNSVerifier) LookupCNAME(ctx context.Context, host string) (string, error) {
	return v.Resolver.LookupCNAME(ctx, host)
}

func (v *NetDNSVerifier) LookupTXT(ctx context.Context, name string) ([]string, error) {
	return v.Resolver.LookupTXT(ctx, name)
}

// VerificationInstructions, kullaniciya alan adini nasil dogrulayacagini aciklar.
type VerificationInstructions struct {
	CNAMERecord string `json:"cname_record"` // or. "api.musteri.com"
	CNAMETarget string `json:"cname_target"` // or. "cname.rpshell.app"
	TXTRecord   string `json:"txt_record"`   // or. "_rpsh-challenge.api.musteri.com"
	TXTValue    string `json:"txt_value"`    // or. "rpsh-verify-123456"
}

// GetInstructions, verilen fqdn ve verifyToken icin DNS ayar talimatlarini uretir.
func GetInstructions(fqdn, verifyToken, platformDomain string) VerificationInstructions {
	cnameTarget := "cname." + platformDomain
	if platformDomain == "" {
		cnameTarget = "cname.zorven.app"
	}
	return VerificationInstructions{
		CNAMERecord: fqdn,
		CNAMETarget: cnameTarget,
		TXTRecord:   "_zorven-challenge." + fqdn,
		TXTValue:    verifyToken,
	}
}

// ValidateDomain, verilen FQDN'in gecerli bir custom domain olup olmadigini kontrol eder.
func ValidateDomain(fqdn, platformDomain string) error {
	fqdn = strings.ToLower(strings.TrimSpace(fqdn))
	if fqdn == "" {
		return ErrInvalidDomain
	}
	// Sonundaki noktayi temizle
	fqdn = strings.TrimSuffix(fqdn, ".")

	if strings.Contains(fqdn, " ") || strings.Contains(fqdn, "/") || strings.Contains(fqdn, ":") {
		return ErrInvalidDomain
	}

	parts := strings.Split(fqdn, ".")
	if len(parts) < 2 {
		return fmt.Errorf("%w: en az bir nokta icermelidir", ErrInvalidDomain)
	}

	for _, p := range parts {
		if len(p) == 0 || len(p) > 63 {
			return fmt.Errorf("%w: etiket uzunlugu 1-63 karakter olmalidir", ErrInvalidDomain)
		}
		if p[0] == '-' || p[len(p)-1] == '-' {
			return fmt.Errorf("%w: etiketler tire ile baslayamaz veya bitemez", ErrInvalidDomain)
		}
	}

	if platformDomain != "" {
		normPlatform := strings.ToLower(strings.TrimSuffix(platformDomain, "."))
		if fqdn == normPlatform || strings.HasSuffix(fqdn, "."+normPlatform) {
			return ErrPlatformSubdomain
		}
	}

	return nil
}

// VerifyCustomDomain, alan adinin platforma ait olup olmadigini CNAME veya TXT ile test eder.
func VerifyCustomDomain(ctx context.Context, fqdn, verifyToken, platformDomain string, verifier DNSVerifier) error {
	fqdn = strings.ToLower(strings.TrimSuffix(strings.TrimSpace(fqdn), "."))
	expectedCNAMETarget := strings.ToLower(strings.TrimSuffix("cname."+platformDomain, "."))
	altCNAMETarget := strings.ToLower(strings.TrimSuffix(platformDomain, "."))

	// 1. CNAME Kontrolu
	cname, err := verifier.LookupCNAME(ctx, fqdn)
	if err == nil {
		normCNAME := strings.ToLower(strings.TrimSuffix(strings.TrimSpace(cname), "."))
		if (expectedCNAMETarget != "" && normCNAME == expectedCNAMETarget) ||
			(altCNAMETarget != "" && (normCNAME == altCNAMETarget || strings.HasSuffix(normCNAME, "."+altCNAMETarget))) {
			return nil // CNAME eslesti!
		}
	}

	// 2. TXT Challenge Kontrolu (_zorven-challenge.<fqdn> veya legacy _rpsh-challenge.<fqdn>)
	for _, challengeHost := range []string{"_zorven-challenge." + fqdn, "_rpsh-challenge." + fqdn} {
		txts, err := verifier.LookupTXT(ctx, challengeHost)
		if err == nil {
			for _, txt := range txts {
				if strings.TrimSpace(txt) == strings.TrimSpace(verifyToken) {
					return nil // TXT eslesti!
				}
			}
		}
	}

	return fmt.Errorf("%w: CNAME '%s' -> '%s' veya TXT '_zorven-challenge.%s' -> '%s' bulunamadi",
		ErrVerificationFailed, fqdn, expectedCNAMETarget, fqdn, verifyToken)
}
