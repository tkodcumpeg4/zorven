package obs

import (
	"bytes"
	"context"
	"fmt"
	"log/slog"
	"runtime"
	"sort"
	"sync"
	"sync/atomic"
)

// Olay sayaclari: panik, log seviyesi, ajan baglan/kop, surum bilgisi (A5 #6, A1 #10).

// --- panik ---------------------------------------------------------------------

// panicMaxWhere: "where" etiketi kodda sabit adlardan gelir (SafeGo adlari, "api");
// yine de beklenmedik bir kaynak kardinaliteyi patlatmasin diye ust sinir.
const panicMaxWhere = 128

var (
	panicMu sync.Mutex
	panics  = map[string]*atomic.Uint64{}
)

// CountPanic, kurtarilan bir paniği yerine gore sayar.
func CountPanic(where string) {
	panicMu.Lock()
	c := panics[where]
	if c == nil {
		if len(panics) >= panicMaxWhere {
			where = "other"
			c = panics[where]
		}
		if c == nil {
			c = &atomic.Uint64{}
			panics[where] = c
		}
	}
	panicMu.Unlock()
	c.Add(1)
}

// Panics, yerin sayaci (test/teshis).
func Panics(where string) uint64 {
	panicMu.Lock()
	defer panicMu.Unlock()
	if c := panics[where]; c != nil {
		return c.Load()
	}
	return 0
}

// --- log seviyesi ------------------------------------------------------------------

var logLevelNames = [...]string{"debug", "info", "warn", "error"}

var logCounts [len(logLevelNames)]atomic.Uint64

func logLevelIndex(l slog.Level) int {
	switch {
	case l >= slog.LevelError:
		return 3
	case l >= slog.LevelWarn:
		return 2
	case l >= slog.LevelInfo:
		return 1
	}
	return 0
}

// LogMessages, seviyenin sayaci (test/teshis).
func LogMessages(level string) uint64 {
	for i, n := range logLevelNames {
		if n == level {
			return logCounts[i].Load()
		}
	}
	return 0
}

// CountingHandler, yazilan her log kaydini seviyesine gore sayar
// (zorven_log_messages_total{level}). ERROR artisi alarm icin kullanilir; seviye
// filtresinden gecmeyen kayitlar sayilmaz (Handle yalniz etkin kayitlarda cagrilir).
func CountingHandler(h slog.Handler) slog.Handler { return countingHandler{h} }

type countingHandler struct{ inner slog.Handler }

func (c countingHandler) Enabled(ctx context.Context, l slog.Level) bool {
	return c.inner.Enabled(ctx, l)
}

func (c countingHandler) Handle(ctx context.Context, r slog.Record) error {
	logCounts[logLevelIndex(r.Level)].Add(1)
	return c.inner.Handle(ctx, r)
}

func (c countingHandler) WithAttrs(as []slog.Attr) slog.Handler {
	return countingHandler{c.inner.WithAttrs(as)}
}

func (c countingHandler) WithGroup(name string) slog.Handler {
	return countingHandler{c.inner.WithGroup(name)}
}

// --- ajan oturumlari --------------------------------------------------------------

var (
	agentConnects    atomic.Uint64
	agentDisconnects atomic.Uint64
)

// AgentConnected / AgentDisconnected, bu dugumde el sikismasi tamamlanan ve biten
// ajan oturumlarini sayar. Anlik sayi zorven_connected_clients gauge'udur; bu
// sayaclar dalgalanmayi (flapping) gosterir.
func AgentConnected()    { agentConnects.Add(1) }
func AgentDisconnected() { agentDisconnects.Add(1) }

// --- surum ---------------------------------------------------------------------

var buildVersion atomic.Pointer[string]

// SetBuildInfo, zorven_build_info{version,go_version} icin surumu kaydeder.
func SetBuildInfo(version string) { buildVersion.Store(&version) }

func writeEvents(b *bytes.Buffer) {
	head(b, "zorven_panics_total", "Kurtarilan paniklerin yere gore sayisi (arka plan isi adi ya da api).", "counter")
	panicMu.Lock()
	names := make([]string, 0, len(panics))
	for k := range panics {
		names = append(names, k)
	}
	sort.Strings(names)
	for _, n := range names {
		fmt.Fprintf(b, "zorven_panics_total{where=%q} %d\n", n, panics[n].Load())
	}
	panicMu.Unlock()

	head(b, "zorven_log_messages_total", "Yazilan log kayitlarinin seviyeye gore sayisi.", "counter")
	for i, n := range logLevelNames {
		fmt.Fprintf(b, "zorven_log_messages_total{level=%q} %d\n", n, logCounts[i].Load())
	}

	head(b, "zorven_agent_connects_total", "Bu dugumde tamamlanan ajan el sikismalari.", "counter")
	fmt.Fprintf(b, "zorven_agent_connects_total %d\n", agentConnects.Load())
	head(b, "zorven_agent_disconnects_total", "Bu dugumde biten ajan oturumlari.", "counter")
	fmt.Fprintf(b, "zorven_agent_disconnects_total %d\n", agentDisconnects.Load())

	v := "unknown"
	if p := buildVersion.Load(); p != nil && *p != "" {
		v = *p
	}
	head(b, "zorven_build_info", "Sunucu surumu ve Go surumu (deger her zaman 1).", "gauge")
	fmt.Fprintf(b, "zorven_build_info{version=%q,go_version=%q} 1\n", v, runtime.Version())
}
