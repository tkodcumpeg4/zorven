// Package botguard, tunel bot korumasinin (B1 PoW sorgusu, B2 hiz siniri, B3 supheli
// skor) saf mantigidir. Ag/veritabani yoktur (dogrulanmis bot DNS'i Resolver
// arayuzu arkasindadir); ingress yalniz hazir yapilari cagirir.
//
// ANAHTARLAR: ana anahtar (ingress.Handler.BotKey; main.go ziyaretci sirrindan
// HKDF ile "zorven-bot-v1" amaciyla turetir) burada ikiye daha bolunur
// (HMAC-SHA256(master, etiket)): cerez anahtari ve sorgu (challenge) anahtari.
// Boylece bir sorgu jetonu cerez olarak, cerez de sorgu olarak kabul edilemez.
package botguard

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/netip"
	"strconv"
	"strings"
	"sync"
	"time"
)

// CookieName, basarili sorgu sonrasi verilen imzali cerez.
const CookieName = "__zv_bot"

// ChallengePath, dogrulama ucu (tunel host'unda rezerve; tunele ILETILMEZ).
const ChallengePath = "/.zorven/challenge"

// ChallengeLifetime, sorgu jetonunun gecerlilik suresi.
const ChallengeLifetime = 5 * time.Minute

// Hatalar.
var (
	ErrToken      = errors.New("gecersiz jeton")
	ErrExpired    = errors.New("sorgu suresi doldu")
	ErrPoW        = errors.New("gecersiz cozum")
	ErrReplay     = errors.New("sorgu zaten kullanildi")
	ErrDifficulty = errors.New("zorluk yetersiz")
)

// Keys, ana anahtardan turetilmis amaca ozel anahtarlar.
type Keys struct {
	cookie    []byte
	challenge []byte
}

// NewKeys, ana anahtardan cerez ve sorgu anahtarlarini turetir. master bos ise
// rastgele bir anahtar uretilir (yalniz bu surec icin gecerli; coklu dugumde
// cerezler dugumler arasi gecmez - main.go her zaman master verir).
func NewKeys(master []byte) *Keys {
	if len(master) == 0 {
		master = make([]byte, 32)
		_, _ = rand.Read(master)
	}
	d := func(label string) []byte {
		m := hmac.New(sha256.New, master)
		m.Write([]byte(label))
		return m.Sum(nil)
	}
	return &Keys{cookie: d("zorven-bot-cookie-v1"), challenge: d("zorven-bot-challenge-v1")}
}

func b64(b []byte) string { return base64.RawURLEncoding.EncodeToString(b) }

func (k *Keys) sign(key []byte, payload []byte) string {
	m := hmac.New(sha256.New, key)
	m.Write(payload)
	return b64(payload) + "." + b64(m.Sum(nil))
}

// open, "payload.mac" jetonunu dogrular ve ham payload'u doner.
func (k *Keys) open(key []byte, tok string) ([]byte, bool) {
	p, mac, ok := strings.Cut(tok, ".")
	if !ok || len(tok) > 1024 {
		return nil, false
	}
	payload, err := base64.RawURLEncoding.DecodeString(p)
	if err != nil {
		return nil, false
	}
	got, err := base64.RawURLEncoding.DecodeString(mac)
	if err != nil {
		return nil, false
	}
	m := hmac.New(sha256.New, key)
	m.Write(payload)
	if subtle.ConstantTimeCompare(m.Sum(nil), got) != 1 {
		return nil, false
	}
	return payload, true
}

// IPPrefix, IP'yi cerez/limit baglama anahtarina cevirir: IPv4 /24, IPv6 /64.
// Cozulemeyen girdi oldugu gibi (kucuk harf) doner.
func IPPrefix(ip string) string {
	a, err := netip.ParseAddr(strings.Trim(ip, "[]"))
	if err != nil {
		return strings.ToLower(ip)
	}
	a = a.Unmap().WithZone("")
	bits := 64
	if a.Is4() {
		bits = 24
	}
	p, err := a.Prefix(bits)
	if err != nil {
		return a.String()
	}
	return p.String()
}

// UAHash, User-Agent'in kisaltilmis SHA-256 ozeti (8 bayt, onaltilik).
func UAHash(ua string) string {
	s := sha256.Sum256([]byte(ua))
	const hexd = "0123456789abcdef"
	out := make([]byte, 16)
	for i := 0; i < 8; i++ {
		out[2*i], out[2*i+1] = hexd[s[i]>>4], hexd[s[i]&15]
	}
	return string(out)
}

// ---- __zv_bot cerezi ----

type cookiePayload struct {
	T string `json:"t"` // tunel id
	I string `json:"i"` // IP onegi
	U string `json:"u"` // UA ozeti
	A int64  `json:"a"` // verilis (unix)
	E int64  `json:"e"` // bitis (unix)
}

// IssueCookie, tunel + IP onegi + UA ozetine baglanmis imzali cerez degeri uretir.
func (k *Keys) IssueCookie(tunnelID, ip, ua string, now time.Time, ttl time.Duration) (string, time.Time) {
	exp := now.Add(ttl)
	b, _ := json.Marshal(cookiePayload{T: tunnelID, I: IPPrefix(ip), U: UAHash(ua), A: now.Unix(), E: exp.Unix()})
	return k.sign(k.cookie, b), exp
}

// VerifyCookie, cerezin imzasini, suresini ve tunel/IP/UA baglamasini denetler.
func (k *Keys) VerifyCookie(val, tunnelID, ip, ua string, now time.Time) bool {
	raw, ok := k.open(k.cookie, val)
	if !ok {
		return false
	}
	var p cookiePayload
	if json.Unmarshal(raw, &p) != nil {
		return false
	}
	if p.T != tunnelID || p.I != IPPrefix(ip) || p.U != UAHash(ua) {
		return false
	}
	return now.Unix() < p.E && p.A <= now.Unix()+60
}

// ---- PoW sorgusu ----

type challengePayload struct {
	T string `json:"t"` // tunel id
	N string `json:"n"` // nonce (base64url, 16 bayt)
	B int    `json:"b"` // gerekli ilk sifir bit sayisi
	E int64  `json:"e"` // bitis (unix)
	I string `json:"i"` // IP onegi
}

// NewChallenge, durumsuz (sunucuda saklanmayan) imzali sorgu uretir. Dondurulen
// nonce, istemcinin SHA-256(nonce || sayac) girdisinde kullandigi metindir.
func (k *Keys) NewChallenge(tunnelID, ip string, bits int, now time.Time) (token, nonce string) {
	raw := make([]byte, 16)
	_, _ = rand.Read(raw)
	nonce = b64(raw)
	b, _ := json.Marshal(challengePayload{T: tunnelID, N: nonce, B: bits, E: now.Add(ChallengeLifetime).Unix(), I: IPPrefix(ip)})
	return k.sign(k.challenge, b), nonce
}

// ParseChallenge, jetonu dogrular (imza, tunel, IP onegi, sure, zorluk >= minBits).
func (k *Keys) ParseChallenge(token, tunnelID, ip string, minBits int, now time.Time) (nonce string, bits int, exp time.Time, err error) {
	raw, ok := k.open(k.challenge, token)
	if !ok {
		return "", 0, time.Time{}, ErrToken
	}
	var p challengePayload
	if json.Unmarshal(raw, &p) != nil || p.N == "" {
		return "", 0, time.Time{}, ErrToken
	}
	if p.T != tunnelID || p.I != IPPrefix(ip) {
		return "", 0, time.Time{}, ErrToken
	}
	if now.Unix() >= p.E {
		return "", 0, time.Time{}, ErrExpired
	}
	if p.B < minBits || p.B > 32 {
		return "", 0, time.Time{}, ErrDifficulty
	}
	return p.N, p.B, time.Unix(p.E, 0), nil
}

// LeadingZeroBits, ozetin basindaki sifir bit sayisi.
func LeadingZeroBits(sum []byte) int {
	n := 0
	for _, b := range sum {
		if b == 0 {
			n += 8
			continue
		}
		for m := byte(0x80); m != 0 && b&m == 0; m >>= 1 {
			n++
		}
		break
	}
	return n
}

// CheckPoW, SHA-256(nonce || counter) ozetinde en az bits bas sifir bit var mi.
// counter yalniz onluk rakam olmali (en fazla 20 hane; basinda sifir yok).
func CheckPoW(nonce, counter string, bits int) bool {
	if l := len(counter); l == 0 || l > 20 || (l > 1 && counter[0] == '0') {
		return false
	}
	if _, err := strconv.ParseUint(counter, 10, 64); err != nil {
		return false
	}
	sum := sha256.Sum256([]byte(nonce + counter))
	return LeadingZeroBits(sum[:]) >= bits
}

// SolvePoW, testler ve araclar icin kaba kuvvet cozucu.
func SolvePoW(nonce string, bits int) string {
	for c := uint64(0); ; c++ {
		s := strconv.FormatUint(c, 10)
		if CheckPoW(nonce, s, bits) {
			return s
		}
	}
}

// ---- tek kullanimlik nonce (replay) ----

// Replay, kullanilmis nonce'lari bitislerine kadar tutan sinirli bellek onbellegi.
type Replay struct {
	mu   sync.Mutex
	used map[string]time.Time
	cap  int
}

// NewReplay, en fazla max girdi tutan onbellek.
func NewReplay(max int) *Replay {
	if max <= 0 {
		max = 100_000
	}
	return &Replay{used: make(map[string]time.Time), cap: max}
}

// Use, nonce'u kullanilmis isaretler. false: zaten kullanilmis ya da onbellek dolu
// (fail-closed; sureleri gecenler once silinir).
func (r *Replay) Use(nonce string, exp, now time.Time) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	if e, ok := r.used[nonce]; ok && now.Before(e) {
		return false
	}
	if len(r.used) >= r.cap {
		for n, e := range r.used {
			if !now.Before(e) {
				delete(r.used, n)
			}
		}
		if len(r.used) >= r.cap {
			return false
		}
	}
	r.used[nonce] = exp
	return true
}

// Len, onbellekteki girdi sayisi (test/izleme).
func (r *Replay) Len() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.used)
}

// SafeNext, yonlendirme hedefini ayni-host goreli yola indirger (acik yonlendirme
// engeli). Gecersizse "/" doner. Dahili /.zorven/ yollarina yonlendirilmez.
func SafeNext(next string) string {
	if next == "" || len(next) > 2048 || next[0] != '/' {
		return "/"
	}
	if len(next) > 1 && (next[1] == '/' || next[1] == '\\') {
		return "/"
	}
	for i := 0; i < len(next); i++ {
		c := next[i]
		if c < 0x20 || c == 0x7f || c == '\\' {
			return "/"
		}
	}
	if strings.HasPrefix(next, "/.zorven/") {
		return "/"
	}
	return next
}
