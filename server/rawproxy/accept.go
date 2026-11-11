package rawproxy

import (
	"errors"
	"log/slog"
	"net"
	"sync/atomic"
	"time"
)

// Kabul (Accept) / okuma dongulerinde gecici hatalara dayaniklilik.
//
// NEDEN: dosya tanimlayici tukenmesi (EMFILE/ENFILE), cekirdek tampon/bellek
// baskisi (ENOBUFS/ENOMEM) ya da el sikisma oncesi kopan baglanti
// (ECONNABORTED) gecicidir. Eskiden ana :443 demux dongusu herhangi bir hatada
// dinleyiciyi kapatip cikiyordu ve sunucu sureci dusuyordu (ulimit 1024 + 2000
// bos TCP ile kimliksiz uretildi). net/http.Server.Serve'in yaptigi gibi
// bekleyip devam ediyoruz; yalniz dinleyici gercekten kapatildiysa cikiyoruz.

const (
	acceptBackoffMin = 5 * time.Millisecond
	acceptBackoffMax = time.Second
)

// acceptBackoff, ardisik hatalarda bekleme suresini ikiye katlar (5 ms - 1 sn).
type acceptBackoff struct{ d time.Duration }

func (b *acceptBackoff) next() time.Duration {
	if b.d == 0 {
		b.d = acceptBackoffMin
	} else {
		b.d *= 2
	}
	if b.d > acceptBackoffMax {
		b.d = acceptBackoffMax
	}
	return b.d
}

func (b *acceptBackoff) reset() { b.d = 0 }

// isClosedErr, dinleyici/soket kapatildigi icin donen hata mi. Yalniz bu
// durumda dongu biter; diger tum hatalar (gecici olsun olmasin) beklenip
// yeniden denenir: kalici bozuk bir dinleyici en fazla saniyede bir denenir
// ve log hiz sinirlidir, ama sureci ASLA dusurmez.
func isClosedErr(err error) bool {
	return errors.Is(err, net.ErrClosed)
}

// acceptErrLog, hiz sinirli uyari: en fazla 10 sn'de bir satir.
type acceptErrLog struct {
	last       atomic.Int64
	suppressed atomic.Int64
}

func (l *acceptErrLog) warn(log *slog.Logger, msg string, err error, wait time.Duration, attrs ...any) {
	now := time.Now().UnixNano()
	last := l.last.Load()
	if now-last < int64(10*time.Second) || !l.last.CompareAndSwap(last, now) {
		l.suppressed.Add(1)
		return
	}
	if log == nil {
		return
	}
	args := append([]any{"hata", err, "bekleme", wait, "bastirilan", l.suppressed.Swap(0)}, attrs...)
	log.Warn(msg, args...)
}

var acceptLog acceptErrLog

// sleepOrDone, d kadar bekler; done kapanirsa erken doner (false).
func sleepOrDone(d time.Duration, done <-chan struct{}) bool {
	if done == nil {
		time.Sleep(d)
		return true
	}
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-t.C:
		return true
	case <-done:
		return false
	}
}
