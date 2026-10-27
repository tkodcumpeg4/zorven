package ingress

// Bot korumasi (B1 PoW sorgusu, B2 hiz siniri, B3 supheli skor) uygulamasi.
// Ayar Router.Reload'da derlenmistir (tunsec.Compiled.Bot / .Rate); saf mantik
// botguard paketindedir. Siralama icin bkz. ServeHTTP:
//
//	IP izin listesi -> Geo -> /_zva /_zvb -> /.zorven/ (dogrulama ucu)
//	-> hiz siniri -> bot sorgusu -> WAF -> erisim denetimi (Basic/OAuth) -> ...

import (
	"encoding/json"
	"io"
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/tkodcumpeg4/zorven/server/botguard"
	"github.com/tkodcumpeg4/zorven/server/reqlog"
	"github.com/tkodcumpeg4/zorven/server/store"
	"github.com/tkodcumpeg4/zorven/server/tunsec"
	"github.com/tkodcumpeg4/zorven/server/visitorauth"
	"github.com/tkodcumpeg4/zorven/shared/protocol"
)

// B1-B3 reject_reason degerleri.
const (
	RejectBotChallenge  = "bot:challenge"  // sorgu sayfasi / 403 gosterildi
	RejectBotFail       = "bot:fail"       // gecersiz cozum
	RejectBotBlock      = "bot:block"      // bos UA vb.
	RejectRateBlock     = "rate:block"     // 429
	RejectRateChallenge = "rate:challenge" // hiz asildi, sorgu istendi
)

// cookieReplayMult, action=challenge iken gecerli cerez/dogrulanmis bot/oturum
// sahibinin hiz siniri carpani (cerez tekrar-oynatma seline karsi hala sinirli).
const cookieReplayMult = 3

// botState, Handler'in tembel baslatilan bot korumasi durumu.
type botState struct {
	keys   *botguard.Keys
	lim    *botguard.Limiter
	replay *botguard.Replay
	vb     *botguard.VerifiedBots
	stats  sync.Map // tunelID -> *BotCounters
}

// BotCounters, tunel basina sorgu sayaclari (bellekte; surec yeniden baslayinca sifirlanir).
type BotCounters struct {
	Issued atomic.Int64 // sorgu sayfasi gosterildi
	Passed atomic.Int64 // dogrulama basarili (cerez verildi)
	Failed atomic.Int64 // gecersiz cozum
}

func (h *Handler) bot() *botState {
	h.botOnce.Do(func() {
		res := h.BotResolver
		if res == nil {
			res = net.DefaultResolver
		}
		h.botSt = &botState{
			keys:   botguard.NewKeys(h.BotKey),
			lim:    botguard.NewLimiter(botguard.DefaultLimiterCap),
			replay: botguard.NewReplay(100_000),
			vb:     botguard.NewVerifiedBots(res),
		}
	})
	return h.botSt
}

// BotStats, tunelin sorgu sayaclarini doner (yoksa sifir).
func (h *Handler) BotStats(tunnelID string) (issued, passed, failed int64) {
	if v, ok := h.bot().stats.Load(tunnelID); ok {
		c := v.(*BotCounters)
		return c.Issued.Load(), c.Passed.Load(), c.Failed.Load()
	}
	return 0, 0, 0
}

func (s *botState) counters(tunnelID string) *BotCounters {
	if v, ok := s.stats.Load(tunnelID); ok {
		return v.(*BotCounters)
	}
	v, _ := s.stats.LoadOrStore(tunnelID, &BotCounters{})
	return v.(*BotCounters)
}

// reservedZorvenPath, tunel host'unda Zorven'e ayrilmis yol alani.
func reservedZorvenPath(p string) bool { return strings.HasPrefix(p, "/.zorven/") }

// botCookieValid, gecerli __zv_bot cerezi var mi (tunel + IP onegi + UA bagli).
func (h *Handler) botCookieValid(r *http.Request, tun store.HostRoute) bool {
	c, err := r.Cookie(botguard.CookieName)
	if err != nil {
		return false
	}
	return h.bot().keys.VerifyCookie(c.Value, tun.TunnelID, clientIP(r), r.UserAgent(), time.Now())
}

// visitorAuthed, ziyaretci bu tunel icin zaten dogrulanmis (OAuth / Basic oturumu)
// mi: bu durumda bot sorgusu atlanir. Oturum yenileme cerezi YAZILMAZ (erisim
// denetimi sonra kendisi yapar).
func (h *Handler) visitorAuthed(r *http.Request, tun store.HostRoute) bool {
	if !tun.AccessEnabled {
		return false
	}
	switch tun.AccessMode {
	case "oauth":
		if h.Visitor == nil || !h.Visitor.Enabled() {
			return false
		}
		sess, ok := h.Visitor.Validate(r, visitorauth.PolicyFromConfig(tun.AccessConfig))
		return ok && !h.sessionRevoked(tun.TunnelID, sess.Email, sess.Issued)
	case "basic":
		cfg := parseBasicConfig(tun.AccessConfig)
		st, ok := h.basicCookieState(r, tun, cfg)
		return ok && !h.sessionRevoked(tun.TunnelID, truncateIdentity(cfg.Username), st.Issued)
	}
	return false
}

// requireChallenge, sorgu gerektigini bildirir: HTML GET/HEAD icin sorgu sayfasi,
// digerleri icin 403 + X-Zorven-Challenge: required. Her iki durumda reqlog'a
// reason yazilir.
func (h *Handler) requireChallenge(w http.ResponseWriter, r *http.Request, tun store.HostRoute, sec *tunsec.Compiled, reason string, started time.Time) {
	st := h.bot()
	if botguard.WantsHTMLPage(r) && !isWebSocketUpgrade(r) {
		token, nonce := st.keys.NewChallenge(tun.TunnelID, clientIP(r), sec.Bot.Difficulty, time.Now())
		botguard.RenderPage(w, r, token, nonce, sec.Bot.Difficulty, r.URL.RequestURI())
		st.counters(tun.TunnelID).Issued.Add(1)
	} else {
		botguard.WriteRequired(w)
	}
	h.logRejectID(tun, r, http.StatusForbidden, reason, started, "")
}

// enforceRate, B2: tunel + istemci IP basina token bucket. true = istek
// sonlandirildi. Anahtar: IPv4 icin tam adres, IPv6 icin /64 (cerez ve sorgu
// baglamasi /24'tur; hiz siniri daha ince taneli tutulur ki komsular birbirini
// etkilemesin).
func (h *Handler) enforceRate(w http.ResponseWriter, r *http.Request, tun store.HostRoute, sec *tunsec.Compiled, started time.Time) bool {
	if !sec.RateActive() || internalVisitorPath(r.URL.Path) || sec.RateExcluded(r.URL.Path) {
		return false
	}
	st := h.bot()
	ip := clientIP(r)
	key := tun.TunnelID + "|" + rateKeyIP(ip)
	window := time.Duration(sec.Rate.WindowSeconds) * time.Second
	challenge := sec.Rate.Action == tunsec.RateChallenge

	// action=challenge: cerezi/oturumu olan ya da dogrulanmis bot olan istemci
	// sorguya gonderilmez ama 3x hizla sinirlanir (tekrar-oynatma seline karsi).
	pass := false
	if challenge {
		pass = h.botCookieValid(r, tun) || h.visitorAuthed(r, tun)
		if !pass && sec.Bot.AllowVerified {
			pass, _ = st.vb.Verify(ip, r.UserAgent())
		}
	}
	mult := 1
	if pass {
		mult, key = cookieReplayMult, key+"|c"
	}
	ok, wait := st.lim.Allow(key, sec.Rate.Requests, window, mult)
	if ok {
		return false
	}
	if !challenge || pass {
		secs := int(wait.Seconds())
		if secs < 1 {
			secs = 1
		}
		w.Header().Set("Retry-After", strconv.Itoa(secs))
		writeError(w, r, http.StatusTooManyRequests, protocol.CodeRateLimited,
			"İstek hız sınırı aşıldı. Lütfen sonra tekrar deneyin.")
		h.logRejectID(tun, r, http.StatusTooManyRequests, RejectRateBlock, started, "")
		return true
	}
	h.requireChallenge(w, r, tun, sec, RejectRateChallenge, started)
	return true
}

// rateKeyIP, hiz siniri anahtari: IPv4 tam adres, IPv6 /64.
func rateKeyIP(ip string) string {
	p := botguard.IPPrefix(ip)
	if strings.Contains(ip, ":") {
		return p
	}
	return strings.Trim(ip, "[]")
}

// enforceBot, B1+B3: sorgu / engelleme karari. true = istek sonlandirildi.
func (h *Handler) enforceBot(w http.ResponseWriter, r *http.Request, tun store.HostRoute, sec *tunsec.Compiled, started time.Time) bool {
	if !sec.BotActive() || internalVisitorPath(r.URL.Path) || reservedZorvenPath(r.URL.Path) || sec.BotExcluded(r.URL.Path) {
		return false
	}
	ua := r.UserAgent()
	if sec.Bot.BlockEmptyUA && strings.TrimSpace(ua) == "" {
		id := reqlog.NewID()
		w.Header().Set(requestIDHeader, id)
		writeError(w, r, http.StatusForbidden, protocol.CodeAccessDenied,
			secDeniedMessage+" Istek kimligi: "+id)
		h.logRejectID(tun, r, http.StatusForbidden, RejectBotBlock, started, id)
		return true
	}
	if h.botCookieValid(r, tun) || h.visitorAuthed(r, tun) {
		return false
	}
	st := h.bot()
	ip := clientIP(r)
	fake := false
	if sec.Bot.AllowVerified {
		verified, claimed := st.vb.Verify(ip, ua)
		if verified {
			return false
		}
		fake = claimed
	}
	if sec.Bot.Mode == tunsec.BotSuspicious {
		var fp *botguard.TLSFingerprint
		if r.TLS != nil {
			fp = h.TLSFP.Get(r.RemoteAddr)
		}
		res := botguard.Score(r, fp)
		if fake {
			res.Score += botguard.ScoreFakeBot
		}
		if !res.Suspicious() {
			return false
		}
	}
	h.requireChallenge(w, r, tun, sec, RejectBotChallenge, started)
	return true
}

// serveReserved, tunel host'undaki /.zorven/* yollarini ele alir; bu yollar
// ASLA tunele iletilmez. /.zorven/challenge: PoW cozumunu dogrular.
func (h *Handler) serveReserved(w http.ResponseWriter, r *http.Request, tun store.HostRoute, sec *tunsec.Compiled, started time.Time) {
	if r.URL.Path != botguard.ChallengePath || !sec.ChallengeAvailable() {
		writeError(w, r, http.StatusNotFound, protocol.CodeTunnelNotFound, "Bulunamadı.")
		return
	}
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", "POST")
		writeError(w, r, http.StatusMethodNotAllowed, "method_not_allowed", "POST gerekli.")
		return
	}
	st := h.bot()
	var body struct {
		Token   string `json:"token"`
		Counter string `json:"counter"`
		Next    string `json:"next"`
	}
	data, _ := io.ReadAll(io.LimitReader(r.Body, 4096))
	ip := clientIP(r)
	now := time.Now()
	fail := func() {
		st.counters(tun.TunnelID).Failed.Add(1)
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte(`{"error":{"code":"challenge_failed","message":"Verification failed."}}` + "\n"))
		h.logRejectID(tun, r, http.StatusForbidden, RejectBotFail, started, "")
	}
	if json.Unmarshal(data, &body) != nil {
		fail()
		return
	}
	nonce, bits, exp, err := st.keys.ParseChallenge(body.Token, tun.TunnelID, ip, sec.Bot.Difficulty, now)
	if err != nil || !botguard.CheckPoW(nonce, body.Counter, bits) || !st.replay.Use(nonce, exp, now) {
		fail()
		return
	}
	ttl := time.Duration(sec.Bot.TTLMinutes) * time.Minute
	val, cexp := st.keys.IssueCookie(tun.TunnelID, ip, r.UserAgent(), now, ttl)
	http.SetCookie(w, &http.Cookie{
		Name: botguard.CookieName, Value: val, Path: "/", Expires: cexp, MaxAge: int(ttl.Seconds()),
		HttpOnly: true, Secure: r.TLS != nil, SameSite: http.SameSiteLaxMode,
	})
	st.counters(tun.TunnelID).Passed.Add(1)
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	_ = json.NewEncoder(w).Encode(map[string]any{"ok": true, "next": botguard.SafeNext(body.Next)})
}
