package botguard

import (
	"net/netip"
	"testing"
)

// Bulut sunucusu (herkesin alabildigi PTR+ileri kayit) Googlebot sayilmaz; belgelenmis adlar gecer.
func TestVerifiedBotsRejectsCloudHostnames(t *testing.T) {
	res := &fakeResolver{
		ptr: map[string][]string{
			"34.1.2.3":     {"3.2.1.34.bc.googleusercontent.com."},         // Google Cloud VM
			"35.4.5.6":     {"x.gae.googleusercontent.com."},               // artik izinli degil
			"66.249.90.1":  {"rate-limited-proxy-66-249-90-1.google.com."}, // belgelenmis on-ek
			"74.125.1.1":   {"mail-yb1.google.com."},                       // google.com ama bot degil
			"66.249.66.10": {"geo-crawl-66-249-66-10.geo.googlebot.com."},
		},
		fwd: map[string][]string{
			"3.2.1.34.bc.googleusercontent.com":         {"34.1.2.3"},
			"x.gae.googleusercontent.com":               {"35.4.5.6"},
			"rate-limited-proxy-66-249-90-1.google.com": {"66.249.90.1"},
			"mail-yb1.google.com":                       {"74.125.1.1"},
			"geo-crawl-66-249-66-10.geo.googlebot.com":  {"66.249.66.10"},
		},
	}
	v := NewVerifiedBots(res)
	gb := "Mozilla/5.0 (compatible; Googlebot/2.1; +http://www.google.com/bot.html)"
	for ip, want := range map[string]bool{"34.1.2.3": false, "35.4.5.6": false, "74.125.1.1": false, "66.249.90.1": true, "66.249.66.10": true} {
		if ok, claimed := v.Verify(ip, gb); ok != want || !claimed {
			t.Errorf("%s: dogrulandi=%v (beklenen %v), iddia=%v", ip, ok, want, claimed)
		}
	}
}

// Yalniz IP araligi yayimlayan botlar: aralikta ise gecer, disinda ise sahte sayilir; DNS'e gidilmez.
func TestVerifiedBotsPublishedRanges(t *testing.T) {
	loadBotRanges()
	if BotRangesSnapshot == "" {
		t.Fatal("botranges.json okunamadi")
	}
	res := &fakeResolver{}
	v := NewVerifiedBots(res)
	for _, b := range rangeBots {
		ps := botRanges[b.name]
		if len(ps) == 0 {
			t.Fatalf("%s: aralik yok", b.name)
		}
		for _, p := range ps {
			if (p.Addr().Is4() && p.Bits() < 16) || (p.Addr().Is6() && p.Bits() < 32) {
				t.Errorf("%s: asiri genis aralik %s", b.name, p)
			}
		}
		in := ps[0].Addr().String()
		ua := "Mozilla/5.0 (compatible; " + b.uaTokens[0] + "/1.0)"
		if ok, claimed := v.Verify(in, ua); !ok || !claimed {
			t.Errorf("%s: aralik ici %s dogrulanmadi", b.name, in)
		}
		if ok, claimed := v.Verify("203.0.113.50", ua); ok || !claimed {
			t.Errorf("%s: aralik disi adres dogrulandi", b.name)
		}
	}
	if res.calls != 0 {
		t.Errorf("aralik botlari icin DNS sorgusu yapilmamali: %d", res.calls)
	}
	// Bir botun araligi baska botun adiyla gecerli olmaz.
	ddg := botRanges["duckduckbot"][0].Addr().String()
	if ok, _ := v.Verify(ddg, "Mozilla/5.0 (compatible; PerplexityBot/1.0)"); ok && !inBotRange("perplexitybot", ddg) {
		t.Errorf("DuckDuckGo adresi PerplexityBot olarak dogrulandi")
	}
	// Egitim tarayicisi (GPTBot) bu izne dahil degil.
	if _, claimed := v.Verify(ddg, "Mozilla/5.0 (compatible; GPTBot/1.2)"); claimed {
		t.Errorf("GPTBot taninmamali")
	}
	if _, err := netip.ParseAddr(ddg); err != nil {
		t.Fatal(err)
	}
}

func TestVerifiedBotsMoreEngines(t *testing.T) {
	res := &fakeResolver{
		ptr: map[string][]string{
			"125.209.1.1": {"crawl.125-209-1-1.web.naver.com."},
			"77.75.1.1":   {"fulltextrobot-77-75-1-1.seznam.cz."},
			"74.6.1.1":    {"b115.crawl.yahoo.net."},
			"114.119.1.1": {"petalbot-114-119-1-1.petalsearch.com."},
		},
		fwd: map[string][]string{
			"crawl.125-209-1-1.web.naver.com":      {"125.209.1.1"},
			"fulltextrobot-77-75-1-1.seznam.cz":    {"77.75.1.1"},
			"b115.crawl.yahoo.net":                 {"74.6.1.1"},
			"petalbot-114-119-1-1.petalsearch.com": {"114.119.1.1"},
		},
	}
	v := NewVerifiedBots(res)
	for ip, ua := range map[string]string{
		"125.209.1.1": "Mozilla/5.0 (compatible; Yeti/1.1; +https://naver.me/spd)",
		"77.75.1.1":   "Mozilla/5.0 (compatible; SeznamBot/4.0)",
		"74.6.1.1":    "Mozilla/5.0 (compatible; Yahoo! Slurp; http://help.yahoo.com/help/us/ysearch/slurp)",
		"114.119.1.1": "Mozilla/5.0 (compatible; PetalBot;+https://webmaster.petalsearch.com/site/petalbot)",
	} {
		if ok, claimed := v.Verify(ip, ua); !ok || !claimed {
			t.Errorf("%s (%s): dogrulanmadi", ip, ua)
		}
	}
}
