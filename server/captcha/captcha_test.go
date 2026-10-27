package captcha

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/tkodcumpeg4/zorven/server/botguard"
	"github.com/tkodcumpeg4/zorven/server/store"
)

// fakeStore, CaptchaStore'un yalniz test icin gereken kismi.
type fakeStore struct {
	store.CaptchaStore
	mu     sync.Mutex
	sites  []store.CaptchaSite
	deltas []store.CaptchaCounterDelta
}

func (f *fakeStore) GetCaptchaSiteByKey(_ context.Context, k string) (store.CaptchaSite, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, s := range f.sites {
		if s.SiteKey == k {
			return s, nil
		}
	}
	return store.CaptchaSite{}, store.ErrNotFound
}
func (f *fakeStore) GetCaptchaSiteBySecretHash(_ context.Context, h string) (store.CaptchaSite, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, s := range f.sites {
		if s.SecretHash == h {
			return s, nil
		}
	}
	return store.CaptchaSite{}, store.ErrNotFound
}
func (f *fakeStore) AddCaptchaCounters(_ context.Context, _ time.Time, d []store.CaptchaCounterDelta) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.deltas = append(f.deltas, d...)
	return nil
}

const goodUA = "Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/537.36 Chrome/126.0 Safari/537.36"

func goodSignals() *Signals { return &Signals{MV: 12, KD: 1, T: 300} }

type env struct {
	svc    *Service
	st     *fakeStore
	site   store.CaptchaSite
	secret string
	now    time.Time
}

func newEnv(t *testing.T, domains []string, bits int, pass bool) *env {
	t.Helper()
	e := &env{st: &fakeStore{}, now: time.Unix(1_800_000_000, 0)}
	e.secret = NewSecret()
	e.site = store.CaptchaSite{ID: "csite_1", TenantID: "t1", Name: "x", SiteKey: NewSiteKey(),
		SecretHash: HashSecret(e.secret), Domains: domains, Difficulty: bits, PassEnabled: pass, PassTTLMinutes: 60}
	e.st.sites = []store.CaptchaSite{e.site}
	e.svc = New(Config{Key: []byte("0123456789abcdef0123456789abcdef"), Store: e.st, Now: func() time.Time { return e.now }})
	return e
}

func (e *env) solve(t *testing.T, host string) SolveResult {
	t.Helper()
	ch, err := e.svc.Challenge(context.Background(), ChallengeRequest{SiteKey: e.site.SiteKey, OriginHost: host, UserAgent: goodUA, AcceptLang: true})
	if err != nil {
		t.Fatal(err)
	}
	e.now = e.now.Add(800 * time.Millisecond)
	res, err := e.svc.Solve(context.Background(), SolveRequest{Token: ch.Token, Counter: botguard.SolvePoW(ch.Nonce, ch.Difficulty),
		Signals: goodSignals(), OriginHost: host, UserAgent: goodUA, AcceptLang: true})
	if err != nil {
		t.Fatalf("Solve: %v", err)
	}
	return res
}

func TestRoundTripAndSingleUse(t *testing.T) {
	e := newEnv(t, nil, 14, false)
	res := e.solve(t, "example.com")
	v := e.svc.Verify(context.Background(), e.secret, res.Response, "1.2.3.4")
	if !v.Success || v.Hostname != "example.com" || len(v.ErrorCodes) != 0 {
		t.Fatalf("beklenmeyen: %+v", v)
	}
	again := e.svc.Verify(context.Background(), e.secret, res.Response, "1.2.3.4")
	if again.Success || again.ErrorCodes[0] != CodeTimeoutDup {
		t.Fatalf("replay reddedilmeliydi: %+v", again)
	}
}

func TestSiteverifyErrorCodes(t *testing.T) {
	e := newEnv(t, nil, 14, false)
	ctx := context.Background()
	res := e.solve(t, "example.com")
	cases := []struct {
		name, secret, resp, want string
	}{
		{"secret yok", "", res.Response, CodeMissingSecret},
		{"response yok", e.secret, "", CodeMissingResponse},
		{"yanlis secret", "zcsec_yanlis", res.Response, CodeInvalidSecret},
		{"bozuk response", e.secret, "abc.def", CodeInvalidResponse},
	}
	for _, c := range cases {
		v := e.svc.Verify(ctx, c.secret, c.resp, "")
		if v.Success || len(v.ErrorCodes) == 0 || v.ErrorCodes[0] != c.want {
			t.Errorf("%s: %+v, beklenen %s", c.name, v, c.want)
		}
	}
	// ikisi de yok -> iki kod
	if v := e.svc.Verify(ctx, "", "", ""); len(v.ErrorCodes) != 2 {
		t.Errorf("iki kod beklenir: %+v", v)
	}
	// yanlis secret yanit jetonunu TUKETMEMELI
	if v := e.svc.Verify(ctx, e.secret, res.Response, ""); !v.Success {
		t.Errorf("gecerli yanit hala dogrulanabilmeli: %+v", v)
	}
}

func TestResponseTamperExpiryWrongSite(t *testing.T) {
	e := newEnv(t, nil, 14, false)
	ctx := context.Background()
	res := e.solve(t, "example.com")

	// kurcalama: imzali govdeyi degistir
	tam := res.Response[:10] + "A" + res.Response[11:]
	if res.Response[10] == 'A' {
		tam = res.Response[:10] + "B" + res.Response[11:]
	}
	if v := e.svc.Verify(ctx, e.secret, tam, ""); v.Success || v.ErrorCodes[0] != CodeInvalidResponse {
		t.Errorf("kurcalanmis yanit: %+v", v)
	}

	// baska sitenin secret'i
	other := NewSecret()
	e.st.sites = append(e.st.sites, store.CaptchaSite{ID: "csite_2", SiteKey: NewSiteKey(), SecretHash: HashSecret(other), Difficulty: 14})
	if v := e.svc.Verify(ctx, other, res.Response, ""); v.Success || v.ErrorCodes[0] != CodeInvalidResponse {
		t.Errorf("baska site secret'i ile dogrulanmamali: %+v", v)
	}

	// sure doldu
	e.now = e.now.Add(ResponseLifetime + time.Second)
	if v := e.svc.Verify(ctx, e.secret, res.Response, ""); v.Success || v.ErrorCodes[0] != CodeTimeoutDup {
		t.Errorf("suresi dolan yanit: %+v", v)
	}
}

func TestChallengeTokenChecks(t *testing.T) {
	e := newEnv(t, []string{"example.com"}, 14, false)
	ctx := context.Background()
	ch, err := e.svc.Challenge(ctx, ChallengeRequest{SiteKey: e.site.SiteKey, OriginHost: "app.example.com", UserAgent: goodUA, AcceptLang: true})
	if err != nil {
		t.Fatal(err)
	}
	counter := botguard.SolvePoW(ch.Nonce, ch.Difficulty)
	e.now = e.now.Add(time.Second)
	req := SolveRequest{Token: ch.Token, Counter: counter, Signals: goodSignals(), OriginHost: "app.example.com", UserAgent: goodUA, AcceptLang: true}

	// farkli hostname (sorgu baska yerden alindi)
	bad := req
	bad.OriginHost = "evil.com"
	if _, err := e.svc.Solve(ctx, bad); err != ErrOriginMismatch {
		t.Errorf("origin uyusmazligi: %v", err)
	}
	// bozuk sayac
	bad = req
	bad.Counter = "1"
	if _, err := e.svc.Solve(ctx, bad); err != ErrPoW {
		t.Errorf("gecersiz PoW: %v", err)
	}
	// kurcalanmis jeton
	bad = req
	bad.Token = "x" + ch.Token
	if _, err := e.svc.Solve(ctx, bad); err != ErrToken {
		t.Errorf("kurcalanmis jeton: %v", err)
	}
	// yanit jetonu sorgu jetonu yerine kabul edilmemeli (ayri anahtarlar)
	full := e.solve(t, "app.example.com")
	bad = req
	bad.Token = full.Response
	if _, err := e.svc.Solve(ctx, bad); err != ErrToken {
		t.Errorf("yanit jetonu sorgu olarak kabul edildi: %v", err)
	}
	// gecerli, sonra replay
	if _, err := e.svc.Solve(ctx, req); err != nil {
		t.Fatalf("gecerli cozum: %v", err)
	}
	if _, err := e.svc.Solve(ctx, req); err != ErrReplay {
		t.Errorf("ayni sorgu ikinci kez: %v", err)
	}
	// suresi dolmus
	ch2, _ := e.svc.Challenge(ctx, ChallengeRequest{SiteKey: e.site.SiteKey, OriginHost: "example.com", UserAgent: goodUA, AcceptLang: true})
	e.now = e.now.Add(ChallengeLifetime + time.Second)
	if _, err := e.svc.Solve(ctx, SolveRequest{Token: ch2.Token, Counter: botguard.SolvePoW(ch2.Nonce, ch2.Difficulty),
		Signals: goodSignals(), OriginHost: "example.com", UserAgent: goodUA, AcceptLang: true}); err != ErrExpired {
		t.Errorf("suresi dolan sorgu: %v", err)
	}
}

func TestDomainAllowlist(t *testing.T) {
	e := newEnv(t, []string{"example.com", "*.shop.test"}, 14, false)
	ctx := context.Background()
	ok := func(host string) bool {
		_, err := e.svc.Challenge(ctx, ChallengeRequest{SiteKey: e.site.SiteKey, OriginHost: host, UserAgent: goodUA, AcceptLang: true})
		return err == nil
	}
	for _, h := range []string{"example.com", "www.example.com", "a.b.example.com", "x.shop.test"} {
		if !ok(h) {
			t.Errorf("%s izinli olmaliydi", h)
		}
	}
	for _, h := range []string{"", "evil.com", "notexample.com", "example.com.evil.com", "shop.test"} {
		if ok(h) {
			t.Errorf("%q reddedilmeliydi", h)
		}
	}
	if _, err := e.svc.Challenge(ctx, ChallengeRequest{SiteKey: "zcs_yok", OriginHost: "example.com"}); err != ErrUnknownSite {
		t.Errorf("bilinmeyen site: %v", err)
	}
	// bos liste = her alan adi (Origin'siz de)
	e2 := newEnv(t, nil, 14, false)
	if _, err := e2.svc.Challenge(ctx, ChallengeRequest{SiteKey: e2.site.SiteKey, OriginHost: "", UserAgent: goodUA, AcceptLang: true}); err != nil {
		t.Errorf("bos liste her seye izin vermeli: %v", err)
	}
}

func TestNormalizeDomains(t *testing.T) {
	got, err := NormalizeDomains([]string{"HTTPS://Ornek.COM:8443/yol", "ornek.com", " *.Alt.test ", "localhost", ""})
	if err != nil || strings.Join(got, ",") != "ornek.com,*.alt.test,localhost" {
		t.Fatalf("%v %v", got, err)
	}
	for _, bad := range []string{"a b", "-x.com", "exa_mple.com", "http://"} {
		if _, err := NormalizeDomains([]string{bad}); err == nil {
			t.Errorf("%q reddedilmeliydi", bad)
		}
	}
}

func TestPlatformSite(t *testing.T) {
	svc := New(Config{Key: []byte("k"), PlatformSiteKey: "zcs_platform", PlatformSecret: "zcsec_platform"})
	if !svc.PlatformEnabled() || svc.PlatformSiteKey() != "zcs_platform" {
		t.Fatal("platform sitesi etkin olmaliydi")
	}
	ctx := context.Background()
	ch, err := svc.Challenge(ctx, ChallengeRequest{SiteKey: "zcs_platform", OriginHost: "zorven.app", UserAgent: goodUA, AcceptLang: true})
	if err != nil {
		t.Fatal(err)
	}
	time.Sleep(40 * time.Millisecond) // too_fast kuralini asmak icin
	res, err := svc.Solve(ctx, SolveRequest{Token: ch.Token, Counter: botguard.SolvePoW(ch.Nonce, ch.Difficulty),
		Signals: goodSignals(), OriginHost: "zorven.app", UserAgent: goodUA, AcceptLang: true})
	if err != nil {
		t.Fatal(err)
	}
	if res.Pass != "" {
		t.Error("platform sitesi pass_cookie kapali olmali")
	}
	if v := svc.Verify(ctx, "zcsec_platform", res.Response, ""); !v.Success {
		t.Fatalf("platform dogrulamasi: %+v", v)
	}
	// env yoksa devre disi
	if New(Config{Key: []byte("k"), PlatformSiteKey: "zcs_x"}).PlatformEnabled() {
		t.Error("secret olmadan etkin olmamali")
	}
}

func TestSecretGenerationAndHash(t *testing.T) {
	s := NewSecret()
	if !strings.HasPrefix(s, "zcsec_") || len(s) != 6+64 || !strings.HasPrefix(NewSiteKey(), "zcs_") {
		t.Fatalf("bicim: %s", s)
	}
	if HashSecret(s) == s || len(HashSecret(s)) != 64 || HashSecret(s) != HashSecret(s) {
		t.Fatal("ozet hatali")
	}
}

func TestScoring(t *testing.T) {
	base := ScoreInput{Signals: goodSignals(), UserAgent: goodUA, AcceptLang: true, Elapsed: 800 * time.Millisecond, Solve: true}
	if sc, why := Score(base); sc != 0 {
		t.Errorf("normal tarayici 0 puan almali: %d %v", sc, why)
	}
	// etkilesimsiz gercek kullanici (sayfa yeni acildi) reddedilmemeli
	quiet := base
	quiet.Signals = &Signals{T: 300}
	if sc, _ := Score(quiet); sc >= RejectScore || sc >= RaiseScore {
		t.Errorf("sessiz kullanici tek basina elenmemeli: %d", sc)
	}
	cases := []struct {
		name string
		mod  func(*ScoreInput)
		want bool // reddedilmeli mi
	}{
		{"webdriver", func(i *ScoreInput) { s := *i.Signals; s.WD = true; i.Signals = &s }, true},
		{"headless UA", func(i *ScoreInput) { i.UserAgent = "Mozilla/5.0 HeadlessChrome/120" }, true},
		{"curl", func(i *ScoreInput) { i.UserAgent = "curl/8.0" }, true},
		{"bos UA + sinyalsiz", func(i *ScoreInput) { i.UserAgent = ""; i.Signals = nil }, true},
		{"sinyalsiz tek basina", func(i *ScoreInput) { i.Signals = nil }, false},
		{"3 headless ipucu", func(i *ScoreInput) { s := *i.Signals; s.HL = 3; i.Signals = &s }, true},
		{"1 headless ipucu", func(i *ScoreInput) { s := *i.Signals; s.HL = 1; i.Signals = &s }, false},
		{"eksik API + etkilesimsiz + hizli", func(i *ScoreInput) {
			i.Signals = &Signals{MS: 2, HL: 1, T: 1}
			i.Elapsed = 5 * time.Millisecond
		}, true},
		{"cok hizli tek basina", func(i *ScoreInput) { i.Elapsed = 5 * time.Millisecond }, false},
		{"sure celiskisi tek basina", func(i *ScoreInput) { s := *i.Signals; s.T = 60000; i.Signals = &s }, false},
	}
	for _, c := range cases {
		in := base
		c.mod(&in)
		sc, why := Score(in)
		if (sc >= RejectScore) != c.want {
			t.Errorf("%s: puan %d (%v), reddedilme beklentisi %v", c.name, sc, why, c.want)
		}
	}
}

func TestSolveRejectsAutomatedAndRaisesDifficulty(t *testing.T) {
	e := newEnv(t, nil, 14, false)
	ctx := context.Background()
	// zorluk artisi: webdriver + bos UA sorgu asamasinda +2 bit
	ch, _ := e.svc.Challenge(ctx, ChallengeRequest{SiteKey: e.site.SiteKey, UserAgent: goodUA, AcceptLang: true, Signals: &Signals{WD: true}})
	if ch.Difficulty != 14+RaiseBits {
		t.Errorf("zorluk artmaliydi: %d", ch.Difficulty)
	}
	clean, _ := e.svc.Challenge(ctx, ChallengeRequest{SiteKey: e.site.SiteKey, UserAgent: goodUA, AcceptLang: true, Signals: goodSignals()})
	if clean.Difficulty != 14 {
		t.Errorf("temiz istemcide zorluk sabit: %d", clean.Difficulty)
	}
	// webdriver cozumu reddedilir
	e.now = e.now.Add(time.Second)
	_, err := e.svc.Solve(ctx, SolveRequest{Token: ch.Token, Counter: botguard.SolvePoW(ch.Nonce, ch.Difficulty),
		Signals: &Signals{WD: true, MV: 5}, UserAgent: goodUA, AcceptLang: true})
	if err != ErrAutomated {
		t.Errorf("webdriver reddedilmeli: %v", err)
	}
}

func TestPoWVectorFromWidget(t *testing.T) {
	// widget/api.js cozucusunun urettigi sayac (node ile dogrulandi): sunucu kabul etmeli.
	if !botguard.CheckPoW("AbCdEfGhIjKlMnOpQrStUv", "108425", 17) {
		t.Fatal("widget cozumu sunucuda gecmedi")
	}
	if botguard.CheckPoW("AbCdEfGhIjKlMnOpQrStUv", "108426", 17) {
		t.Fatal("yanlis sayac gecmemeli")
	}
}

func TestCountersFlush(t *testing.T) {
	e := newEnv(t, nil, 14, false)
	res := e.solve(t, "example.com")
	e.svc.Verify(context.Background(), e.secret, res.Response, "")
	e.svc.Verify(context.Background(), e.secret, res.Response, "") // tekrar -> fail
	if p := e.svc.PendingCounters()["csite_1"]; p.Challenge != 1 || p.Solve != 1 || p.VerifyOK != 1 || p.VerifyFail != 1 {
		t.Fatalf("bekleyen sayaclar: %+v", p)
	}
	e.svc.Flush(context.Background())
	if len(e.st.deltas) != 1 || e.st.deltas[0].VerifyOK != 1 {
		t.Fatalf("yazilan: %+v", e.st.deltas)
	}
	if len(e.svc.PendingCounters()) != 0 {
		t.Error("flush sonrasi bekleyen kalmamali")
	}
}

// ---- gecis cerezi (pass_cookie) ----

func TestPassIssueRenew(t *testing.T) {
	e := newEnv(t, []string{"example.com"}, 14, true)
	ctx := context.Background()
	res := e.solve(t, "example.com")
	if res.Pass == "" || res.PassTTLSeconds != 3600 {
		t.Fatalf("pass beklenir: %+v", res)
	}
	// pass yalniz pass_cookie aciksa
	if r2 := newEnv(t, nil, 14, false).solve(t, "a.test"); r2.Pass != "" {
		t.Error("pass_cookie kapaliyken pass verilmemeli")
	}
	// renew: PoW'suz yeni yanit, siteverify ile dogrulanir, tek kullanimlik
	rn, err := e.svc.Renew(ctx, RenewRequest{SiteKey: e.site.SiteKey, Pass: res.Pass, OriginHost: "www.example.com"})
	if err != nil {
		t.Fatalf("Renew: %v", err)
	}
	if rn.Response == res.Response {
		t.Error("renew yeni yanit uretmeli")
	}
	if v := e.svc.Verify(ctx, e.secret, rn.Response, ""); !v.Success || v.Hostname != "www.example.com" {
		t.Fatalf("renew yaniti dogrulanmali: %+v", v)
	}
	if v := e.svc.Verify(ctx, e.secret, rn.Response, ""); v.Success {
		t.Error("renew yaniti tek kullanimlik olmali")
	}
	// ayni pass ile tekrar yenileme serbest (IP bagi yok)
	if _, err := e.svc.Renew(ctx, RenewRequest{SiteKey: e.site.SiteKey, Pass: res.Pass, OriginHost: "example.com"}); err != nil {
		t.Errorf("ikinci yenileme: %v", err)
	}
	// alan listesi disi
	if _, err := e.svc.Renew(ctx, RenewRequest{SiteKey: e.site.SiteKey, Pass: res.Pass, OriginHost: "evil.com"}); err != ErrOriginNotAllow {
		t.Errorf("alan adi denetimi: %v", err)
	}
}

func TestPassTamperExpiredWrongSite(t *testing.T) {
	e := newEnv(t, nil, 14, true)
	ctx := context.Background()
	res := e.solve(t, "example.com")
	tam := res.Pass[:12] + "Z" + res.Pass[13:]
	if res.Pass[12] == 'Z' {
		tam = res.Pass[:12] + "Y" + res.Pass[13:]
	}
	if _, err := e.svc.Renew(ctx, RenewRequest{SiteKey: e.site.SiteKey, Pass: tam, OriginHost: "example.com"}); err != ErrToken {
		t.Errorf("kurcalanmis pass: %v", err)
	}
	// baska sitenin pass'i
	otherSecret := NewSecret()
	e.st.sites = append(e.st.sites, store.CaptchaSite{ID: "csite_2", SiteKey: NewSiteKey(), SecretHash: HashSecret(otherSecret),
		Difficulty: 14, PassEnabled: true, PassTTLMinutes: 60})
	if _, err := e.svc.Renew(ctx, RenewRequest{SiteKey: e.st.sites[1].SiteKey, Pass: res.Pass, OriginHost: "example.com"}); err != ErrToken {
		t.Errorf("baska site pass'i: %v", err)
	}
	// yanit/sorgu jetonu pass olarak kabul edilmemeli
	if _, err := e.svc.Renew(ctx, RenewRequest{SiteKey: e.site.SiteKey, Pass: res.Response, OriginHost: "example.com"}); err != ErrToken {
		t.Errorf("yanit jetonu pass olarak kabul edildi: %v", err)
	}
	// pass_cookie sonradan kapatilirsa eski cerezler gecersiz
	e.svc.Invalidate(e.site.SiteKey)
	e.st.sites[0].PassEnabled = false
	if _, err := e.svc.Renew(ctx, RenewRequest{SiteKey: e.site.SiteKey, Pass: res.Pass, OriginHost: "example.com"}); err != ErrToken {
		t.Errorf("kapali ayarda pass: %v", err)
	}
	e.st.sites[0].PassEnabled = true
	e.svc.Invalidate(e.site.SiteKey)
	// sure doldu (TTL 60 dk)
	e.now = e.now.Add(61 * time.Minute)
	if _, err := e.svc.Renew(ctx, RenewRequest{SiteKey: e.site.SiteKey, Pass: res.Pass, OriginHost: "example.com"}); err != ErrExpired {
		t.Errorf("suresi dolan pass: %v", err)
	}
}

func TestPassMetrics(t *testing.T) {
	e := newEnv(t, nil, 14, true)
	ctx := context.Background()
	r1 := e.solve(t, "example.com") // pass #1 (benzersiz 1)
	r2 := e.solve(t, "example.com") // pass #2 (benzersiz 2)
	for i := 0; i < 3; i++ {        // ayni pass #1 3 kez yenilenir: yeni benzersiz yok
		if _, err := e.svc.Renew(ctx, RenewRequest{SiteKey: e.site.SiteKey, Pass: r1.Pass, OriginHost: "example.com"}); err != nil {
			t.Fatal(err)
		}
	}
	_ = r2
	p := e.svc.PendingCounters()["csite_1"]
	if p.UniquePasses != 2 || p.Renewals != 3 {
		t.Fatalf("unique_passes=%d renewals=%d (beklenen 2, 3)", p.UniquePasses, p.Renewals)
	}
	// ertesi gun ayni pass yeniden gorulunce yeni gunun benzersiz sayisina eklenir
	e.svc.Flush(ctx)
	e.now = e.now.Add(25 * time.Hour)
	if _, err := e.svc.Renew(ctx, RenewRequest{SiteKey: e.site.SiteKey, Pass: r1.Pass, OriginHost: "example.com"}); err != nil && err != ErrExpired {
		t.Fatal(err)
	}
	if ClampPassTTL(0) != 1440 || ClampPassTTL(1) != 5 || ClampPassTTL(999999) != 43200 {
		t.Error("ClampPassTTL")
	}
}
