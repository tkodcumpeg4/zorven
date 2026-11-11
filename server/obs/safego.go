package obs

import (
	"context"
	"log/slog"
	"os"
	"runtime/debug"
	"time"
)

// Arka plan goroutine'lerinde panik (A1 #10). net/http yalniz isleyici
// goroutine'ini kurtarir; zamanlayici, supurucu, persister gibi uzun omurlu
// islerdeki bir panik TUM sureci (tum kiracilarin tunellerini) dusururdu ve
// iz yalniz stderr'e duserdi.
//
// Iki politika:
//
//   - GoRestart: durumsuz/periyodik isler (degerlendiriciler, supurucular,
//     istatistik toplayicilar, yeniden yukleyiciler). Panik loglanir, sayilir ve
//     is ussel bekleyerek (1 sn -> 1 dk) yeniden baslatilir. Is normal donerse
//     (ctx bitti) yeniden baslatilmaz. Bir isin bir turu bozuk veriyle panikliyorsa
//     surekli yeniden baslatma log/CPU'yu yer; bekleme bu yuzden ussel.
//   - GoFatal: bellek ici durumu olan ve yarim kalmis bir panikte tutarliligi
//     garanti edilemeyen isler (log persister'lari, ag yoneticisi). Panik loglanir,
//     sayilir, sonra surec cikis kodu 2 ile kontrollu sonlanir; compose
//     "restart: unless-stopped" temiz bir surec baslatir. Yari bozuk durumla
//     calismaya devam etmek (kilit tutulurken panik = kalici kilitlenme) daha
//     kotudur.

// FatalExit, GoFatal'in cikis fonksiyonu (testler degistirir).
var FatalExit = func(code int) { os.Exit(code) }

const (
	restartMin = time.Second
	restartMax = time.Minute
)

// GoRestart, fn'i arka planda calistirir; paniklerse yeniden baslatir.
func GoRestart(ctx context.Context, log *slog.Logger, name string, fn func(context.Context)) {
	go func() {
		wait := restartMin
		for {
			if !runRecovered(ctx, log, name, fn) {
				return // normal donus
			}
			select {
			case <-ctx.Done():
				return
			case <-time.After(wait):
			}
			if log != nil {
				log.Warn("arka plan isi panik sonrasi yeniden baslatiliyor", "is", name, "bekleme", wait)
			}
			if wait *= 2; wait > restartMax {
				wait = restartMax
			}
		}
	}()
}

// GoFatal, fn'i arka planda calistirir; paniklerse loglayip sureci sonlandirir.
func GoFatal(ctx context.Context, log *slog.Logger, name string, fn func(context.Context)) {
	go func() {
		if runRecovered(ctx, log, name, fn) {
			if log != nil {
				log.Error("durumlu arka plan isi panikledi; surec kontrollu olarak sonlandiriliyor", "is", name)
			}
			FatalExit(2)
		}
	}()
}

// runRecovered, fn'i calistirir; panik olduysa true doner (loglanmis ve sayilmis).
func runRecovered(ctx context.Context, log *slog.Logger, name string, fn func(context.Context)) (panicked bool) {
	defer func() {
		if p := recover(); p != nil {
			panicked = true
			CountPanic(name)
			if log != nil {
				log.Error("arka plan isi panikledi", "is", name, "panik", p, "yigin", string(debug.Stack()))
			}
		}
	}()
	fn(ctx)
	return false
}
