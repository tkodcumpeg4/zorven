package wgnet

import (
	"errors"
	"log/slog"
	"net"
	"time"
)

// acceptRetry, dinleyici dongulerinde gecici Accept hatasi (EMFILE/ENFILE,
// ENOBUFS, ECONNABORTED...) sonrasi ussel bekleme.
//
// NEDEN: dongu eskiden ILK hatada sessizce cikiyordu; fd siniri kisa sure
// dolunca (bircok eszamanli baglanti) paylasim / yerel yonlendirme dinleyicisi
// kalici olarak oluyor, kullanici bir sey gormuyordu. Sunucudaki
// rawproxy/accept.go ile ayni yaklasim: 5 ms'den 1 sn'ye ussel bekle, devam et;
// yalniz kapatilmis dinleyicide cik. Log 10 sn'de en fazla bir satir.
type acceptRetry struct {
	delay      time.Duration
	lastLog    time.Time
	suppressed int
}

const (
	acceptRetryMin = 5 * time.Millisecond
	acceptRetryMax = time.Second
)

// next, err sonrasi beklenecek sureyi doner ve gerekirse loglar.
func (b *acceptRetry) next(log *slog.Logger, what string, err error) time.Duration {
	if b.delay == 0 {
		b.delay = acceptRetryMin
	} else {
		b.delay = min(b.delay*2, acceptRetryMax)
	}
	if log != nil {
		if now := time.Now(); now.Sub(b.lastLog) >= 10*time.Second {
			log.Warn("dinleyici baglanti kabul edemedi, yeniden denenecek", "dinleyici", what, "hata", err,
				"bekleme", b.delay, "bastirilan", b.suppressed)
			b.lastLog, b.suppressed = now, 0
		} else {
			b.suppressed++
		}
	}
	return b.delay
}

// reset, basarili Accept sonrasi beklemeyi sifirlar.
func (b *acceptRetry) reset() { b.delay = 0 }

// isClosedErr, dinleyicinin kapatildigini gosteren hata mi.
func isClosedErr(err error) bool { return errors.Is(err, net.ErrClosed) }
