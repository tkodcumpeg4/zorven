package mail

import (
	"context"
	"crypto/rand"
	"errors"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/tkodcumpeg4/zorven/server/auth"
	"github.com/tkodcumpeg4/zorven/server/store"
)

// Kimlik dogrulama hatalari. ErrAuthFailed kullaniciya her zaman ayni genel
// mesajla doner (kullanici adi/parola ayrimi sizdirilmaz).
var (
	ErrAuthFailed      = errors.New("kimlik dogrulama basarisiz")
	ErrAuthRateLimited = errors.New("cok fazla basarisiz deneme")
)

const (
	// Basarisiz giris siniri: IP+kullanici basina 10 dk'da 10, IP basina 60.
	failWindow      = 10 * time.Minute
	failLimitPair   = 10
	failLimitIP     = 60
	appPasswordLen  = 16
	appPasswordAlph = "abcdefghjkmnpqrstuvwxyz23456789" // belirsiz karakterler (i,l,o,0,1) yok
)

// GenerateAppPassword, xxxx-xxxx-xxxx-xxxx bicimli rastgele bir uygulama
// parolasi uretir (31^16, yaklasik 79 bit).
func GenerateAppPassword() string {
	var b [appPasswordLen]byte
	buf := make([]byte, appPasswordLen*2)
	_, _ = rand.Read(buf)
	// Modulo yanliligini onlemek icin reddetmeli ornekleme.
	limit := 256 - (256 % len(appPasswordAlph))
	for i, n := 0, 0; i < appPasswordLen; {
		if n >= len(buf) {
			_, _ = rand.Read(buf)
			n = 0
		}
		v := int(buf[n])
		n++
		if v >= limit {
			continue
		}
		b[i] = appPasswordAlph[v%len(appPasswordAlph)]
		i++
	}
	var sb strings.Builder
	for i, c := range b {
		if i > 0 && i%4 == 0 {
			sb.WriteByte('-')
		}
		sb.WriteByte(c)
	}
	return sb.String()
}

// NormalizeAppPassword, kullanicinin yazdigi/yapistirdigi parolayi (bosluk,
// tire, buyuk harf farklarini yok sayarak) saklama bicimine cevirir.
func NormalizeAppPassword(p string) string {
	return strings.ToLower(strings.NewReplacer("-", "", " ", "", "\t", "").Replace(p))
}

// HashAppPassword, normalize edilmis parolanin argon2id hash'ini uretir.
func HashAppPassword(p string) (string, error) {
	return auth.HashSecret(NormalizeAppPassword(p))
}

// Account, dogrulanmis bir posta kutusu oturumudur.
type Account struct {
	TenantID   string
	Address    string // kucuk harf, tam adres
	System     bool   // <lp>@<platformDomain> sistem kutusu
	PasswordID string
}

// Authenticator, IMAP ve SMTP gonderim sunuculari icin ortak kimlik dogrulayici.
type Authenticator struct {
	Store            store.Store
	MailDomain       string
	PlatformDomain   string
	SystemLocalParts map[string]bool
	Logger           *slog.Logger

	mu    sync.Mutex
	fails map[string][]time.Time
	now   func() time.Time
}

// NewAuthenticator, kimlik dogrulayici olusturur.
func NewAuthenticator(st store.Store, mailDomain, platformDomain string, systemLocalParts []string, logger *slog.Logger) *Authenticator {
	sys := make(map[string]bool, len(systemLocalParts))
	for _, lp := range systemLocalParts {
		if lp = strings.ToLower(strings.TrimSpace(lp)); lp != "" {
			sys[lp] = true
		}
	}
	if logger == nil {
		logger = slog.Default()
	}
	return &Authenticator{
		Store:            st,
		MailDomain:       strings.ToLower(strings.TrimSpace(mailDomain)),
		PlatformDomain:   strings.ToLower(strings.TrimSpace(platformDomain)),
		SystemLocalParts: sys,
		Logger:           logger,
		fails:            make(map[string][]time.Time),
		now:              time.Now,
	}
}

// ResolveMailbox, bir e-posta adresinin hangi kiraciya ait gecerli bir kutu
// oldugunu bulur: <slug>@<mailDomain> veya sistem kutusu (platform kiracisi).
func (a *Authenticator) ResolveMailbox(ctx context.Context, addr string) (tenantID string, system, ok bool) {
	addr = strings.ToLower(strings.TrimSpace(addr))
	at := strings.LastIndex(addr, "@")
	if at <= 0 || at == len(addr)-1 {
		return "", false, false
	}
	local, domain := addr[:at], addr[at+1:]
	if a.PlatformDomain != "" && domain == a.PlatformDomain {
		if a.SystemLocalParts[local] {
			return store.DefaultTenantID, true, true
		}
		return "", false, false
	}
	if a.MailDomain != "" && domain == a.MailDomain {
		t, err := a.Store.GetTenantBySlug(ctx, local)
		if err != nil {
			return "", false, false
		}
		return t.ID, false, true
	}
	return "", false, false
}

func (a *Authenticator) blocked(ip, user string) bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	cut := a.now().Add(-failWindow)
	count := func(key string) int {
		l := a.fails[key]
		i := 0
		for i < len(l) && l[i].Before(cut) {
			i++
		}
		l = l[i:]
		if len(l) == 0 {
			delete(a.fails, key)
		} else {
			a.fails[key] = l
		}
		return len(l)
	}
	return count("p|"+ip+"|"+user) >= failLimitPair || count("i|"+ip) >= failLimitIP
}

func (a *Authenticator) recordFail(ip, user string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	now := a.now()
	a.fails["p|"+ip+"|"+user] = append(a.fails["p|"+ip+"|"+user], now)
	a.fails["i|"+ip] = append(a.fails["i|"+ip], now)
	// Bellek sinirla: cok fazla anahtar birikirse eskileri at.
	if len(a.fails) > 20000 {
		cut := now.Add(-failWindow)
		for k, l := range a.fails {
			if len(l) == 0 || l[len(l)-1].Before(cut) {
				delete(a.fails, k)
			}
		}
	}
}

func (a *Authenticator) clearFails(ip, user string) {
	a.mu.Lock()
	delete(a.fails, "p|"+ip+"|"+user)
	a.mu.Unlock()
}

// Authenticate, tam e-posta adresi + uygulama parolasini dogrular. proto
// ("imap" | "smtp") yalnizca gunluge yazilir.
func (a *Authenticator) Authenticate(ctx context.Context, proto, username, password, ip string) (*Account, error) {
	user := strings.ToLower(strings.TrimSpace(username))
	if a.blocked(ip, user) {
		a.Logger.Warn("mail istemcisi girisi hiz siniri nedeniyle reddedildi", "proto", proto, "kullanici", user, "ip", ip)
		return nil, ErrAuthRateLimited
	}
	fail := func(reason string) (*Account, error) {
		a.recordFail(ip, user)
		a.Logger.Warn("mail istemcisi girisi basarisiz", "proto", proto, "kullanici", user, "ip", ip, "neden", reason)
		return nil, ErrAuthFailed
	}

	tenantID, system, ok := a.ResolveMailbox(ctx, user)
	if !ok {
		auth.VerifyDummy(NormalizeAppPassword(password))
		return fail("bilinmeyen kutu")
	}
	pws, err := a.Store.ListActiveMailAppPasswords(ctx, user)
	if err != nil {
		a.Logger.Error("uygulama parolalari okunamadi", "hata", err)
		return nil, ErrAuthFailed
	}
	norm := NormalizeAppPassword(password)
	if len(pws) == 0 {
		auth.VerifyDummy(norm)
		return fail("uygulama parolasi yok")
	}
	for _, p := range pws {
		if p.TenantID != tenantID {
			continue
		}
		if okPw, verr := auth.Verify(norm, p.PasswordHash); verr == nil && okPw {
			a.clearFails(ip, user)
			if terr := a.Store.TouchMailAppPassword(ctx, p.ID, ip); terr != nil {
				a.Logger.Warn("uygulama parolasi son kullanim guncellenemedi", "hata", terr)
			}
			a.Logger.Info("mail istemcisi girisi basarili", "proto", proto, "kullanici", user, "ip", ip, "parola_id", p.ID)
			return &Account{TenantID: tenantID, Address: user, System: system, PasswordID: p.ID}, nil
		}
	}
	return fail("parola yanlis")
}
