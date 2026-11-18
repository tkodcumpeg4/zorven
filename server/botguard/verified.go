package botguard

import (
	"context"
	_ "embed"
	"encoding/json"
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

// goodBots, ters+ileri DNS ile dogrulanan arama botlari: UA sozcugu -> ureticinin YAYIMLADIGI
// ters DNS alan adi sonekleri. Yalniz IP listesi yayimlayan botlar (DuckDuckGo, OpenAI, Perplexity)
// rangeBots'tadir (botranges.json).
//
// GUVENLIK: sonek, yalniz botun kendisinin alabilecegi adlari kapsamali. Herkesin sunucu kiralayip
// hem PTR hem ileri kaydini alabildigi bulut alanlari (or. Google Cloud: *.bc.googleusercontent.com)
// ASLA eklenmez; yoksa bulutta sunucu kiralayan biri UA'ya "Googlebot" yazip korumayi gecer.
// Google icin: *.googlebot.com (tarayicilar) ve yalniz belgelenmis on-ekli *.google.com adlari.
var goodBots = []struct {
	name     string
	uaTokens []string
	suffixes []string
	// prefixes bos degilse, soneke uyan ad ayrica bu on-eklerden biriyle baslamalidir.
	prefixes map[string][]string
}{
	{name: "googlebot", uaTokens: []string{"googlebot", "google-inspectiontool", "googleother", "adsbot-google", "storebot-google", "apis-google", "mediapartners-google", "feedfetcher-google", "google-extended"},
		suffixes: []string{".googlebot.com", ".google.com"},
		prefixes: map[string][]string{".google.com": {"rate-limited-proxy-", "google-proxy-"}}},
	{name: "bingbot", uaTokens: []string{"bingbot", "msnbot", "adidxbot", "bingpreview"}, suffixes: []string{".search.msn.com"}},
	{name: "applebot", uaTokens: []string{"applebot"}, suffixes: []string{".applebot.apple.com"}},
	{name: "yandexbot", uaTokens: []string{"yandex"}, suffixes: []string{".yandex.ru", ".yandex.net", ".yandex.com"}},
	{name: "baiduspider", uaTokens: []string{"baiduspider"}, suffixes: []string{".baidu.com", ".baidu.jp"}},
	{name: "naverbot", uaTokens: []string{"yeti/", "naverbot"}, suffixes: []string{".naver.com"}},
	{name: "seznambot", uaTokens: []string{"seznambot"}, suffixes: []string{".seznam.cz"}},
	{name: "yahoo-slurp", uaTokens: []string{"yahoo! slurp"}, suffixes: []string{".crawl.yahoo.net"}},
	{name: "petalbot", uaTokens: []string{"petalbot"}, suffixes: []string{".petalsearch.com"}},
}

// rangeBots, yalniz IP araligi yayimlayan arama ve yapay zeka arama botlari. Araliklar
// botranges.json'dan (scripts/update-bot-ranges.py uretir) okunur. Egitim tarayicilari (or. GPTBot)
// bilerek yoktur: bu izin "aranabilir/bulunabilir olmak" icindir.
var rangeBots = []struct {
	name     string
	uaTokens []string
}{
	{"duckduckbot", []string{"duckduckbot", "duckassistbot"}},
	{"oai-searchbot", []string{"oai-searchbot"}},
	{"chatgpt-user", []string{"chatgpt-user"}},
	{"perplexitybot", []string{"perplexitybot"}},
	{"perplexity-user", []string{"perplexity-user"}},
}

//go:embed botranges.json
var botRangesJSON []byte

var (
	botRangesOnce sync.Once
	botRanges     map[string][]netip.Prefix
	// BotRangesSnapshot, gomulu aralik listesinin tarihi (YYYY-AA-GG).
	BotRangesSnapshot string
)

func loadBotRanges() {
	botRangesOnce.Do(func() {
		var doc struct {
			Snapshot string              `json:"snapshot"`
			Bots     map[string][]string `json:"bots"`
		}
		botRanges = map[string][]netip.Prefix{}
		if err := json.Unmarshal(botRangesJSON, &doc); err != nil {
			return
		}
		BotRangesSnapshot = doc.Snapshot
		for name, cidrs := range doc.Bots {
			for _, c := range cidrs {
				p, err := netip.ParsePrefix(c)
				if err != nil {
					continue
				}
				// Asiri genis aralik korumayi delmesin (uretici betigi de eler; burada ikinci kapi).
				if (p.Addr().Is4() && p.Bits() < 16) || (p.Addr().Is6() && p.Bits() < 32) {
					continue
				}
				botRanges[name] = append(botRanges[name], p.Masked())
			}
		}
	})
}

// inBotRange, ip'nin adi verilen botun yayimlanmis araliklarinda olup olmadigini soyler.
func inBotRange(name, ip string) bool {
	loadBotRanges()
	a, err := netip.ParseAddr(strings.Trim(ip, "[]"))
	if err != nil {
		return false
	}
	a = a.Unmap()
	for _, p := range botRanges[name] {
		if p.Contains(a) {
			return true
		}
	}
	return false
}

// claimedRangeBot, UA yalniz-IP-listesi botlarindan birini iddia ediyor mu.
func claimedRangeBot(ua string) (string, bool) {
	l := strings.ToLower(ua)
	for _, b := range rangeBots {
		for _, t := range b.uaTokens {
			if strings.Contains(l, t) {
				return b.name, true
			}
		}
	}
	return "", false
}

// hostMatches, ters DNS adinin botun izinli soneklerinden (ve varsa on-eklerinden) birine uyup uymadigi.
func hostMatches(host string, suffixes []string, prefixes map[string][]string) bool {
	for _, s := range suffixes {
		if !strings.HasSuffix(host, s) {
			continue
		}
		pre := prefixes[s]
		if len(pre) == 0 {
			return true
		}
		for _, p := range pre {
			if strings.HasPrefix(host, p) {
				return true
			}
		}
	}
	return false
}

// ClaimedBot, UA bilinen bir arama botu iddiasinda mi; bot adini doner.
func ClaimedBot(ua string) (string, bool) {
	if n, ok := claimedRangeBot(ua); ok {
		return n, true
	}
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
	// Yalniz IP araligi yayimlayan botlar: DNS gerekmez, yayimlanmis aralikta olmasi yeter.
	if rn, ok := claimedRangeBot(ua); ok {
		return inBotRange(rn, ip), true
	}
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
	var prefixes map[string][]string
	for _, b := range goodBots {
		if b.name == name {
			suffixes, prefixes = b.suffixes, b.prefixes
		}
	}
	names, err := v.Resolver.LookupAddr(ctx, ip)
	if err != nil {
		return false
	}
	for _, n := range names {
		host := strings.ToLower(strings.TrimSuffix(n, "."))
		if !hostMatches(host, suffixes, prefixes) {
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
