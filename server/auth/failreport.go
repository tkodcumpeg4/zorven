package auth

import (
	"log/slog"
	"sync"
	"time"

	"github.com/tkodcumpeg4/zorven/server/obs"
)

// Basarisiz kimlik dogrulama izi (O16). Onceden 300 gecersiz token hicbir log,
// denetim kaydi ya da metrik birakmiyordu; saldiri ancak bellek grafiginden
// fark edilirdi. Simdi her basarisizlik sayilir (zorven_auth_failures_total)
// ve ornekli WARN yazilir. Log seli olmasin diye pencere basina en fazla
// birkac satir; bastirilan satir sayisi bir sonraki satirda ozetlenir.
// Satirlar SIR ya da token govdesi ICERMEZ: yalniz tur, neden, IP.

// Tur (kind) degerleri: metrik etiketiyle ayni sabit kume (obs/auth.go).
const (
	KindAPIToken    = "api_token"
	KindClientToken = "client_token"
	KindAdminKey    = "admin_key"
	KindAgent       = "agent"
	KindBasic       = "basic"
	KindMail        = "mail"
)

// Neden (reason) degerleri.
const (
	ReasonMalformed   = "malformed"
	ReasonUnknown     = "unknown"
	ReasonInvalid     = "invalid"
	ReasonExpired     = "expired"
	ReasonRateLimited = "rate_limited"
	ReasonBusy        = "busy"
	ReasonStoreError  = "store_error"
)

// logSampler, pencere basina en fazla limit satira izin verir.
type logSampler struct {
	mu         sync.Mutex
	window     time.Duration
	limit      int
	start      time.Time
	n          int
	suppressed int
	now        func() time.Time
}

// allow, satir yazilabilirse true ve onceki pencerede bastirilan sayiyi doner.
func (s *logSampler) allow() (bool, int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := s.now()
	if now.Sub(s.start) >= s.window {
		s.start, s.n = now, 0
	}
	if s.n >= s.limit {
		s.suppressed++
		return false, 0
	}
	s.n++
	sup := s.suppressed
	s.suppressed = 0
	return true, sup
}

var (
	failSampler  = &logSampler{window: 10 * time.Second, limit: 10, now: time.Now}
	storeSampler = &logSampler{window: 10 * time.Second, limit: 5, now: time.Now}
)

// ReportFailure, basarisiz kimlik dogrulamasini sayar ve ornekli WARN yazar.
// log nil olabilir (yalniz sayac).
func ReportFailure(log *slog.Logger, kind, reason, ip string) {
	obs.CountAuthFailure(kind, reason)
	if log == nil {
		return
	}
	ok, sup := failSampler.allow()
	if !ok {
		return
	}
	args := []any{"tur", kind, "neden", reason, "ip", ip}
	if sup > 0 {
		args = append(args, "bastirilan", sup)
	}
	log.Warn("kimlik dogrulama basarisiz", args...)
}

// ReportStoreError, kimlik dogrulama sirasinda depo hatasini (DB kapali,
// havuz zaman asimi) sayar ve ornekli ERROR yazar. Bu durum "token gecersiz"
// degildir; cagiran 503 doner.
func ReportStoreError(log *slog.Logger, kind, ip string, err error) {
	obs.CountAuthFailure(kind, ReasonStoreError)
	if log == nil {
		return
	}
	ok, sup := storeSampler.allow()
	if !ok {
		return
	}
	args := []any{"tur", kind, "ip", ip, "hata", err}
	if sup > 0 {
		args = append(args, "bastirilan", sup)
	}
	log.Error("kimlik dogrulama deposu okunamadi", args...)
}
