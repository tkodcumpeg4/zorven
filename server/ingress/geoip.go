package ingress

// Geo/IP cozumleyici (FAZ 1 Part 2 / F03a; ZTNA Z3).
//
// TASARIM KARARI (K2): Zorven HICBIR GeoIP veritabani dagitmaz. Okuyucu herhangi
// bir mmdb dosyasini okur; yol isletmeci tarafindan ZORVEN_GEOIP_DB ile verilir
// (vars. /app/data/GeoLite2-Country.mmdb). Boylece lisans sorusu urunun degil
// dagitimin sorunu olur ve kurum kaynagini kendi secer (DB-IP Lite / IPinfo Lite /
// GeoLite2 / ticari). Ag cagrisi YOKTUR.
//
// Cozum istek basina BIR KEZ yapilir ve o istegin tum Geo kosullarinda paylasilir.
// DB yoksa veya IP cozulemezse ulke "unknown" sayilir: politika kosullari
// eslesmez, tunel Geo ayari ise unknown_is (allow|deny) ile cozulur.

import (
	"net/netip"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"time"

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

// geoRecheckEvery, dosya degisiklik (mtime) denetiminin en sik yapilacagi aralik.
const geoRecheckEvery = 30 * time.Second

// geoCloseDelay, yeniden yuklemede eski okuyucunun kapatilmasindan once beklenen sure
// (o anda arama yapan istekler bitsin).
const geoCloseDelay = 2 * time.Minute

// geoLookuper, Router'in ihtiyac duydugu cozum arayuzu (testlerde sahte uygulanir).
type geoLookuper interface {
	lookup(ip string) ipInfo
}

// geoResolver, mmdb okuyucusu. Dosya degisince (mtime) otomatik yeniden yuklenir;
// dosya sonradan eklenirse de yuklenir (aylik guncelleme icin yeniden baslatma
// gerekmez). nil resolver = ozellik kapali.
type geoResolver struct {
	path      string
	db        atomic.Pointer[maxminddb.Reader]
	mtime     atomic.Int64 // yuklu dosyanin UnixNano degisiklik zamani
	nextCheck atomic.Int64
	mu        sync.Mutex // yeniden yuklemeyi serilestirir
	now       func() time.Time
}

// newGeoResolver, verilen yoldan mmdb acar. Yol bos ise (ozellik kapali) nil
// resolver ve nil hata doner — bu bir hata durumu DEGILDIR. Dosya acilamazsa hata.
func newGeoResolver(path string) (*geoResolver, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		return nil, nil
	}
	g := &geoResolver{path: path, now: time.Now}
	if err := g.load(); err != nil {
		return nil, err
	}
	return g, nil
}

// newAutoGeoResolver, dosya henuz yoksa bile hata vermez: dosya belirince (mtime
// denetimiyle) yuklenir; o zamana dek ulke "unknown" kalir.
func newAutoGeoResolver(path string) *geoResolver {
	path = strings.TrimSpace(path)
	if path == "" {
		return nil
	}
	g := &geoResolver{path: path, now: time.Now}
	_ = g.load()
	return g
}

// load, dosyayi acar ve okuyucuyu degistirir.
func (g *geoResolver) load() error {
	fi, err := os.Stat(g.path)
	if err != nil {
		return err
	}
	db, err := maxminddb.Open(g.path)
	if err != nil {
		return err
	}
	old := g.db.Swap(db)
	g.mtime.Store(fi.ModTime().UnixNano())
	if old != nil {
		time.AfterFunc(geoCloseDelay, func() { _ = old.Close() })
	}
	return nil
}

// maybeReload, en fazla geoRecheckEvery'de bir dosya zamanini denetler; degistiyse
// yeniden yukler. Acilamayan/bozuk yeni dosyada ESKI veritabani korunur.
func (g *geoResolver) maybeReload() {
	nowNs := g.now().UnixNano()
	if nowNs < g.nextCheck.Load() {
		return
	}
	if !g.mu.TryLock() {
		return
	}
	defer g.mu.Unlock()
	g.nextCheck.Store(nowNs + int64(geoRecheckEvery))
	fi, err := os.Stat(g.path)
	if err != nil {
		return
	}
	if fi.ModTime().UnixNano() == g.mtime.Load() && g.db.Load() != nil {
		return
	}
	_ = g.load()
}

// close, acik mmdb dosyasini kapatir.
func (g *geoResolver) close() {
	if g == nil {
		return
	}
	if old := g.db.Swap(nil); old != nil {
		_ = old.Close()
	}
}

// loaded, veritabani yuklu mu (durum bilgisi).
func (g *geoResolver) loaded() bool { return g != nil && g.db.Load() != nil }

// lookup, IP icin bilgileri cozer. Resolver veya DB yoksa ok=false doner.
func (g *geoResolver) lookup(ip string) ipInfo {
	if g == nil {
		return ipInfo{}
	}
	g.maybeReload()
	db := g.db.Load()
	if db == nil {
		return ipInfo{}
	}
	addr, err := netip.ParseAddr(strings.TrimSpace(ip))
	if err != nil {
		return ipInfo{}
	}
	addr = addr.Unmap()
	res := db.Lookup(addr)
	if !res.Found() {
		return ipInfo{}
	}
	var rec geoRecord
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
