package agent

// FAZ 3 / F15 — Uzaktan ajan yapilandirmasi.
//
// GUVENLIK ILKESI — UZAKTAN AYAR YALNIZCA KISITLAR:
//
// Yerel olarak kapatilmis bir izni sunucu ACAMAZ. Kullanici --no-terminal
// dediyse, sunucudan "allow_terminal: true" gelmesi bunu geri acmaz. Aksi
// halde sunucuyu ele geciren biri, kullanicinin kasitla kapattigi uzak kabugu
// yeniden acabilirdi. Tersi serbesttir: sunucu daha fazla kisitlayabilir.
//
// Bu yuzden yerel bayraklar "tavan", uzak ayarlar "tavani asagi cekme" olarak
// modellenir.

import (
	"log/slog"
	"sync"
	"time"

	"github.com/tkodcumpeg4/zorven/shared/protocol"
)

// localCaps, yerel bayraklarla belirlenen TAVAN yetenekler.
// Agent.Run baslarken bir kez doldurulur ve bir daha degismez.
type localCaps struct {
	terminal bool
	screen   bool
}

// effectiveSettings, yerel tavan ile uzak ayarin birlesimi.
type effectiveSettings struct {
	allowTerminal bool
	allowScreen   bool
	autoUpdate    bool
	metricsEvery  time.Duration
	maxBackoff    time.Duration
	logLevel      string
}

// settingsState, es zamanli okunan/guncellenen etkin ayarlar.
type settingsState struct {
	mu   sync.RWMutex
	caps localCaps
	eff  effectiveSettings

	// levelVar doluysa uzak log_level CANLI uygulanir (cagiran logger'i bu
	// LevelVar ile kurmus olmali). nil ise log seviyesi degistirilmez.
	levelVar *slog.LevelVar

	// Metrik onbellegi: metrics_interval_sec'ten sik toplama yapilmaz.
	lastMetrics   any
	lastMetricsAt time.Time
}

func newSettingsState(caps localCaps, autoUpdate bool) *settingsState {
	return &settingsState{
		caps: caps,
		eff: effectiveSettings{
			allowTerminal: caps.terminal,
			allowScreen:   caps.screen,
			autoUpdate:    autoUpdate,
		},
	}
}

// apply, sunucudan gelen ayarlari YEREL TAVANLA sinirlayarak uygular.
// s nil ise hicbir sey degismez: "ayar gonderilmedi" ile "sifirla" ayri seydir.
func (st *settingsState) apply(s *protocol.AgentSettings, log *slog.Logger) {
	if st == nil || s == nil {
		return
	}
	st.mu.Lock()
	defer st.mu.Unlock()

	if s.AllowTerminal != nil {
		// VE (AND): sunucu yalnizca kisitlayabilir.
		want := *s.AllowTerminal && st.caps.terminal
		if want != st.eff.allowTerminal {
			st.eff.allowTerminal = want
			log.Info("uzak ayar: terminal izni", "izin", want)
		}
		if *s.AllowTerminal && !st.caps.terminal {
			log.Warn("uzak ayar terminal izni acmak istedi ama yerel olarak kapali; YOK SAYILDI")
		}
	}
	if s.AllowScreen != nil {
		want := *s.AllowScreen && st.caps.screen
		if want != st.eff.allowScreen {
			st.eff.allowScreen = want
			log.Info("uzak ayar: ekran izni", "izin", want)
		}
		if *s.AllowScreen && !st.caps.screen {
			log.Warn("uzak ayar ekran izni acmak istedi ama yerel olarak kapali; YOK SAYILDI")
		}
	}
	if s.AutoUpdate != nil {
		st.eff.autoUpdate = *s.AutoUpdate
	}
	if s.MetricsIntervalSec > 0 {
		st.eff.metricsEvery = time.Duration(s.MetricsIntervalSec) * time.Second
	}
	if s.ReconnectMaxBackoffSec > 0 {
		st.eff.maxBackoff = time.Duration(s.ReconnectMaxBackoffSec) * time.Second
	}
	if s.LogLevel != "" && s.LogLevel != st.eff.logLevel {
		st.eff.logLevel = s.LogLevel
		if st.levelVar != nil {
			var lvl slog.Level
			if err := lvl.UnmarshalText([]byte(s.LogLevel)); err == nil {
				st.levelVar.Set(lvl)
				log.Info("uzak ayar: log seviyesi", "seviye", s.LogLevel)
			}
		}
	}
}

// metricsEveryOr, uzak metrik araligi verilmediyse varsayilani doner.
func (st *settingsState) metricsEveryOr(def time.Duration) time.Duration {
	if st == nil {
		return def
	}
	st.mu.RLock()
	defer st.mu.RUnlock()
	if st.eff.metricsEvery <= 0 {
		return def
	}
	return st.eff.metricsEvery
}

// cachedMetrics, metrics_interval_sec ayarlandiysa son toplanan metrikleri
// aralik dolana kadar yeniden kullanir; collect yalnizca gerekince cagrilir.
// Aralik ayarlanmadiysa her cagrida toplar (eski davranis).
func cachedMetrics[T any](st *settingsState, now time.Time, collect func() T) T {
	every := st.metricsEveryOr(0)
	if st == nil || every <= 0 {
		return collect()
	}
	st.mu.Lock()
	defer st.mu.Unlock()
	if m, ok := st.lastMetrics.(T); ok && now.Sub(st.lastMetricsAt) < every {
		return m
	}
	m := collect()
	st.lastMetrics, st.lastMetricsAt = m, now
	return m
}

func (st *settingsState) terminalAllowed() bool {
	if st == nil {
		return false
	}
	st.mu.RLock()
	defer st.mu.RUnlock()
	return st.eff.allowTerminal
}

func (st *settingsState) screenAllowed() bool {
	if st == nil {
		return false
	}
	st.mu.RLock()
	defer st.mu.RUnlock()
	return st.eff.allowScreen
}

func (st *settingsState) autoUpdateEnabled() bool {
	if st == nil {
		return false
	}
	st.mu.RLock()
	defer st.mu.RUnlock()
	return st.eff.autoUpdate
}

// maxBackoffOr, uzak ayar verilmediyse verilen varsayilani doner.
func (st *settingsState) maxBackoffOr(def time.Duration) time.Duration {
	if st == nil {
		return def
	}
	st.mu.RLock()
	defer st.mu.RUnlock()
	if st.eff.maxBackoff <= 0 {
		return def
	}
	return st.eff.maxBackoff
}
