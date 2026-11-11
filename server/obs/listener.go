package obs

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"log/slog"
	"net"
	"net/http"
	"net/http/pprof"
	"strings"
	"sync/atomic"
	"time"
)

// --- hazir olma (/readyz) ---------------------------------------------------------

// DefaultMaxStale, yonlendirme verisinin hazir sayilabilecegi en buyuk yas.
const DefaultMaxStale = 30 * time.Minute

// Readiness, /readyz kontrollerinin girdileri.
type Readiness struct {
	// DBPing, veritabani erisimini dener (nil = kontrol yok, ok sayilir).
	DBPing func(ctx context.Context) error
	// MaxStale, yonlendirme verisinin (DB'den son basarili Reload ya da diskteki
	// goruntunun veri zamani) kabul edilen en buyuk yasi. 0 = DefaultMaxStale.
	MaxStale time.Duration
	// RequireDB, DB erisilemezken hazir olmayi (503) bozar. standalone/primary'de
	// true: yonetim API'si, kimlik dogrulama ve tunel olusturma DB'siz calismaz;
	// "ready" demek dis izlemeyi yaniltiyordu (A5 #2). Edge'de false: veri yolu
	// diskteki goruntuyle DB'siz hizmet verir, hazirlik yalniz yonlendirme yasina bakar.
	RequireDB bool
}

var (
	readiness atomic.Pointer[Readiness]
	draining  atomic.Bool
)

// SetDraining, kapanis basladiginda cagrilir: /readyz 503 doner ki yuk dengeleyici ve
// dis izleme yoldaki istekler biterken yeni trafigi baska yere ceksin.
func SetDraining(v bool) { draining.Store(v) }

// SetReadiness, /readyz girdilerini ayarlar (main.go kurar).
func SetReadiness(r Readiness) { readiness.Store(&r) }

// ReadyHandler, kimliksiz /readyz isleyicisi. Govde YALNIZ boolean/sayi icerir;
// sir, hata metni, DSN, host adi yazilmaz.
//
// Hazir degil (503) kosullari: yonlendirme verisi hic yuklenmedi ya da MaxStale'den
// eski, varsayilan sertifikanin suresi dolmus, surec kapaniyor (SetDraining) ya da
// RequireDB iken DB erisilemiyor. RequireDB=false (edge) iken DB erisimi tek basina
// hazirligi BOZMAZ (goruntu/son basarili tablo ile hizmet surer) ama govdede gorunur.
//
// Canlilik (liveness) icin /api/v1/health kullanilir (statik): konteyner saglik
// kontrolu oraya bakar ki kisa bir DB kesintisinde konteyner yeniden baslatilmasin.
func ReadyHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			w.Header().Set("Allow", "GET, HEAD")
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		cfg := readiness.Load()
		if cfg == nil {
			cfg = &Readiness{}
		}
		maxStale := cfg.MaxStale
		if maxStale <= 0 {
			maxStale = DefaultMaxStale
		}

		dbOK := true
		if cfg.DBPing != nil {
			ctx, cancel := context.WithTimeout(r.Context(), time.Second)
			dbOK = cfg.DBPing(ctx) == nil
			cancel()
		}

		source := "db"
		dataAt := LastReloadOK()
		if ServingFromSnapshot() {
			source = "snapshot"
			dataAt = SnapshotTime()
		}
		routesOK := !dataAt.IsZero() && time.Since(dataAt) <= maxStale
		age := float64(-1)
		if !dataAt.IsZero() {
			age = time.Since(dataAt).Seconds()
		}

		certOK := true
		certDays := float64(-1)
		if t := CertNotAfter(); !t.IsZero() {
			certDays = time.Until(t).Hours() / 24
			certOK = certDays > 0
		}

		drain := draining.Load()
		ready := routesOK && certOK && !drain && (dbOK || !cfg.RequireDB)
		body := map[string]any{
			"ready": ready,
			"checks": map[string]any{
				"draining": drain,
				"db":       map[string]any{"ok": dbOK, "required": cfg.RequireDB},
				"routes":   map[string]any{"ok": routesOK, "source": source, "age_seconds": age},
				"cert":     map[string]any{"ok": certOK, "days_remaining": certDays},
				"reload":   map[string]any{"last_success_age_seconds": ageSeconds(LastReloadOK())},
			},
		}
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Cache-Control", "no-store")
		if !ready {
			w.WriteHeader(http.StatusServiceUnavailable)
		}
		if r.Method == http.MethodHead {
			return
		}
		_ = json.NewEncoder(w).Encode(body)
	})
}

func ageSeconds(t time.Time) float64 {
	if t.IsZero() {
		return -1
	}
	return time.Since(t).Seconds()
}

// --- ayri dinleyici -------------------------------------------------------------------

// Config, metrik/pprof dinleyicisi ayarlari.
type Config struct {
	Addr  string // bos = kapali
	Token string // dolu ise /metrics ve /debug/pprof icin Authorization: Bearer
	Pprof bool   // yalniz Addr ile birlikte anlamlidir
}

// ConfigFromEnv, ZORVEN_METRICS_ADDR / ZORVEN_METRICS_TOKEN / ZORVEN_PPROF okur.
func ConfigFromEnv(get func(string) string) Config {
	return Config{
		Addr:  strings.TrimSpace(get("ZORVEN_METRICS_ADDR")),
		Token: strings.TrimSpace(get("ZORVEN_METRICS_TOKEN")),
		Pprof: strings.TrimSpace(get("ZORVEN_PPROF")) == "1",
	}
}

// Handler, ayri dinleyicinin mux'ini kurar. /readyz kimliksizdir; /metrics ve
// pprof Token doluysa Bearer ister. Ana porttaki mux'a ASLA baglanmaz.
func Handler(cfg Config) http.Handler {
	mux := http.NewServeMux()
	mux.Handle("/readyz", ReadyHandler())
	mux.Handle("/metrics", requireToken(cfg.Token, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain; version=0.0.4; charset=utf-8")
		w.Header().Set("Cache-Control", "no-store")
		_ = Write(w)
	})))
	if cfg.Pprof {
		pp := http.NewServeMux()
		pp.HandleFunc("/debug/pprof/", pprof.Index)
		pp.HandleFunc("/debug/pprof/cmdline", pprof.Cmdline)
		pp.HandleFunc("/debug/pprof/profile", pprof.Profile)
		pp.HandleFunc("/debug/pprof/symbol", pprof.Symbol)
		pp.HandleFunc("/debug/pprof/trace", pprof.Trace)
		mux.Handle("/debug/pprof/", requireToken(cfg.Token, pp))
	}
	return mux
}

func requireToken(token string, next http.Handler) http.Handler {
	if token == "" {
		return next
	}
	want := []byte("Bearer " + token)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got := []byte(r.Header.Get("Authorization"))
		if subtle.ConstantTimeCompare(got, want) != 1 {
			w.Header().Set("WWW-Authenticate", `Bearer realm="zorven-metrics"`)
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// Serve, cfg.Addr bos degilse ayri dinleyiciyi arka planda baslatir; ctx iptalinde
// kapatir. Addr bos ise hicbir sey yapmaz (varsayilan KAPALI). Dinleme hatasi
// sunucuyu durdurmaz, loglanir.
//
// FAIL-CLOSED (A6 #1): loopback disi adreste token yoksa dinleyici HIC acilmaz ve
// ERROR loglanir; false doner. /debug/pprof ve /metrics kimliksiz disari acilmasin
// (pprof profili ve goroutine dokumu ic yapiyi ve yuku sizdirir). Ana sunucu
// durdurulmaz: metrik dinleyicisi opsiyoneldir, bir yazim hatasi trafigi kesmemeli.
func Serve(ctx context.Context, cfg Config, log *slog.Logger) bool {
	if cfg.Addr == "" {
		return false
	}
	if log == nil {
		log = slog.Default()
	}
	if cfg.Token == "" && !isLoopbackAddr(cfg.Addr) {
		log.Error("metrik dinleyicisi BASLATILMADI: loopback disi adres ZORVEN_METRICS_TOKEN gerektirir",
			"adres", cfg.Addr)
		return false
	}
	srv := &http.Server{
		Addr:              cfg.Addr,
		Handler:           Handler(cfg),
		ReadHeaderTimeout: 5 * time.Second,
		IdleTimeout:       60 * time.Second,
	}
	go func() {
		<-ctx.Done()
		sctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		_ = srv.Shutdown(sctx)
	}()
	go func() {
		log.Info("metrik dinleyicisi basladi", "adres", cfg.Addr, "pprof", cfg.Pprof, "token", cfg.Token != "")
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Error("metrik dinleyicisi durdu", "hata", err)
		}
	}()
	return true
}

// isLoopbackAddr, "host:port" bicimindeki adresin yalniz loopback'e baglandigini soyler.
// Bos host (":9100") TUM arayuzler demektir: loopback DEGIL.
func isLoopbackAddr(a string) bool {
	host, _, err := net.SplitHostPort(a)
	if err != nil {
		return false
	}
	if host == "localhost" {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}
