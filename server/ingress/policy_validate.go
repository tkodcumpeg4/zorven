package ingress

import (
	"bytes"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
)

// ValidatePolicyConfig, policy config JSON'unu KAYIT zamaninda dogrular.
// compilePolicy gecersiz kurallari sessizce dusurur (fail-closed/noop); bu yuzden
// kullanici "kaydedildi" gorup kuralin hic calismadigini fark etmezdi. API bu
// fonksiyonla 422 doner. Bos config ({} veya bos) gecerlidir (kural yok).
func ValidatePolicyConfig(raw []byte) error {
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 || string(raw) == "null" {
		return nil
	}
	var cfg rawPolicyConfig
	dec := json.NewDecoder(bytes.NewReader(raw))
	if err := dec.Decode(&cfg); err != nil {
		return fmt.Errorf("config gecerli bir JSON nesnesi olmali: %v", err)
	}
	for i, r := range cfg.Rules {
		if err := validatePolicyRule(r); err != nil {
			return fmt.Errorf("kural %d: %v", i+1, err)
		}
	}
	return nil
}

var rateKeyHeaderRe = regexp.MustCompile(`^header:[A-Za-z0-9-]+$`)

func validatePolicyRule(r rawPolicyRule) error {
	m := r.Match
	for _, mm := range m.Methods {
		if strings.TrimSpace(mm) == "" {
			return fmt.Errorf("methods bos eleman iceremez")
		}
	}
	for _, c := range m.Country {
		if len(strings.TrimSpace(c)) != 2 {
			return fmt.Errorf("country ISO 3166-1 alpha-2 olmali: %q", c)
		}
	}
	a := r.Action
	switch strings.ToLower(strings.TrimSpace(a.Type)) {
	case "deny":
		if a.Status != 0 && (a.Status < 400 || a.Status > 599) {
			return fmt.Errorf("deny status 400-599 olmali")
		}
	case "redirect":
		if strings.TrimSpace(a.Location) == "" {
			return fmt.Errorf("redirect icin location gerekli")
		}
		if a.Status != 0 && (a.Status < 300 || a.Status > 399) {
			return fmt.Errorf("redirect status 300-399 olmali")
		}
	case "require_mtls":
	case "set_header":
		if len(a.Request.Set) == 0 && len(a.Request.Remove) == 0 &&
			len(a.Response.Set) == 0 && len(a.Response.Remove) == 0 {
			return fmt.Errorf("set_header icin en az bir baslik tanimlanmali")
		}
	case "rate_limit":
		if a.Requests <= 0 {
			return fmt.Errorf("rate_limit icin requests > 0 olmali")
		}
		if a.WindowSec < 0 || a.Burst < 0 {
			return fmt.Errorf("window_sec ve burst negatif olamaz")
		}
		k := strings.TrimSpace(a.Key)
		if k != "" && k != "ip" && k != "tunnel" && !rateKeyHeaderRe.MatchString(k) {
			return fmt.Errorf("rate_limit key ip, tunnel veya header:<Ad> olmali")
		}
	case "verify_webhook":
		if !isKnownWebhookProvider(strings.ToLower(strings.TrimSpace(a.Provider))) {
			return fmt.Errorf("bilinmeyen webhook saglayicisi: %q", a.Provider)
		}
		if strings.TrimSpace(a.SecretRef) == "" {
			return fmt.Errorf("verify_webhook icin secret_ref gerekli")
		}
	case "waf":
		if _, ok := wafRuleset(a.Ruleset); !ok {
			return fmt.Errorf("bilinmeyen waf kumesi: %q", a.Ruleset)
		}
		if _, ok := compileWAFPatterns(a.Patterns); !ok {
			return fmt.Errorf("waf patterns icinde gecersiz regex var")
		}
	default:
		return fmt.Errorf("bilinmeyen action.type: %q", a.Type)
	}
	return nil
}
