// Package mcpconf, tunel "MCP" modu ayarinin (tunnel_mcp.config) tek kaynakli
// sema, dogrulama ve eslestirme mantigidir. Hem API (kaydederken dogrular) hem de
// ingress (Reload'da parse eder) bunu kullanir; boylece ikisi ayri kurallar
// uygulayamaz.
package mcpconf

import (
	"encoding/json"
	"fmt"
	"net"
	"strings"
	"time"
)

// Kimlik dogrulama yontemleri.
const (
	AuthOAuth  = "oauth"
	AuthAPIKey = "apikey"
	AuthNone   = "none"
)

// Tasimalar.
const (
	TransportStreamable = "streamable"
	TransportSSE        = "sse"
	TransportAuto       = "auto"
)

// Log duzeyleri.
const (
	LogMeta = "meta"
	LogNone = "none"
)

// Sinirlar ve varsayilanlar.
const (
	DefaultPath           = "/mcp"
	DefaultHeadTimeoutSec = 300
	MaxHeadTimeoutSec     = 900
	DefaultIdleTimeoutSec = 900
	MaxIdleTimeoutSec     = 3600
	DefaultMaxRequest     = 1 << 20
	MaxMaxRequest         = 8 << 20
	MaxRatePerMin         = 6000
	MaxToolPatterns       = 100
	MaxToolPatternLen     = 128
	MaxAllowedEmails      = 200
	DefaultMaxStreams     = 20
)

// Config, kullanicinin gordugu/duzenledigi MCP ayari (JSON).
type Config struct {
	Path      string `json:"path"`
	Transport string `json:"transport"`
	Auth      string `json:"auth"`
	// AckNoAuth, auth=none icin acik onay (panelde kirmizi uyari).
	AckNoAuth bool `json:"ack_no_auth,omitempty"`

	// Arac politikasi (M4). DenyTools onceliklidir; AllowTools doluysa listede
	// olmayan arac reddedilir. Desen: "*" ve "?" jokerleri.
	AllowTools []string `json:"allow_tools"`
	DenyTools  []string `json:"deny_tools"`

	LogLevel        string `json:"log_level"`
	MaxRequestBytes int64  `json:"max_request_bytes"`
	HeadTimeoutSec  int    `json:"head_timeout_sec"`
	IdleTimeoutSec  int    `json:"idle_timeout_sec"`
	// RateLimitPerMin, dakikada tools/call (ve diger istek) sayisi siniri; 0 = yok.
	RateLimitPerMin int `json:"rate_limit_per_min"`
	MaxStreams      int `json:"max_streams"`

	// Kimlik kaynagi (auth=oauth): Zorven hesabi sahibi olan ve (a) tunel
	// organizasyonunun uyesi (AllowOrgMembers) ya da (b) e-postasi AllowedEmails
	// listesinde olan kullanicilar.
	AllowOrgMembers bool     `json:"allow_org_members"`
	AllowedEmails   []string `json:"allowed_emails"`
}

// Default, bos ayar icin guvenli varsayilanlar (auth=oauth, yalniz org uyeleri).
func Default() Config {
	return Config{
		Path: DefaultPath, Transport: TransportAuto, Auth: AuthOAuth, LogLevel: LogMeta,
		MaxRequestBytes: DefaultMaxRequest, HeadTimeoutSec: DefaultHeadTimeoutSec,
		IdleTimeoutSec: DefaultIdleTimeoutSec, MaxStreams: DefaultMaxStreams,
		AllowOrgMembers: true,
		AllowTools:      []string{}, DenyTools: []string{}, AllowedEmails: []string{},
	}
}

// Parse, ham JSON'u cozer ve Normalize eder. Hata durumunda guvenli varsayilan
// DEGIL hata doner (API kaydi reddeder); ingress hata alirsa MCP'yi KAPALI sayar
// (fail-closed: yanlis ayar korumayi sessizce gevsetmesin diye ingress tarafi
// ayrica ParseLenient kullanir).
func Parse(raw []byte) (Config, error) {
	c := Default()
	if len(raw) > 0 && string(raw) != "{}" && string(raw) != "null" {
		if err := json.Unmarshal(raw, &c); err != nil {
			return Config{}, fmt.Errorf("gecersiz mcp ayari: %w", err)
		}
	}
	if err := c.Normalize(); err != nil {
		return Config{}, err
	}
	return c, nil
}

// ParseLenient, bozuk ayarda bile calisir: gecersiz alanlari en kisitlayici
// degere ceker (auth bozuksa oauth, listeler korunur). Ingress icin.
func ParseLenient(raw []byte) Config {
	c, err := Parse(raw)
	if err == nil {
		return c
	}
	d := Default()
	var probe Config
	if json.Unmarshal(raw, &probe) == nil {
		// Yalnizca guvenli alanlar: izin/ret listeleri ve auth korunur.
		d.AllowTools, d.DenyTools = probe.AllowTools, probe.DenyTools
		if probe.Auth == AuthAPIKey {
			d.Auth = AuthAPIKey
		}
		d.AllowedEmails = probe.AllowedEmails
		d.AllowOrgMembers = probe.AllowOrgMembers
		d.RateLimitPerMin = probe.RateLimitPerMin
	}
	if d.Normalize() != nil {
		d = Default()
	}
	return d
}

// Normalize alanlari dogrular ve varsayilanlari uygular.
func (c *Config) Normalize() error {
	if c.Path == "" {
		c.Path = DefaultPath
	}
	p, err := CleanPath(c.Path)
	if err != nil {
		return err
	}
	c.Path = p
	switch c.Transport {
	case "":
		c.Transport = TransportAuto
	case TransportStreamable, TransportSSE, TransportAuto:
	default:
		return fmt.Errorf("transport streamable, sse veya auto olmali")
	}
	switch c.Auth {
	case "":
		c.Auth = AuthOAuth
	case AuthOAuth, AuthAPIKey:
	case AuthNone:
		if !c.AckNoAuth {
			return fmt.Errorf("auth=none icin ack_no_auth=true ile acik onay gerekli")
		}
	default:
		return fmt.Errorf("auth oauth, apikey veya none olmali")
	}
	switch c.LogLevel {
	case "":
		c.LogLevel = LogMeta
	case LogMeta, LogNone:
	default:
		return fmt.Errorf("log_level meta veya none olmali")
	}
	if c.MaxRequestBytes <= 0 {
		c.MaxRequestBytes = DefaultMaxRequest
	}
	if c.MaxRequestBytes > MaxMaxRequest {
		return fmt.Errorf("max_request_bytes en fazla %d", MaxMaxRequest)
	}
	if c.HeadTimeoutSec <= 0 {
		c.HeadTimeoutSec = DefaultHeadTimeoutSec
	}
	if c.HeadTimeoutSec > MaxHeadTimeoutSec {
		return fmt.Errorf("head_timeout_sec en fazla %d", MaxHeadTimeoutSec)
	}
	if c.IdleTimeoutSec <= 0 {
		c.IdleTimeoutSec = DefaultIdleTimeoutSec
	}
	if c.IdleTimeoutSec > MaxIdleTimeoutSec {
		return fmt.Errorf("idle_timeout_sec en fazla %d", MaxIdleTimeoutSec)
	}
	if c.RateLimitPerMin < 0 || c.RateLimitPerMin > MaxRatePerMin {
		return fmt.Errorf("rate_limit_per_min 0..%d olmali", MaxRatePerMin)
	}
	if c.MaxStreams <= 0 {
		c.MaxStreams = DefaultMaxStreams
	}
	if c.MaxStreams > 200 {
		return fmt.Errorf("max_streams en fazla 200")
	}
	if c.AllowTools, err = cleanPatterns(c.AllowTools, "allow_tools"); err != nil {
		return err
	}
	if c.DenyTools, err = cleanPatterns(c.DenyTools, "deny_tools"); err != nil {
		return err
	}
	if len(c.AllowedEmails) > MaxAllowedEmails {
		return fmt.Errorf("allowed_emails en fazla %d", MaxAllowedEmails)
	}
	emails := make([]string, 0, len(c.AllowedEmails))
	for _, e := range c.AllowedEmails {
		e = strings.ToLower(strings.TrimSpace(e))
		if e == "" {
			continue
		}
		if len(e) > 254 || strings.ContainsAny(e, " \t\r\n\x00") {
			return fmt.Errorf("allowed_emails: gecersiz oge")
		}
		emails = append(emails, e)
	}
	c.AllowedEmails = emails
	return nil
}

// HeadTimeout / IdleTimeout, sureye cevrilmis degerler.
func (c Config) HeadTimeout() time.Duration { return time.Duration(c.HeadTimeoutSec) * time.Second }
func (c Config) IdleTimeout() time.Duration { return time.Duration(c.IdleTimeoutSec) * time.Second }

// HasToolPolicy, arac izin/ret listesi tanimli mi.
func (c Config) HasToolPolicy() bool { return len(c.AllowTools) > 0 || len(c.DenyTools) > 0 }

// ToolAllowed, aracin politika geregi cagrilabilir/listelenebilir olup olmadigi.
// "!desen" bicimindeki allow_tools ogeleri ret deseni sayilir.
func (c Config) ToolAllowed(name string) bool {
	allowAny := false
	allowMatch := false
	for _, p := range c.AllowTools {
		if neg, ok := strings.CutPrefix(p, "!"); ok {
			if Glob(neg, name) {
				return false
			}
			continue
		}
		allowAny = true
		if Glob(p, name) {
			allowMatch = true
		}
	}
	for _, p := range c.DenyTools {
		if Glob(strings.TrimPrefix(p, "!"), name) {
			return false
		}
	}
	if allowAny && !allowMatch {
		return false
	}
	return true
}

// IsMCPPath, kanonik yolun bu ayarin MCP uc noktalarindan biri olup olmadigi.
func (c Config) IsMCPPath(canon string) bool {
	if canon == c.Path || canon == strings.TrimSuffix(c.Path, "/") {
		return true
	}
	if c.Transport != TransportStreamable && (canon == "/sse" || canon == "/messages") {
		return true
	}
	return false
}

// IsLegacyPath, 2024-11-05 HTTP+SSE tasimasi yollari (/sse, /messages).
func (c Config) IsLegacyPath(canon string) bool {
	return c.Transport != TransportStreamable && (canon == "/sse" || canon == "/messages") && canon != c.Path
}

// CleanPath, MCP yolunu dogrular: "/" ile baslar, ".." / "//" / sorgu / kontrol
// karakteri icermez, .well-known altinda olamaz.
func CleanPath(p string) (string, error) {
	p = strings.TrimSpace(p)
	if !strings.HasPrefix(p, "/") || len(p) > 128 {
		return "", fmt.Errorf("path '/' ile baslamali (en fazla 128 karakter)")
	}
	if strings.ContainsAny(p, "?#%\\ \t\r\n\x00") || strings.Contains(p, "//") || strings.Contains(p, "/./") ||
		strings.Contains(p, "/../") || strings.HasSuffix(p, "/..") || strings.HasSuffix(p, "/.") {
		return "", fmt.Errorf("path gecersiz karakter veya dizi iceriyor")
	}
	for _, r := range p {
		if r < 0x20 || r > 0x7e {
			return "", fmt.Errorf("path yalnizca yazdirilabilir ASCII icermeli")
		}
	}
	if strings.HasPrefix(p, "/.well-known") || strings.HasPrefix(p, "/_zv") {
		return "", fmt.Errorf("path ayrilmis bir on-ek kullaniyor")
	}
	if len(p) > 1 {
		p = strings.TrimRight(p, "/")
		if p == "" {
			p = "/"
		}
	}
	return p, nil
}

func cleanPatterns(in []string, field string) ([]string, error) {
	if len(in) > MaxToolPatterns {
		return nil, fmt.Errorf("%s en fazla %d oge", field, MaxToolPatterns)
	}
	out := make([]string, 0, len(in))
	seen := map[string]bool{}
	for _, p := range in {
		p = strings.TrimSpace(p)
		if p == "" || seen[p] {
			continue
		}
		if len(p) > MaxToolPatternLen {
			return nil, fmt.Errorf("%s: desen cok uzun", field)
		}
		for _, r := range p {
			if r < 0x21 || r > 0x7e {
				return nil, fmt.Errorf("%s: desen yalnizca bosluksuz yazdirilabilir ASCII icermeli", field)
			}
		}
		seen[p] = true
		out = append(out, p)
	}
	return out, nil
}

// Glob, "*" (sifir veya daha fazla karakter) ve "?" (tek karakter) jokerli
// eslestirme. Buyuk/kucuk harf duyarli. Ozyinelemesiz (O(n*m) en kotu durum).
func Glob(pattern, s string) bool {
	px, sx := 0, 0
	star, mark := -1, 0
	for sx < len(s) {
		switch {
		case px < len(pattern) && (pattern[px] == '?' || pattern[px] == s[sx]) && pattern[px] != '*':
			px++
			sx++
		case px < len(pattern) && pattern[px] == '*':
			star = px
			mark = sx
			px++
		case star >= 0:
			px = star + 1
			mark++
			sx = mark
		default:
			return false
		}
	}
	for px < len(pattern) && pattern[px] == '*' {
		px++
	}
	return px == len(pattern)
}

// CanonicalResource, bir MCP kaynaginin kanonik anahtarini uretir: kucuk harfli
// host (varsayilan port atilir) + yol; sema DAHIL DEGILDIR (token aud
// karsilastirmasi sema-duyarsizdir). Ornek: "acme.zorven.app/mcp".
func CanonicalResource(host, path string) string {
	host = strings.ToLower(strings.TrimSpace(host))
	if h, p, err := net.SplitHostPort(host); err == nil && (p == "443" || p == "80") {
		host = h
	}
	host = strings.TrimSuffix(host, ".")
	if path == "" {
		path = "/"
	}
	if len(path) > 1 {
		path = strings.TrimRight(path, "/")
	}
	return host + path
}

// ResourceKey, tam bir kaynak URL'sinden ("https://acme.zorven.app/mcp") kanonik
// anahtari cikarir. Gecersizse ("", false).
func ResourceKey(rawURL string) (string, bool) {
	rest, ok := strings.CutPrefix(rawURL, "https://")
	if !ok {
		rest, ok = strings.CutPrefix(rawURL, "http://")
		if !ok {
			return "", false
		}
	}
	if strings.ContainsAny(rest, "?#@\\ ") {
		return "", false
	}
	host, path, _ := strings.Cut(rest, "/")
	if host == "" {
		return "", false
	}
	return CanonicalResource(host, "/"+path), true
}

// EmailListed, e-postanin listede olup olmadigi. BOS LISTE = kimse (visitorauth'un
// "bos = dogrulanmis herkes" davranisindan bilerek farkli). "*@alan.com" jokeri
// desteklenir; karsilastirma buyuk/kucuk harf duyarsizdir.
func EmailListed(list []string, email string) bool {
	email = strings.ToLower(strings.TrimSpace(email))
	if email == "" {
		return false
	}
	for _, a := range list {
		a = strings.ToLower(strings.TrimSpace(a))
		if a == email {
			return true
		}
		if dom, ok := strings.CutPrefix(a, "*@"); ok && dom != "" && strings.HasSuffix(email, "@"+dom) {
			return true
		}
	}
	return false
}
