package domain

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/netip"
	"strings"

	"golang.org/x/net/publicsuffix"
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
	// Apex, ad kayit edilebilir alan adinin kendisi mi (or. example.org). DNS
	// standardi kok adda CNAME'e izin vermez; bu durumda A kaydi kullanilmali.
	Apex bool `json:"apex"`
	// ARecords, A kaydina yazilacak platform IPv4 adresleri (cname.<platform>'un
	// cozdugu adresler). Kok adda zorunlu yol, alt alanda CNAME'e alternatif.
	ARecords []string `json:"a_records,omitempty"`
	// RecordName, DNS panelinin "ad" alanina yazilacak etiket (kayit edilebilir
	// alana gore): example.org -> "@", app.example.org -> "app". Cogu panel tam adi
	// degil yalniz etiketi ister; tam ad yazilirsa app.example.org.example.org olur.
	RecordName string `json:"record_name,omitempty"`
}

// Zone, fqdn'in kayit edilebilir alani (app.example.org -> example.org); hata olursa fqdn.
func Zone(fqdn string) string {
	fqdn = strings.ToLower(strings.TrimSuffix(strings.TrimSpace(fqdn), "."))
	if z, err := publicsuffix.EffectiveTLDPlusOne(fqdn); err == nil {
		return z
	}
	return fqdn
}

// RecordLabel, fqdn'in kayit edilebilir alana gore etiketini doner ("@" kok icin).
func RecordLabel(fqdn string) string {
	fqdn = strings.ToLower(strings.TrimSuffix(strings.TrimSpace(fqdn), "."))
	etld1, err := publicsuffix.EffectiveTLDPlusOne(fqdn)
	if err != nil || etld1 == fqdn {
		return "@"
	}
	return strings.TrimSuffix(fqdn, "."+etld1)
}

// IsApex, fqdn'in kayit edilebilir alan adinin kendisi olup olmadigini soyler
// (Public Suffix List: example.org, ornek.com.tr -> true; app.example.org -> false).
func IsApex(fqdn string) bool {
	fqdn = strings.ToLower(strings.TrimSuffix(strings.TrimSpace(fqdn), "."))
	etld1, err := publicsuffix.EffectiveTLDPlusOne(fqdn)
	return err == nil && etld1 == fqdn
}

// PlatformIPv4s, kok adlar icin A kaydi hedefi olacak platform IPv4 adreslerini
// (cname.<platform> cozumlemesi) doner. Hata/bos durumda nil.
func PlatformIPv4s(ctx context.Context, r IPResolver, platformDomain string) []string {
	if r == nil {
		r = net.DefaultResolver
	}
	target := "cname.zorven.app"
	if platformDomain != "" {
		target = "cname." + platformDomain
	}
	addrs, err := r.LookupIPAddr(ctx, target)
	if err != nil {
		return nil
	}
	var out []string
	for _, a := range addrs {
		if v4 := a.IP.To4(); v4 != nil {
			out = append(out, v4.String())
		}
	}
	return out
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
		Apex:        IsApex(fqdn),
		RecordName:  RecordLabel(fqdn),
	}
}

// reservedDomainSuffixes, genel DNS'te anlamli olmayan ic ag son ekleridir.
// (.test/.example bilincli olarak ENGELLENMEZ: staging/e2e testleri kullanir.)
var reservedDomainSuffixes = []string{".localhost", ".local", ".internal"}

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

	if ip, perr := netip.ParseAddr(fqdn); perr == nil && ip.IsValid() {
		return fmt.Errorf("%w: IP adresi alan adi olamaz", ErrInvalidDomain)
	}
	if fqdn == "localhost" {
		return fmt.Errorf("%w: localhost kullanilamaz", ErrInvalidDomain)
	}
	for _, suf := range reservedDomainSuffixes {
		if strings.HasSuffix(fqdn, suf) {
			return fmt.Errorf("%w: ayrilmis/ic ag alan adi son eki (%s)", ErrInvalidDomain, suf)
		}
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

	// 1. CNAME Kontrolu
	cname, err := verifier.LookupCNAME(ctx, fqdn)
	if err == nil {
		normCNAME := strings.ToLower(strings.TrimSuffix(strings.TrimSpace(cname), "."))
		// Yalniz belgelenmis hedef birebir kabul edilir (baska *.platform
		// hedefleri, ornegin baskasinin tuneli, sahiplik kaniti degildir).
		if expectedCNAMETarget != "" && normCNAME == expectedCNAMETarget {
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
