package tunsec

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestBotRateDefaults(t *testing.T) {
	n, err := Normalize(Config{})
	if err != nil {
		t.Fatal(err)
	}
	if n.Bot.Mode != BotOff || n.Bot.Difficulty != 18 || n.Bot.TTLMinutes != 60 || !n.Bot.AllowVerifiedBots || n.Bot.BlockEmptyUA {
		t.Fatalf("bot varsayilanlari: %+v", n.Bot)
	}
	if n.Bot.ExcludePaths == nil || n.Rate.ExcludePaths == nil {
		t.Fatal("exclude_paths [] olmali (null degil)")
	}
	if n.Rate.Enabled || n.Rate.Requests != 120 || n.Rate.WindowSeconds != 60 || n.Rate.Action != RateBlock {
		t.Fatalf("rate varsayilanlari: %+v", n.Rate)
	}
	if Compile(Config{}) != nil {
		t.Fatal("hicbir sey acik degilken Compile nil olmali")
	}
	// Eski depolanmis ayar (bot/rate yok) varsayilanlarla acilir.
	old := Parse([]byte(`{"waf":{"mode":"log"},"geo":{"mode":"off"}}`))
	if old.Bot.Mode != BotOff || old.Rate.Requests != 120 {
		t.Fatalf("eski ayar: %+v", old)
	}
}

func TestBotRateValidation(t *testing.T) {
	bad := []Config{
		{Bot: BotConfig{Mode: "sometimes"}},
		{Bot: BotConfig{Mode: BotAlways, Difficulty: 11}},
		{Bot: BotConfig{Mode: BotAlways, Difficulty: 23}},
		{Bot: BotConfig{Mode: BotAlways, TTLMinutes: 4}},
		{Bot: BotConfig{Mode: BotAlways, TTLMinutes: 10081}},
		{Bot: BotConfig{Mode: BotAlways, ExcludePaths: []string{"api/*"}}},
		{Bot: BotConfig{Mode: BotAlways, ExcludePaths: []string{"/a*b"}}},
		{Bot: BotConfig{Mode: BotAlways, ExcludePaths: make([]string, 33)}},
		{Rate: RateConfig{Enabled: true, Action: "ban"}},
		{Rate: RateConfig{Enabled: true, Requests: -1}},
		{Rate: RateConfig{Enabled: true, WindowSeconds: 3601}},
		{Rate: RateConfig{Enabled: true, ExcludePaths: []string{"x"}}},
	}
	for i, c := range bad {
		if i == 7 { // 33 bos oge: bos dizgeler atlanmaz, sinir sayiyi kontrol eder
			for j := range c.Bot.ExcludePaths {
				c.Bot.ExcludePaths[j] = "/p" + strings.Repeat("x", j)
			}
		}
		if _, err := Normalize(c); err == nil {
			t.Fatalf("gecersiz ayar kabul edildi #%d: %+v", i, c)
		}
	}
	ok, err := Normalize(Config{
		Bot:  BotConfig{Mode: " ALWAYS ", Difficulty: 12, TTLMinutes: 10080, ExcludePaths: []string{"/api/*", "/api/*", " /webhook/* "}},
		Rate: RateConfig{Enabled: true, Requests: 5, WindowSeconds: 1, Action: "Challenge"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if ok.Bot.Mode != BotAlways || len(ok.Bot.ExcludePaths) != 2 || ok.Bot.ExcludePaths[1] != "/webhook/*" || ok.Rate.Action != RateChallenge {
		t.Fatalf("kanonik hal: %+v", ok)
	}
	// JSON sekli (sozlesme)
	b, _ := json.Marshal(ok)
	for _, k := range []string{`"mode"`, `"difficulty"`, `"ttl_minutes"`, `"exclude_paths"`, `"allow_verified_bots"`, `"block_empty_ua"`,
		`"enabled"`, `"requests"`, `"window_seconds"`, `"action"`} {
		if !strings.Contains(string(b), k) {
			t.Fatalf("sozlesme alani eksik %s: %s", k, b)
		}
	}
}

func TestPathMatch(t *testing.T) {
	cases := []struct {
		p, path string
		want    bool
	}{
		{"/api/*", "/api/x/y", true}, {"/api/*", "/api/", true}, {"/api/*", "/api", false},
		{"/health", "/health", true}, {"/health", "/health/", false}, {"/health", "/healthz", false},
		{"/*", "/anything", true},
	}
	for _, c := range cases {
		if PathMatch(c.p, c.path) != c.want {
			t.Errorf("PathMatch(%q,%q) != %v", c.p, c.path, c.want)
		}
	}
	cp := Compile(Config{Bot: BotConfig{Mode: BotAlways, ExcludePaths: []string{"/api/*"}}, Rate: RateConfig{Enabled: true, ExcludePaths: []string{"/r"}}})
	if !cp.BotExcluded("/api/v1") || cp.BotExcluded("/r") || !cp.RateExcluded("/r") || cp.RateExcluded("/api/v1") {
		t.Fatal("bot/rate exclude ayrimi")
	}
}

func TestCompileBotRateOnly(t *testing.T) {
	c := Compile(Config{Bot: BotConfig{Mode: BotSuspicious}})
	if c == nil || !c.BotActive() || c.RateActive() || c.WAFActive() || c.GeoActive() || !c.ChallengeAvailable() {
		t.Fatalf("yalniz bot: %+v", c)
	}
	c = Compile(Config{Rate: RateConfig{Enabled: true, Action: RateBlock}})
	if c == nil || !c.RateActive() || c.ChallengeAvailable() {
		t.Fatal("rate=block sorgu ucunu acmamali")
	}
	c = Compile(Config{Rate: RateConfig{Enabled: true, Action: RateChallenge}})
	if !c.ChallengeAvailable() || c.Bot.Difficulty != 18 {
		t.Fatal("rate=challenge sorgu ucunu varsayilan zorlukla acmali")
	}
}
