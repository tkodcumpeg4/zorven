package ingress

// Geo/IP cozumleyici (FAZ 1 Part 2 / F03a).
//
// TASARIM KARARI (K2): Zorven HICBIR GeoIP veritabani dagitmaz. Okuyucu herhangi
// bir mmdb dosyasini okur; yol isletmeci tarafindan ZORVEN_GEOIP_DB ile verilir.
// Boylece lisans sorusu urunun degil dagitimin sorunu olur ve kurum kaynagini
// kendi secer (DB-IP Lite / IPinfo Lite / GeoLite2 / ticari).
//
// Cozum istek basina BIR KEZ yapilir ve o istegin tum Geo kosullarinda paylasilir.
// DB yoksa veya IP cozulemezse kosul FALSE doner: yani Geo kosullu bir deny
// TETIKLENMEZ ve istek normal akisina devam eder. Bu bilincli bir tercihtir —
// panelde "Geo kurali tek basina guvenlik kapisi degildir" uyarisi gosterilmeli.

import (
	"net/netip"
	"strings"

	"github.com/oschwald/maxminddb-golang/v2"
)

// ipInfo, tek bir IP icin cozulmus bilgiler. ok=false ise hicbir Geo kosulu eslesmez.
type ipInfo struct {
	country   string // ISO 3166-1 alpha-2, buyuk harf
	continent string // kita kodu, buyuk harf
	asn       uint
	isp       string // ASN organizasyon adi
	ok        bool
}

// geoRecord, mmdb kaydinin cozulecek alanlari. Saglayicilar farkli alt kumeler
// tasir; eksik alanlar sifir degerde kalir ve ilgili kosul eslesmez.
type geoRecord struct {
	Country struct {
		ISOCode string `maxminddb:"iso_code"`
	} `maxminddb:"country"`
	Continent struct {
		Code string `maxminddb:"code"`
	} `maxminddb:"continent"`
	ASN    uint   `maxminddb:"autonomous_system_number"`
	ASNOrg string `maxminddb:"autonomous_system_organization"`
}

// geoResolver, mmdb okuyucusu. db nil ise ozellik kapalidir.
type geoResolver struct {
	db *maxminddb.Reader
}

// newGeoResolver, verilen yoldan mmdb acar. Yol bos ise (ozellik kapali) nil
// resolver ve nil hata doner — bu bir hata durumu DEGILDIR.
func newGeoResolver(path string) (*geoResolver, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		return nil, nil
	}
	db, err := maxminddb.Open(path)
	if err != nil {
		return nil, err
	}
	return &geoResolver{db: db}, nil
}

// close, acik mmdb dosyasini kapatir.
func (g *geoResolver) close() {
	if g != nil && g.db != nil {
		_ = g.db.Close()
	}
}

// lookup, IP icin bilgileri cozer. Resolver veya DB yoksa ok=false doner.
func (g *geoResolver) lookup(ip string) ipInfo {
	if g == nil || g.db == nil {
		return ipInfo{}
	}
	addr, err := netip.ParseAddr(strings.TrimSpace(ip))
	if err != nil {
		return ipInfo{}
	}
	var rec geoRecord
	res := g.db.Lookup(addr)
	if err := res.Decode(&rec); err != nil {
		return ipInfo{}
	}
	return ipInfo{
		country:   strings.ToUpper(rec.Country.ISOCode),
		continent: strings.ToUpper(rec.Continent.Code),
		asn:       rec.ASN,
		isp:       rec.ASNOrg,
		ok:        true,
	}
}
