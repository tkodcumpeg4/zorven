// Package captcha, Zorven CAPTCHA'nin (Turnstile benzeri widget + dogrulama
// servisi) saf mantigidir: durumsuz HMAC jetonlari, PoW, sinyal puanlama,
// siteverify ve gunluk sayaclar. HTTP katmani server/api'dedir.
//
// AKIS: widget POST challenge -> imzali sorgu jetonu + nonce + zorluk; PoW
// (SHA-256(nonce || ondalik sayac) basinda >= zorluk sifir bit; botguard ile ayni sema)
// cozulur; POST solve -> imzali, TEK KULLANIMLIK, 300 sn gecerli, site + hostname
// bagli yanit jetonu; sitenin sunucusu POST siteverify (secret + response) ile dogrular.
//
// ANAHTARLAR: main.go ana anahtari deriveKey(.., "zorven-captcha-v1") ile verir;
// burada HMAC ile ikiye bolunur (sorgu / yanit) - bir sorgu jetonu yanit olarak
// kabul edilemez.
//
// SINIR: tek-kullanim (replay) onbellegi surec belleginde tutulur; coklu dugumde
// bir yanit jetonu her dugumde bir kez dogrulanabilir.
package captcha

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"time"

	"github.com/tkodcumpeg4/zorven/server/botguard"
	"github.com/tkodcumpeg4/zorven/server/store"
)

// Sabitler.
const (
	ChallengeLifetime = 5 * time.Minute
	ResponseLifetime  = 300 * time.Second
	DefaultDifficulty = 17
	MinDifficulty     = 14
	MaxDifficulty     = 22

	// Gecis cerezi (pass_cookie) TTL siniri/varsayilani (dakika).
	PassMinTTL     = 5
	PassMaxTTL     = 43200
	PassDefaultTTL = 1440
	maxPassSeen    = 500_000

	// PlatformSiteID, yerlesik (env) sitenin sayac kimligi.
	PlatformSiteID = "platform"

	siteCacheTTL    = 60 * time.Second
	siteCacheNegTTL = 20 * time.Second
	siteCacheMax    = 10000
)

// Hatalar (HTTP katmani kodlara cevirir).
var (
	ErrUnknownSite    = errors.New("bilinmeyen site anahtari")
	ErrOriginNotAllow = errors.New("alan adi bu sitede izinli degil")
	ErrToken          = errors.New("gecersiz jeton")
	ErrExpired        = errors.New("jeton suresi doldu")
	ErrReplay         = errors.New("jeton zaten kullanildi")
	ErrPoW            = errors.New("gecersiz cozum")
	ErrOriginMismatch = errors.New("cozum farkli bir alan adindan geldi")
	ErrAutomated      = errors.New("otomasyon belirtileri")
	ErrInternal       = errors.New("ic hata")
)

// Siteverify hata kodlari (Turnstile uyumlu).
const (
	CodeMissingSecret   = "missing-input-secret"
	CodeInvalidSecret   = "invalid-input-secret"
	CodeMissingResponse = "missing-input-response"
	CodeInvalidResponse = "invalid-input-response"
	CodeTimeoutDup      = "timeout-or-duplicate"
	CodeInternal        = "internal-error"
)

// Config, Service ayarlari.
type Config struct {
	// Key, deriveKey(master, "zorven-captcha-v1") ile turetilmis anahtar. Bos ise
	// rastgele (yalniz bu surec; dugumler arasi gecmez).
	Key   []byte
	Store store.CaptchaStore // nil olabilir (yalniz platform sitesi calisir)
	// Platform sitesi (ZORVEN_CAPTCHA_PLATFORM_SITEKEY/SECRET); ikisi de doluysa etkin.
	PlatformSiteKey string
	PlatformSecret  string
	PlatformDomains []string // bos = her alan adi
	Now             func() time.Time
}

// Service, CAPTCHA mantigi.
type Service struct {
	ck, rk, pk []byte // sorgu / yanit / gecis cerezi imza anahtarlari
	st         store.CaptchaStore
	now        func() time.Time

	platform     *store.CaptchaSite
	platformHash string

	chReplay   *botguard.Replay
	respReplay *botguard.Replay
	failLim    *botguard.Limiter // gecersiz secret denemeleri (IP basina)

	mu    sync.Mutex
	cache map[string]cacheEntry

	pmu      sync.Mutex
	passDay  string              // UTC gun (YYYY-MM-DD) - rollover'da kume sifirlanir
	passSeen map[string]struct{} // site|pass_id ozeti (kisisel veri degil; gun sonunda atilir)

	cmu      sync.Mutex
	counters map[string]*store.CaptchaCounterDelta
}

type cacheEntry struct {
	site    store.CaptchaSite
	found   bool
	expires time.Time
}

// New, Service kurar.
func New(cfg Config) *Service {
	master := cfg.Key
	if len(master) == 0 {
		master = make([]byte, 32)
		_, _ = rand.Read(master)
	}
	d := func(label string) []byte {
		m := hmac.New(sha256.New, master)
		m.Write([]byte(label))
		return m.Sum(nil)
	}
	s := &Service{
		ck: d("zorven-captcha-challenge-v1"), rk: d("zorven-captcha-response-v1"), pk: d("zorven-captcha-pass-v1"),
		st: cfg.Store, now: cfg.Now,
		chReplay: botguard.NewReplay(200_000), respReplay: botguard.NewReplay(200_000),
		failLim:  botguard.NewLimiter(20_000),
		cache:    map[string]cacheEntry{},
		counters: map[string]*store.CaptchaCounterDelta{},
	}
	if s.now == nil {
		s.now = time.Now
	}
	if cfg.PlatformSiteKey != "" && cfg.PlatformSecret != "" {
		doms, _ := NormalizeDomains(cfg.PlatformDomains)
		s.platform = &store.CaptchaSite{ID: PlatformSiteID, TenantID: "", Name: "Zorven",
			SiteKey: cfg.PlatformSiteKey, Domains: doms, Difficulty: DefaultDifficulty}
		s.platformHash = HashSecret(cfg.PlatformSecret)
	}
	return s
}

// PlatformEnabled / PlatformSiteKey: yerlesik site env ile tanimli mi.
func (s *Service) PlatformEnabled() bool { return s != nil && s.platform != nil }
func (s *Service) PlatformSiteKey() string {
	if s == nil || s.platform == nil {
		return ""
	}
	return s.platform.SiteKey
}

// ---- anahtar/secret uretimi ----

func randHex(n int) string {
	b := make([]byte, n)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

// NewSiteKey, "zcs_" + 24 onaltilik karakter.
func NewSiteKey() string { return "zcs_" + randHex(12) }

// NewSecret, "zcsec_" + 64 onaltilik karakter (256 bit).
func NewSecret() string { return "zcsec_" + randHex(32) }

// HashSecret, secret'in SHA-256 onaltilik ozeti (yuksek entropi: tuzsuz yeterli).
func HashSecret(secret string) string {
	h := sha256.Sum256([]byte(secret))
	return hex.EncodeToString(h[:])
}

// ClampDifficulty, zorlugu 14-22 araligina getirir (0 -> varsayilan).
func ClampDifficulty(d int) int {
	if d == 0 {
		return DefaultDifficulty
	}
	if d < MinDifficulty {
		return MinDifficulty
	}
	if d > MaxDifficulty {
		return MaxDifficulty
	}
	return d
}

// ---- imzali jeton ----

func b64(b []byte) string { return base64.RawURLEncoding.EncodeToString(b) }

func sign(key []byte, v any) string {
	payload, _ := json.Marshal(v)
	m := hmac.New(sha256.New, key)
	m.Write(payload)
	return b64(payload) + "." + b64(m.Sum(nil))
}

func open(key []byte, tok string, v any) bool {
	if len(tok) > 2048 {
		return false
	}
	p, mac, ok := strings.Cut(tok, ".")
	if !ok {
		return false
	}
	payload, err := base64.RawURLEncoding.DecodeString(p)
	if err != nil {
		return false
	}
	got, err := base64.RawURLEncoding.DecodeString(mac)
	if err != nil {
		return false
	}
	m := hmac.New(sha256.New, key)
	m.Write(payload)
	if subtle.ConstantTimeCompare(m.Sum(nil), got) != 1 {
		return false
	}
	return json.Unmarshal(payload, v) == nil
}

type challengePayload struct {
	K string `json:"k"` // site anahtari
	N string `json:"n"` // nonce
	B int    `json:"b"` // zorluk (bit)
	T int64  `json:"t"` // verilis (unix ms)
	E int64  `json:"e"` // bitis (unix sn)
	H string `json:"h"` // Origin hostname (sorgu istegi)
}

type passPayload struct {
	K string `json:"k"` // site anahtari
	I string `json:"i"` // pass_id (rastgele; kisisel veri yok)
	T int64  `json:"t"` // verilis (unix sn)
	E int64  `json:"e"` // bitis (unix sn)
}

// ClampPassTTL, dakikayi 5-43200 araligina getirir (0 -> varsayilan).
func ClampPassTTL(m int) int {
	if m == 0 {
		return PassDefaultTTL
	}
	if m < PassMinTTL {
		return PassMinTTL
	}
	if m > PassMaxTTL {
		return PassMaxTTL
	}
	return m
}

type responsePayload struct {
	K string `json:"k"`
	H string `json:"h"`
	T int64  `json:"t"` // verilis (unix ms)
	E int64  `json:"e"` // bitis (unix sn)
	J string `json:"j"` // benzersiz kimlik (tek kullanim)
}

// ---- site cozumu ----

func (s *Service) siteByKey(ctx context.Context, key string) (store.CaptchaSite, bool) {
	if s.platform != nil && subtle.ConstantTimeCompare([]byte(key), []byte(s.platform.SiteKey)) == 1 {
		return *s.platform, true
	}
	if s.st == nil || !strings.HasPrefix(key, "zcs_") || len(key) > 64 {
		return store.CaptchaSite{}, false
	}
	now := s.now()
	s.mu.Lock()
	if e, ok := s.cache[key]; ok && now.Before(e.expires) {
		s.mu.Unlock()
		return e.site, e.found
	}
	s.mu.Unlock()
	site, err := s.st.GetCaptchaSiteByKey(ctx, key)
	found := err == nil
	if err != nil && !errors.Is(err, store.ErrNotFound) {
		return store.CaptchaSite{}, false // gecici DB hatasi onbelleklenmez
	}
	ttl := siteCacheTTL
	if !found {
		ttl = siteCacheNegTTL
	}
	s.mu.Lock()
	if len(s.cache) >= siteCacheMax {
		s.cache = map[string]cacheEntry{}
	}
	s.cache[key] = cacheEntry{site: site, found: found, expires: now.Add(ttl)}
	s.mu.Unlock()
	return site, found
}

// Invalidate, site degisince/silinince yerel onbellegi atar (diger dugumlerde
// en fazla siteCacheTTL gecikme olur).
func (s *Service) Invalidate(siteKey string) {
	if s == nil {
		return
	}
	s.mu.Lock()
	delete(s.cache, siteKey)
	s.mu.Unlock()
}

// ---- sorgu ----

// ChallengeRequest, sorgu istegi.
type ChallengeRequest struct {
	SiteKey    string
	OriginHost string // Origin/Referer'dan; yoksa ""
	UserAgent  string
	AcceptLang bool
	Signals    *Signals // istege bagli (ortam sinyalleri; zorluk artirir)
}

// ChallengeResult, sorgu cevabi.
type ChallengeResult struct {
	Token      string    `json:"token"`
	Nonce      string    `json:"nonce"`
	Difficulty int       `json:"difficulty"`
	ExpiresAt  time.Time `json:"expires_at"`
}

// Challenge, durumsuz imzali PoW sorgusu uretir.
func (s *Service) Challenge(ctx context.Context, req ChallengeRequest) (ChallengeResult, error) {
	site, ok := s.siteByKey(ctx, req.SiteKey)
	if !ok {
		return ChallengeResult{}, ErrUnknownSite
	}
	if !MatchDomain(site.Domains, req.OriginHost) {
		return ChallengeResult{}, ErrOriginNotAllow
	}
	bits := ClampDifficulty(site.Difficulty)
	if req.Signals != nil {
		if sc, _ := Score(ScoreInput{Signals: req.Signals, UserAgent: req.UserAgent, AcceptLang: req.AcceptLang}); sc >= RaiseScore {
			bits += RaiseBits
			if bits > MaxBits {
				bits = MaxBits
			}
		}
	}
	raw := make([]byte, 16)
	_, _ = rand.Read(raw)
	nonce := b64(raw)
	now := s.now()
	exp := now.Add(ChallengeLifetime)
	tok := sign(s.ck, challengePayload{K: site.SiteKey, N: nonce, B: bits, T: now.UnixMilli(), E: exp.Unix(), H: req.OriginHost})
	s.count(site.ID, func(d *store.CaptchaCounterDelta) { d.Challenge++ })
	return ChallengeResult{Token: tok, Nonce: nonce, Difficulty: bits, ExpiresAt: exp}, nil
}

// ---- cozum ----

// SolveRequest, cozum istegi.
type SolveRequest struct {
	Token      string
	Counter    string
	Signals    *Signals
	OriginHost string
	UserAgent  string
	AcceptLang bool
}

// SolveResult, cozum cevabi.
type SolveResult struct {
	Response  string    `json:"response"`
	ExpiresAt time.Time `json:"expires_at"`
	// Pass: yalniz sitede pass_cookie acikken; widget bunu birinci taraf `zcap_pass`
	// cerezine yazar. Cerez sureleri PassTTLSeconds.
	Pass           string `json:"pass,omitempty"`
	PassTTLSeconds int    `json:"pass_ttl_seconds,omitempty"`
}

// Solve, PoW'yi ve sinyalleri dogrular; yanit jetonu uretir.
func (s *Service) Solve(ctx context.Context, req SolveRequest) (SolveResult, error) {
	var cp challengePayload
	if !open(s.ck, req.Token, &cp) || cp.N == "" || cp.K == "" {
		return SolveResult{}, ErrToken
	}
	now := s.now()
	if now.Unix() >= cp.E {
		return SolveResult{}, ErrExpired
	}
	site, ok := s.siteByKey(ctx, cp.K)
	if !ok {
		return SolveResult{}, ErrUnknownSite
	}
	if req.OriginHost != cp.H {
		return SolveResult{}, ErrOriginMismatch
	}
	if !MatchDomain(site.Domains, req.OriginHost) {
		return SolveResult{}, ErrOriginNotAllow
	}
	if cp.B < MinDifficulty || cp.B > MaxBits || !botguard.CheckPoW(cp.N, req.Counter, cp.B) {
		return SolveResult{}, ErrPoW
	}
	// Sorgu tek kullanimlik: ayni PoW ile birden fazla yanit alinamaz.
	if !s.chReplay.Use(cp.N, time.Unix(cp.E, 0), now) {
		return SolveResult{}, ErrReplay
	}
	if sc, _ := Score(ScoreInput{Signals: req.Signals, UserAgent: req.UserAgent, AcceptLang: req.AcceptLang,
		Elapsed: now.Sub(time.UnixMilli(cp.T)), Solve: true}); sc >= RejectScore {
		return SolveResult{}, ErrAutomated
	}
	jb := make([]byte, 16)
	_, _ = rand.Read(jb)
	exp := now.Add(ResponseLifetime)
	tok := sign(s.rk, responsePayload{K: site.SiteKey, H: req.OriginHost, T: now.UnixMilli(), E: exp.Unix(), J: b64(jb)})
	s.count(site.ID, func(d *store.CaptchaCounterDelta) { d.Solve++ })
	res := SolveResult{Response: tok, ExpiresAt: exp}
	if site.PassEnabled {
		ttl := ClampPassTTL(site.PassTTLMinutes)
		pb := make([]byte, 16)
		_, _ = rand.Read(pb)
		pexp := now.Add(time.Duration(ttl) * time.Minute)
		res.Pass = sign(s.pk, passPayload{K: site.SiteKey, I: b64(pb), T: now.Unix(), E: pexp.Unix()})
		res.PassTTLSeconds = ttl * 60
		s.notePass(site, b64(pb), now)
	}
	return res, nil
}

// RenewRequest, gecis cerezi ile yeni yanit alma istegi.
type RenewRequest struct {
	SiteKey    string
	Pass       string
	OriginHost string
}

// Renew, gecerli bir gecis cerezi (imza + site + sure) karsiliginda PoW'suz yeni,
// tek kullanimlik yanit jetonu verir. IP baglamasi yoktur. Site pass_cookie'yi
// kapattiysa mevcut cerezler de gecersizdir (ErrToken).
func (s *Service) Renew(ctx context.Context, req RenewRequest) (SolveResult, error) {
	site, ok := s.siteByKey(ctx, req.SiteKey)
	if !ok {
		return SolveResult{}, ErrUnknownSite
	}
	if !MatchDomain(site.Domains, req.OriginHost) {
		return SolveResult{}, ErrOriginNotAllow
	}
	var pp passPayload
	if !open(s.pk, req.Pass, &pp) || pp.I == "" || subtle.ConstantTimeCompare([]byte(pp.K), []byte(site.SiteKey)) != 1 || !site.PassEnabled {
		return SolveResult{}, ErrToken
	}
	now := s.now()
	if now.Unix() >= pp.E {
		return SolveResult{}, ErrExpired
	}
	jb := make([]byte, 16)
	_, _ = rand.Read(jb)
	exp := now.Add(ResponseLifetime)
	tok := sign(s.rk, responsePayload{K: site.SiteKey, H: req.OriginHost, T: now.UnixMilli(), E: exp.Unix(), J: b64(jb)})
	s.count(site.ID, func(d *store.CaptchaCounterDelta) { d.Renewals++ })
	s.notePass(site, pp.I, now)
	return SolveResult{Response: tok, ExpiresAt: exp}, nil
}

// notePass, gunluk benzersiz pass_id sayacini gunceller. pass_id'nin SHA-256
// ozeti bellekte yalniz UTC gunu boyunca bir kumede tutulur (diske yazilmaz);
// kisisel veri yoktur. Yeniden baslatmada kume sifirlanir (hafif fazla sayim).
func (s *Service) notePass(site store.CaptchaSite, id string, now time.Time) {
	day := now.UTC().Format("2006-01-02")
	h := sha256.Sum256([]byte(site.ID + "|" + id))
	key := string(h[:12])
	s.pmu.Lock()
	if s.passDay != day || s.passSeen == nil {
		s.passDay, s.passSeen = day, map[string]struct{}{}
	}
	_, seen := s.passSeen[key]
	if !seen && len(s.passSeen) < maxPassSeen {
		s.passSeen[key] = struct{}{}
	}
	s.pmu.Unlock()
	if !seen {
		s.count(site.ID, func(d *store.CaptchaCounterDelta) { d.UniquePasses++ })
	}
}

// ---- siteverify ----

// VerifyResult, Turnstile uyumlu siteverify cevabi.
type VerifyResult struct {
	Success     bool      `json:"success"`
	ChallengeTS time.Time `json:"challenge_ts,omitempty"`
	Hostname    string    `json:"hostname,omitempty"`
	ErrorCodes  []string  `json:"error-codes"`
	Action      string    `json:"action,omitempty"`
}

// siteBySecret: platform secret'i ya da DB'deki site (sabit-zamanli ozet karsilastirmasi).
func (s *Service) siteBySecret(ctx context.Context, secret string) (store.CaptchaSite, error) {
	h := HashSecret(secret)
	if s.platform != nil && subtle.ConstantTimeCompare([]byte(h), []byte(s.platformHash)) == 1 {
		return *s.platform, nil
	}
	if s.st == nil {
		return store.CaptchaSite{}, store.ErrNotFound
	}
	site, err := s.st.GetCaptchaSiteBySecretHash(ctx, h)
	if err != nil {
		return store.CaptchaSite{}, err
	}
	if subtle.ConstantTimeCompare([]byte(h), []byte(site.SecretHash)) != 1 {
		return store.CaptchaSite{}, store.ErrNotFound
	}
	return site, nil
}

// Verify, yanit jetonunu secret'in sahibi site adina dogrular ve TUKETIR.
// callerIP yalniz gecersiz-secret deneme sinirlamasi icindir (bos olabilir).
func (s *Service) Verify(ctx context.Context, secret, response, callerIP string) VerifyResult {
	fail := func(codes ...string) VerifyResult { return VerifyResult{ErrorCodes: codes} }
	var codes []string
	if secret == "" {
		codes = append(codes, CodeMissingSecret)
	}
	if response == "" {
		codes = append(codes, CodeMissingResponse)
	}
	if len(codes) > 0 {
		return fail(codes...)
	}
	// Gecersiz secret denemeleri IP basina sinirli (tahmin/DB yukleme koruması).
	key := "sv:" + botguard.IPPrefix(callerIP)
	site, err := s.siteBySecret(ctx, secret)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			if callerIP != "" {
				if ok, _ := s.failLim.Allow(key, 60, time.Minute, 1); !ok {
					return fail(CodeInvalidSecret)
				}
			}
			return fail(CodeInvalidSecret)
		}
		return fail(CodeInternal)
	}
	var rp responsePayload
	if !open(s.rk, response, &rp) || rp.J == "" || rp.K == "" {
		s.count(site.ID, func(d *store.CaptchaCounterDelta) { d.VerifyFail++ })
		return fail(CodeInvalidResponse)
	}
	if subtle.ConstantTimeCompare([]byte(rp.K), []byte(site.SiteKey)) != 1 {
		s.count(site.ID, func(d *store.CaptchaCounterDelta) { d.VerifyFail++ })
		return fail(CodeInvalidResponse) // baska sitenin yaniti
	}
	now := s.now()
	if now.Unix() >= rp.E {
		s.count(site.ID, func(d *store.CaptchaCounterDelta) { d.VerifyFail++ })
		return fail(CodeTimeoutDup)
	}
	if !s.respReplay.Use(rp.J, time.Unix(rp.E, 0), now) {
		s.count(site.ID, func(d *store.CaptchaCounterDelta) { d.VerifyFail++ })
		return fail(CodeTimeoutDup)
	}
	s.count(site.ID, func(d *store.CaptchaCounterDelta) { d.VerifyOK++ })
	return VerifyResult{Success: true, ChallengeTS: time.UnixMilli(rp.T).UTC(), Hostname: rp.H, ErrorCodes: []string{}}
}

// ---- sayaclar ----

func (s *Service) count(siteID string, f func(*store.CaptchaCounterDelta)) {
	s.cmu.Lock()
	d := s.counters[siteID]
	if d == nil {
		d = &store.CaptchaCounterDelta{SiteID: siteID}
		s.counters[siteID] = d
	}
	f(d)
	s.cmu.Unlock()
}

// Flush, bellekteki sayaclari tek toplu upsert ile yazar. Hata olursa artislar
// geri eklenir (sonraki turda yeniden denenir).
func (s *Service) Flush(ctx context.Context) {
	if s == nil || s.st == nil {
		return
	}
	s.cmu.Lock()
	cur := s.counters
	s.counters = map[string]*store.CaptchaCounterDelta{}
	s.cmu.Unlock()
	if len(cur) == 0 {
		return
	}
	deltas := make([]store.CaptchaCounterDelta, 0, len(cur))
	for _, d := range cur {
		deltas = append(deltas, *d)
	}
	if err := s.st.AddCaptchaCounters(ctx, s.now(), deltas); err != nil {
		for _, d := range deltas {
			d := d
			s.count(d.SiteID, func(x *store.CaptchaCounterDelta) {
				x.Challenge += d.Challenge
				x.Solve += d.Solve
				x.VerifyOK += d.VerifyOK
				x.VerifyFail += d.VerifyFail
				x.Renewals += d.Renewals
				x.UniquePasses += d.UniquePasses
			})
		}
	}
}

// Run, sayaclari 30 sn'de bir yazar; gunde bir 40 gunden eski satirlari budar.
// ctx iptalinde son bir yazim yapar.
func (s *Service) Run(ctx context.Context) {
	if s == nil || s.st == nil {
		return
	}
	t := time.NewTicker(30 * time.Second)
	defer t.Stop()
	prune := time.NewTicker(24 * time.Hour)
	defer prune.Stop()
	for {
		select {
		case <-ctx.Done():
			fctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			s.Flush(fctx)
			cancel()
			return
		case <-t.C:
			fctx, cancel := context.WithTimeout(ctx, 10*time.Second)
			s.Flush(fctx)
			cancel()
		case <-prune.C:
			pctx, cancel := context.WithTimeout(ctx, 10*time.Second)
			_ = s.st.PruneCaptchaCounters(pctx, s.now().AddDate(0, 0, -40))
			cancel()
		}
	}
}

// PendingCounters, henuz yazilmamis artislari doner (API sayaclara ekler ve testler).
func (s *Service) PendingCounters() map[string]store.CaptchaCounterDelta {
	out := map[string]store.CaptchaCounterDelta{}
	if s == nil {
		return out
	}
	s.cmu.Lock()
	defer s.cmu.Unlock()
	for k, v := range s.counters {
		out[k] = *v
	}
	return out
}
