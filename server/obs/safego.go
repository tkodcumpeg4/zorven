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

// Go, istek basina acilan KISA OMURLU bir isi (denetim kaydi yazimi, son kullanim
// damgasi, config itmesi) arka planda calistirir. Panik loglanir ve
// zorven_panics_total{where=name} artar; is yeniden baslatilmaz (tek seferlik,
// kaybi zararsiz) ve surec ayakta kalir. Neden: net/http yalniz isleyici
// goroutine'ini kurtarir; isleyicinin actigi "go func" icindeki bir panik (or. depo
// surucusunde nil) tum sureci dusururdu.
func Go(log *slog.Logger, name string, fn func()) {
	go runRecovered(context.Background(), log, name, func(context.Context) { fn() })
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

// PanicCleanupTimeout, panik sonrasi temizligin (kapanis yolu) tamamlanmasi
// icin taninan sure (degisken: testler kisaltir). Asilirsa temizlik buyuk
// olasilikla panikleyen goroutine'in birakmadigi bir kilitte bekliyordur.
var PanicCleanupTimeout = 30 * time.Second

// Recover, oturum/akis kapsamli bir goroutine'de paniği kurtarir: tum sureci
// degil yalniz o oturumu/akisi dusurmek icin. `defer obs.Recover(...)` olarak
// DOGRUDAN ertelenmelidir (recover yalniz ertelenen fonksiyonun kendisinde
// calisir). Panikte: sayac (zorven_panics_total{where}), ERROR + yigin, sonra
// onPanic (kaynaklari normal kapanis yoluyla birakan kapatma). onPanic'in
// kendisi paniklerse o da kurtarilir. onPanic PanicCleanupTimeout icinde
// bitmezse (kilitlenme) surec GoFatal gibi kontrollu sonlanir: yari kilitli
// durumla calismak (ornegin bekleyen tum istekler sonsuza dek asili) daha kotu.
func Recover(log *slog.Logger, where string, onPanic func(p any)) {
	p := recover()
	if p == nil {
		return
	}
	if log == nil {
		log = slog.Default()
	}
	CountPanic(where)
	log.Error("goroutine panikledi; yalniz ilgili oturum/akis kapatiliyor", "yer", where, "panik", p, "yigin", string(debug.Stack()))
	if onPanic == nil {
		return
	}
	stop := PanicWatchdog(log, where)
	defer stop()
	defer func() {
		if p2 := recover(); p2 != nil {
			CountPanic(where)
			log.Error("panik sonrasi kapatma da panikledi", "yer", where, "panik", p2, "yigin", string(debug.Stack()))
		}
	}()
	onPanic(p)
}

// PanicWatchdog, panik sonrasi temizlik icin bekci kurar; temizlik bitince
// donen stop cagrilmalidir. Sure dolarsa ERROR loglanir ve FatalExit(2).
// Temizligin bir kismi ertelenmis fonksiyonlarda (Recover'dan SONRA calisan
// defer'lar) yapiliyorsa bekci onPanic icinde kurulup en son defer'da durdurulur.
func PanicWatchdog(log *slog.Logger, where string) (stop func()) {
	if log == nil {
		log = slog.Default()
	}
	t := time.AfterFunc(PanicCleanupTimeout, func() {
		log.Error("panik sonrasi temizlik zamaninda bitmedi (olasi kilitlenme); surec kontrollu olarak sonlandiriliyor", "yer", where)
		FatalExit(2)
	})
	return func() { t.Stop() }
}
