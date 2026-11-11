package auth

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"errors"
	"fmt"
	"os"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/tkodcumpeg4/zorven/server/obs"
)

// --- argon2 eszamanlilik kapisi (K1) -----------------------------------------
//
// Her argon2id cagrisi argonMemory (32 MB) ayirir. Sinir olmadan 64 eszamanli
// sahte token istegi 2-3 GB tepe bellek uretiyordu (prod makinesi 2.9 GB).
// Tum argon2 cagrilari bu kapidan gecer; boylece tepe bellek kabaca
// kapasite x 32 MB ile sinirlanir. Kapasite dolu ve kuyrukta bekleme suresi
// asilirsa ErrBusy doner: cagiran 503 + Retry-After yazar (istemci yeniden dener).

// ErrBusy, argon2 kapisi doluyken bekleme suresi asildiginda (ya da istek
// iptal edildiginde) doner. "Token gecersiz" DEGILDIR; gecici bir durumdur.
var ErrBusy = errors.New("kimlik dogrulama kapasitesi dolu")

const (
	// defaultGateWait, kuyrukta bekleme ust siniri. Gecerli bir istemcinin
	// kisa bir tepe aninda reddedilmemesi icin birkac argon2 suresi kadar.
	defaultGateWait = 2 * time.Second

	envGateSize = "ZORVEN_ARGON2_CONCURRENCY"
	envGateWait = "ZORVEN_ARGON2_QUEUE_WAIT"
)

var (
	gateOnce sync.Once
	gate     chan struct{}
	gateWait time.Duration
	inflight atomic.Int64
)

func init() {
	obs.RegisterGauge("zorven_argon2_inflight", "Su an calisan argon2 dogrulama/hash sayisi.",
		func() float64 { return float64(inflight.Load()) })
	obs.RegisterGauge("zorven_argon2_capacity", "Argon2 eszamanlilik kapisinin kapasitesi.",
		func() float64 { gateOnce.Do(initGate); return float64(cap(gate)) })
}

// gateSizeFromEnv, kapasiteyi belirler: env gecerli pozitif tamsayiysa o,
// degilse max(2, GOMAXPROCS/argonThreads). Her argon2 cagrisi zaten
// argonThreads (4) seridi paralel calistirir; GOMAXPROCS/4 eszamanli cagri
// CPU'yu doyurur, fazlasi yalniz sira bekleyip bellek tutar. Yerel olcum
// (8 cekirdek, farkli IP'lerden 256 sahte ajan token'i, 64 eszamanli):
// kapasite 8 -> tepe RSS 797 MB, 7.5 sn; kapasite 2 -> 241 MB, 4.8 sn.
func gateSizeFromEnv(get func(string) string) int {
	if v := strings.TrimSpace(get(envGateSize)); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			return n
		}
	}
	return max(2, runtime.GOMAXPROCS(0)/argonThreads)
}

func gateWaitFromEnv(get func(string) string) time.Duration {
	if v := strings.TrimSpace(get(envGateWait)); v != "" {
		if d, err := time.ParseDuration(v); err == nil && d > 0 {
			return d
		}
	}
	return defaultGateWait
}

func initGate() {
	configureGate(gateSizeFromEnv(os.Getenv), gateWaitFromEnv(os.Getenv))
}

// configureGate, kapiyi yeniden kurar (yalniz baslangic ve testler; calisan
// dogrulamalar varken cagrilmamali).
func configureGate(size int, wait time.Duration) {
	gate = make(chan struct{}, size)
	gateWait = wait
}

// GateCapacity, argon2 kapisinin kapasitesi (baslangic logu icin).
func GateCapacity() int {
	gateOnce.Do(initGate)
	return cap(gate)
}

// acquire, kapidan bir yer alir. bounded ise en fazla gateWait bekler;
// degilse yalniz ctx ile sinirli bekler (hash uretimi gibi kimlikli ve seyrek yollar).
func acquire(ctx context.Context, bounded bool) error {
	gateOnce.Do(initGate)
	g := gate
	wait := time.Duration(0)
	if bounded {
		wait = gateWait
	}
	select {
	case g <- struct{}{}:
		inflight.Add(1)
		return nil
	default:
	}
	var timeout <-chan time.Time
	if wait > 0 {
		t := time.NewTimer(wait)
		defer t.Stop()
		timeout = t.C
	}
	select {
	case g <- struct{}{}:
		inflight.Add(1)
		return nil
	case <-timeout:
		return ErrBusy
	case <-ctx.Done():
		return fmt.Errorf("%w: %w", ErrBusy, ctx.Err())
	}
}

func release() {
	inflight.Add(-1)
	<-gate
}

// --- dogrulanmis sir onbellegi (K1) ------------------------------------------
//
// Ayni gecerli token her istekte yeniden argon2'den gecmesin diye (API c=1'de
// p50 134 ms, ziyaretci Basic auth'ta her istekte 32 MB) BASARILI dogrulamalar
// kisa sure hatirlanir. Yalniz argon2 adimi atlanir: cagiranlar token/parola
// kaydini her istekte depodan yine okur, boylece iptal/silme aninda gecerli
// kalir. Anahtar, kapsam + sir + saklanan hash'in surec-ozel HMAC'idir: hash
// degisirse (token yenileme, parola degisimi) kayit kendiliginden gecersizdir;
// bellek dokumunde sir ya da cevrimdisi kaba kuvvete uygun ozet bulunmaz.

const (
	verifiedTTL = 60 * time.Second
	verifiedMax = 4096
)

type verifiedCache struct {
	mu  sync.Mutex
	key []byte
	m   map[[32]byte]time.Time
	now func() time.Time
}

var okCache = newVerifiedCache()

func newVerifiedCache() *verifiedCache {
	k := make([]byte, 32)
	if _, err := rand.Read(k); err != nil {
		// crypto/rand yoksa token uretimi de calismaz; onbellek devre disi kalir.
		k = nil
	}
	return &verifiedCache{key: k, m: make(map[[32]byte]time.Time), now: time.Now}
}

func (c *verifiedCache) digest(scope, secret, encodedHash string) ([32]byte, bool) {
	var out [32]byte
	if c.key == nil {
		return out, false
	}
	mac := hmac.New(sha256.New, c.key)
	mac.Write([]byte(scope))
	mac.Write([]byte{0})
	mac.Write([]byte(secret))
	mac.Write([]byte{0})
	mac.Write([]byte(encodedHash))
	copy(out[:], mac.Sum(nil))
	return out, true
}

func (c *verifiedCache) hit(k [32]byte) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	exp, ok := c.m[k]
	if !ok {
		return false
	}
	if !c.now().Before(exp) {
		delete(c.m, k)
		return false
	}
	return true
}

func (c *verifiedCache) put(k [32]byte) {
	c.mu.Lock()
	defer c.mu.Unlock()
	now := c.now()
	if len(c.m) >= verifiedMax {
		for kk, exp := range c.m {
			if !now.Before(exp) {
				delete(c.m, kk)
			}
		}
		// Hala doluysa rastgele (map sirasi) kayitlar dusurulur: en kotu durumda
		// bir sonraki istek yeniden argon2'den gecer, dogruluk etkilenmez.
		for kk := range c.m {
			if len(c.m) < verifiedMax {
				break
			}
			delete(c.m, kk)
		}
	}
	c.m[k] = now.Add(verifiedTTL)
}

func (c *verifiedCache) len() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.m)
}

// CachedOK, (kapsam, sir, hash) uclusu yakin zamanda basariyla dogrulandiysa
// true doner; argon2 calistirmaz. Cagiranlar bunu IP basina basarisiz deneme
// sinirindan ONCE sorar: ayni IP'yi paylasan (NAT) bir saldirgan, onbellekte
// olan gecerli bir istemciyi 429'a dusuremesin.
func CachedOK(scope, secret, encodedHash string) bool {
	k, ok := okCache.digest(scope, secret, encodedHash)
	return ok && okCache.hit(k)
}

// VerifyCached, VerifyContext gibidir ama yakin zamanda ayni (kapsam, sir,
// hash) ucluyu basariyla dogruladiysa argon2 calistirmadan true doner.
// scope, kaydin turunu ve kimligini tasir (or. "api:"+tokenID); farkli
// turlerin ayni hash'i paylasmasi durumunda bile karismasini onler.
// Basarisiz sonuc ASLA onbelleklenmez.
func VerifyCached(ctx context.Context, scope, secret, encodedHash string) (bool, error) {
	k, ok := okCache.digest(scope, secret, encodedHash)
	if ok && okCache.hit(k) {
		return true, nil
	}
	valid, err := VerifyContext(ctx, secret, encodedHash)
	if err == nil && valid && ok {
		okCache.put(k)
	}
	return valid, err
}
