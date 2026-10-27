package rawproxy

import (
	"os"
	"strconv"
	"sync"
	"time"
)

// Ham TCP baglanti sinirlari. Her ziyaretci baglantisi sunucuda bir goroutine
// ve dosya tanimlayicisi, ajanda bir yerel baglanti tutar; IP listesi olmayan
// bir tunele internetten acilan sinirsiz/bosta baglantilar FD'leri tuketip
// diger kiracilari da etkileyebilir. Varsayilanlar ortam degiskeniyle degisir:
//
//	ZORVEN_RAW_TCP_MAX_CONNS          tum sunucu (vars. 8192)
//	ZORVEN_RAW_TCP_MAX_CONNS_TUNNEL   tunel basina (vars. 512)
//	ZORVEN_RAW_TCP_MAX_CONNS_IP       tunel x kaynak IP basina (vars. 64)
//	ZORVEN_RAW_TCP_IDLE_SEC           iki yonde de veri yoksa kapat (vars. 1800; 0 = kapali)
const (
	defMaxConns       = 8192
	defMaxConnsTunnel = 512
	defMaxConnsIP     = 64
	defIdle           = 30 * time.Minute
)

type connLimits struct {
	max, perTunnel, perIP int
	idle                  time.Duration

	mu       sync.Mutex
	total    int
	byTunnel map[string]int
	byIP     map[string]int // tunel|ip
}

func envInt(name string, def int) int {
	if v, err := strconv.Atoi(os.Getenv(name)); err == nil && v >= 0 {
		return v
	}
	return def
}

func newConnLimits() *connLimits {
	idle := defIdle
	if v, err := strconv.Atoi(os.Getenv("ZORVEN_RAW_TCP_IDLE_SEC")); err == nil && v >= 0 {
		idle = time.Duration(v) * time.Second
	}
	return &connLimits{
		max:       envInt("ZORVEN_RAW_TCP_MAX_CONNS", defMaxConns),
		perTunnel: envInt("ZORVEN_RAW_TCP_MAX_CONNS_TUNNEL", defMaxConnsTunnel),
		perIP:     envInt("ZORVEN_RAW_TCP_MAX_CONNS_IP", defMaxConnsIP),
		idle:      idle,
		byTunnel:  map[string]int{},
		byIP:      map[string]int{},
	}
}

// acquire, sinirlar icindeyse yeri ayirir ve birakma fonksiyonu doner.
// 0 degerli sinir = sinirsiz.
func (l *connLimits) acquire(tunnelID, ip string) (func(), bool) {
	k := tunnelID + "|" + ip
	l.mu.Lock()
	defer l.mu.Unlock()
	if (l.max > 0 && l.total >= l.max) ||
		(l.perTunnel > 0 && l.byTunnel[tunnelID] >= l.perTunnel) ||
		(l.perIP > 0 && l.byIP[k] >= l.perIP) {
		return nil, false
	}
	l.total++
	l.byTunnel[tunnelID]++
	l.byIP[k]++
	var once sync.Once
	return func() {
		once.Do(func() {
			l.mu.Lock()
			defer l.mu.Unlock()
			l.total--
			if l.byTunnel[tunnelID]--; l.byTunnel[tunnelID] <= 0 {
				delete(l.byTunnel, tunnelID)
			}
			if l.byIP[k]--; l.byIP[k] <= 0 {
				delete(l.byIP, k)
			}
		})
	}, true
}
