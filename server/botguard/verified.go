package botguard

import (
	"context"
	"net/netip"
	"strings"
	"sync"
	"time"
)

// Resolver, ters/ileri DNS arayuzu (net.Resolver uyar; testlerde sahte).
type Resolver interface {
	LookupAddr(ctx context.Context, addr string) ([]string, error)
	LookupHost(ctx context.Context, host string) ([]string, error)
}

// goodBot, UA sozcugu -> resmi ters DNS alan adi sonekleri. Yalniz arama motoru
// ureticilerinin YAYIMLADIGI alanlar; DuckDuckGo yalniz IP listesi yayimladigi
// icin dahil degildir (dogrulanamaz = supheli skorlamaya duser).
var goodBots = []struct {
	name     string
	uaTokens []string
	suffixes []string
}{
	{"googlebot", []string{"googlebot", "google-inspectiontool", "googleother", "adsbot-google", "storebot-google", "apis-google", "mediapartners-google", "feedfetcher-google"},
		[]string{".googlebot.com", ".google.com", ".googleusercontent.com"}},
	{"bingbot", []string{"bingbot", "msnbot", "adidxbot", "bingpreview"}, []string{".search.msn.com"}},
	{"applebot", []string{"applebot"}, []string{".applebot.apple.com"}},
	{"yandexbot", []string{"yandex"}, []string{".yandex.ru", ".yandex.net", ".yandex.com"}},
	{"baiduspider", []string{"baiduspider"}, []string{".baidu.com", ".baidu.jp"}},
}

// ClaimedBot, UA bilinen bir arama botu iddiasinda mi; bot adini doner.
func ClaimedBot(ua string) (string, bool) {
	l := strings.ToLower(ua)
	for _, b := range goodBots {
		for _, t := range b.uaTokens {
			if strings.Contains(l, t) {
				return b.name, true
			}
		}
	}
	return "", false
}

type vbEntry struct {
	ok      bool
	pending bool
	exp     time.Time
	done    chan struct{}
}

// VerifiedBots, ters DNS (IP -> ad -> sonek eslesmesi) + ileri DNS (ad -> IP
// icerir) ile bot dogrulayicisidir. Sonuclar (olumlu ve olumsuz) TTL ile
// onbelleklenir. Arama arka planda tek-ucus (IP+bot basina tek) calisir; cagiran
// en fazla Wait kadar bekler, sure dolarsa "dogrulanamadi" sayilir ve sonuc
// onbellege sonradan yazilir.
type VerifiedBots struct {
	Resolver    Resolver
	PositiveTTL time.Duration // vars. 24 sa
	NegativeTTL time.Duration // vars. 1 sa
	Wait        time.Duration // cagiranin bekleme ust siniri, vars. 300 ms
	LookupTO    time.Duration // DNS zaman asimi, vars. 2 sn
	MaxEntries  int           // vars. 20000
	MaxInflight int           // es zamanli arama ust siniri, vars. 32

	mu    sync.Mutex
	cache map[string]*vbEntry
	sem   chan struct{}
	now   func() time.Time
}

// NewVerifiedBots, varsayilan degerlerle dogrulayici.
func NewVerifiedBots(res Resolver) *VerifiedBots {
	v := &VerifiedBots{Resolver: res, PositiveTTL: 24 * time.Hour, NegativeTTL: time.Hour,
		Wait: 300 * time.Millisecond, LookupTO: 2 * time.Second, MaxEntries: 20000, MaxInflight: 32,
		cache: make(map[string]*vbEntry), now: time.Now}
	v.sem = make(chan struct{}, v.MaxInflight)
	return v
}

// Verify, ip'nin ua'daki iddia edilen botun resmi aginda olup olmadigini soyler.
// UA bilinen bir bot iddiasinda degilse (false, false) doner (claimed=false).
func (v *VerifiedBots) Verify(ip, ua string) (verified, claimed bool) {
	name, ok := ClaimedBot(ua)
	if !ok || v == nil || v.Resolver == nil {
		return false, ok
	}
	key := name + "|" + ip
	v.mu.Lock()
	e := v.cache[key]
	now := v.now()
	if e != nil && !e.pending && now.After(e.exp) {
		delete(v.cache, key)
		e = nil
	}
	if e == nil {
		if len(v.cache) >= v.MaxEntries {
			for k, c := range v.cache {
				if !c.pending && now.After(c.exp) {
					delete(v.cache, k)
				}
			}
			if len(v.cache) >= v.MaxEntries {
				v.mu.Unlock()
				return false, true // dolu: dogrulanamadi
			}
		}
		select {
		case v.sem <- struct{}{}:
		default:
			v.mu.Unlock()
			return false, true // cok fazla es zamanli arama
		}
		e = &vbEntry{pending: true, done: make(chan struct{})}
		v.cache[key] = e
		go v.lookup(key, name, ip, e)
	}
	if !e.pending {
		ok := e.ok
		v.mu.Unlock()
		return ok, true
	}
	done := e.done
	v.mu.Unlock()
	t := time.NewTimer(v.Wait)
	defer t.Stop()
	select {
	case <-done:
		v.mu.Lock()
		ok := e.ok
		v.mu.Unlock()
		return ok, true
	case <-t.C:
		return false, true
	}
}

func (v *VerifiedBots) lookup(key, name, ip string, e *vbEntry) {
	defer func() { <-v.sem }()
	ctx, cancel := context.WithTimeout(context.Background(), v.LookupTO)
	defer cancel()
	ok := v.check(ctx, name, ip)
	ttl := v.NegativeTTL
	if ok {
		ttl = v.PositiveTTL
	}
	v.mu.Lock()
	e.ok, e.pending, e.exp = ok, false, v.now().Add(ttl)
	v.mu.Unlock()
	close(e.done)
}

func (v *VerifiedBots) check(ctx context.Context, name, ip string) bool {
	var suffixes []string
	for _, b := range goodBots {
		if b.name == name {
			suffixes = b.suffixes
		}
	}
	names, err := v.Resolver.LookupAddr(ctx, ip)
	if err != nil {
		return false
	}
	for _, n := range names {
		host := strings.ToLower(strings.TrimSuffix(n, "."))
		match := false
		for _, s := range suffixes {
			if strings.HasSuffix(host, s) {
				match = true
				break
			}
		}
		if !match {
			continue
		}
		addrs, err := v.Resolver.LookupHost(ctx, host)
		if err != nil {
			continue
		}
		for _, a := range addrs {
			if sameIP(a, ip) {
				return true
			}
		}
	}
	return false
}

func sameIP(a, b string) bool {
	x, e1 := netip.ParseAddr(strings.Trim(a, "[]"))
	y, e2 := netip.ParseAddr(strings.Trim(b, "[]"))
	return e1 == nil && e2 == nil && x.Unmap() == y.Unmap()
}
