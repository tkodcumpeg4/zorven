// Package tunsec, tunel seviyesinde WAF ve Geo kurallarinin (ZTNA Z3) saf mantigidir.
//
// Ag ve veritabani cagrisi yoktur: yapilandirma JSON olarak gelir, bir kez
// derlenir (Compile) ve ingress / rawproxy sicak yolunda yalniz hazir yapi
// uygulanir. Ulke cozumu CountryResolver arayuzu arkasindadir (yerel MaxMind /
// DB-IP mmdb dosyasi; bkz. ingress.geoResolver).
package tunsec

import (
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
	"strings"
)

// WAF kipleri.
const (
	WAFOff   = "off"
	WAFLog   = "log"
	WAFBlock = "block"
)

// Geo kipleri.
const (
	GeoOff   = "off"
	GeoAllow = "allow" // yalniz listedeki ulkeler
	GeoDeny  = "deny"  // listedeki ulkeler reddedilir
)

// UnknownCountry, ulke cozulemediginde kullanilan etiket (log/panel).
const UnknownCountry = "unknown"

// Sinirlar.
const (
	maxCountries     = 300
	maxDisabledRules = 100
)

// WAFConfig, tunel WAF ayari.
type WAFConfig struct {
	Mode string `json:"mode"` // off | log | block (bos = off)
	// Body true ise form/JSON/metin govdesinin ILK 8 KB'i da taranir (varsayilan hayir).
	Body bool `json:"body,omitempty"`
	// DisabledRules, kapatilan kural kimlikleri veya kategori adlari.
	DisabledRules []string `json:"disabled_rules,omitempty"`
}

// GeoConfig, tunel ulke kurali.
type GeoConfig struct {
	Mode      string   `json:"mode"`                 // off | allow | deny
	Countries []string `json:"countries,omitempty"`  // ISO 3166-1 alpha-2
	UnknownIs string   `json:"unknown_is,omitempty"` // allow | deny (bos = allow)
}

// Config, tunel guvenlik ayari (tunnel_security.config).
type Config struct {
	WAF WAFConfig `json:"waf"`
	Geo GeoConfig `json:"geo"`
	// Bot (B1-B3) ve Rate (B2): bot korumasi + tunel+IP hiz siniri. Bkz. bot.go.
	Bot  BotConfig  `json:"bot"`
	Rate RateConfig `json:"rate"`
}

var countryRe = regexp.MustCompile(`^[A-Z]{2}$`)

// Normalize, ayari dogrular ve kanonik hale getirir (buyuk harf, tekil, sirali).
func Normalize(c Config) (Config, error) {
	c.WAF.Mode = strings.ToLower(strings.TrimSpace(c.WAF.Mode))
	switch c.WAF.Mode {
	case "":
		c.WAF.Mode = WAFOff
	case WAFOff, WAFLog, WAFBlock:
	default:
		return c, fmt.Errorf("waf.mode: off, log veya block olmali")
	}
	if len(c.WAF.DisabledRules) > maxDisabledRules {
		return c, fmt.Errorf("waf.disabled_rules: en fazla %d oge", maxDisabledRules)
	}
	seen := map[string]bool{}
	var dis []string
	for _, id := range c.WAF.DisabledRules {
		id = strings.ToLower(strings.TrimSpace(id))
		if id == "" || seen[id] {
			continue
		}
		if !KnownRuleOrCategory(id) {
			return c, fmt.Errorf("waf.disabled_rules: bilinmeyen kural %q", id)
		}
		seen[id] = true
		dis = append(dis, id)
	}
	sort.Strings(dis)
	c.WAF.DisabledRules = dis

	c.Geo.Mode = strings.ToLower(strings.TrimSpace(c.Geo.Mode))
	switch c.Geo.Mode {
	case "":
		c.Geo.Mode = GeoOff
	case GeoOff, GeoAllow, GeoDeny:
	default:
		return c, fmt.Errorf("geo.mode: off, allow veya deny olmali")
	}
	c.Geo.UnknownIs = strings.ToLower(strings.TrimSpace(c.Geo.UnknownIs))
	switch c.Geo.UnknownIs {
	case "":
		c.Geo.UnknownIs = "allow"
	case "allow", "deny":
	default:
		return c, fmt.Errorf("geo.unknown_is: allow veya deny olmali")
	}
	if len(c.Geo.Countries) > maxCountries {
		return c, fmt.Errorf("geo.countries: en fazla %d ulke", maxCountries)
	}
	cs := map[string]bool{}
	var list []string
	for _, cc := range c.Geo.Countries {
		cc = strings.ToUpper(strings.TrimSpace(cc))
		if cc == "" || cs[cc] {
			continue
		}
		if !countryRe.MatchString(cc) {
			return c, fmt.Errorf("geo.countries: gecersiz ulke kodu %q", cc)
		}
		cs[cc] = true
		list = append(list, cc)
	}
	sort.Strings(list)
	c.Geo.Countries = list
	if c.Geo.Mode == GeoAllow && len(list) == 0 {
		return c, fmt.Errorf("geo: allow kipinda en az bir ulke gerekli (aksi halde herkes reddedilir)")
	}
	var err error
	if c.Bot, err = normalizeBot(c.Bot); err != nil {
		return c, err
	}
	if c.Rate, err = normalizeRate(c.Rate); err != nil {
		return c, err
	}
	return c, nil
}

// Parse, ham JSON'u ayara cevirir. Bozuk/bos girdi = her sey kapali.
func Parse(raw []byte) Config {
	var c Config
	if len(raw) == 0 {
		return Config{}
	}
	if err := json.Unmarshal(raw, &c); err != nil {
		return Config{}
	}
	n, err := Normalize(c)
	if err != nil {
		// Gecersiz depolanmis ayar: hicbir sey uygulama (API zaten Normalize ile
		// kaydetmeden once reddeder).
		return Config{}
	}
	return n
}

// Compiled, ingress/rawproxy sicak yolu icin hazir ayar. nil = hicbir sey acik degil.
type Compiled struct {
	WAFMode   string
	Body      bool
	off       map[string]bool
	geoMode   string
	countries map[string]struct{}
	unkDeny   bool
	// Bot ve Rate: B1-B3 (bkz. bot.go). Kapaliyken de varsayilanlarla doludur.
	Bot  BotSettings
	Rate RateSettings
}

// Compile, ayari derler. WAF, Geo, bot ve hiz siniri hicbiri acik degilse nil doner.
func Compile(c Config) *Compiled {
	n, err := Normalize(c)
	if err != nil {
		return nil
	}
	if n.WAF.Mode == WAFOff && n.Geo.Mode == GeoOff && n.Bot.Mode == BotOff && !n.Rate.Enabled {
		return nil
	}
	cp := &Compiled{WAFMode: n.WAF.Mode, Body: n.WAF.Body, geoMode: n.Geo.Mode, unkDeny: n.Geo.UnknownIs == "deny",
		Bot: BotSettings{Mode: n.Bot.Mode, Difficulty: n.Bot.Difficulty, TTLMinutes: n.Bot.TTLMinutes,
			ExcludePaths: n.Bot.ExcludePaths, AllowVerified: n.Bot.AllowVerifiedBots, BlockEmptyUA: n.Bot.BlockEmptyUA},
		Rate: RateSettings{Enabled: n.Rate.Enabled, Requests: n.Rate.Requests, WindowSeconds: n.Rate.WindowSeconds,
			Action: n.Rate.Action, ExcludePaths: n.Rate.ExcludePaths}}
	if len(n.WAF.DisabledRules) > 0 {
		cp.off = make(map[string]bool, len(n.WAF.DisabledRules))
		for _, id := range n.WAF.DisabledRules {
			cp.off[id] = true
		}
	}
	if len(n.Geo.Countries) > 0 {
		cp.countries = make(map[string]struct{}, len(n.Geo.Countries))
		for _, cc := range n.Geo.Countries {
			cp.countries[cc] = struct{}{}
		}
	}
	return cp
}

// GeoActive, ulke kurali acik mi.
func (c *Compiled) GeoActive() bool { return c != nil && c.geoMode != GeoOff }

// WAFActive, WAF log veya block kipinde mi.
func (c *Compiled) WAFActive() bool { return c != nil && c.WAFMode != WAFOff }

// CountryResolver, IP -> ulke. ok=false: cozulemedi (DB yok, ozel/bilinmeyen IP).
type CountryResolver interface {
	Country(ip string) (code string, ok bool)
}

// GeoAllows, ulkeye gore izin karari. known=false ise unknown_is uygulanir.
func (c *Compiled) GeoAllows(country string, known bool) bool {
	if !c.GeoActive() {
		return true
	}
	if !known || country == "" {
		return !c.unkDeny
	}
	_, in := c.countries[strings.ToUpper(country)]
	if c.geoMode == GeoAllow {
		return in
	}
	return !in
}

// GeoCheck, IP icin karar + log icin ulke etiketi (cozulemezse "unknown").
func (c *Compiled) GeoCheck(res CountryResolver, ip string) (allowed bool, country string) {
	if !c.GeoActive() {
		return true, ""
	}
	country = UnknownCountry
	known := false
	if res != nil {
		if cc, ok := res.Country(ip); ok && cc != "" {
			country, known = strings.ToUpper(cc), true
		}
	}
	return c.GeoAllows(country, known), country
}
