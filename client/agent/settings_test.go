package agent

import (
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/tkodcumpeg4/zorven/shared/protocol"
)

func quiet() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }
func boolp(b bool) *bool  { return &b }

// EN KRITIK DAVRANIS: yerelde kapatilmis bir izni sunucu ACAMAZ.
// Bu kirilirsa, sunucuyu ele geciren biri kullanicinin kasitla kapattigi
// uzak kabugu geri acabilir.
func TestRemoteCannotEnableLocallyDisabledPermissions(t *testing.T) {
	st := newSettingsState(localCaps{terminal: false, screen: false}, true)
	st.apply(&protocol.AgentSettings{
		AllowTerminal: boolp(true),
		AllowScreen:   boolp(true),
	}, quiet())

	if st.terminalAllowed() {
		t.Error("yerelde kapali terminal izni uzaktan acildi")
	}
	if st.screenAllowed() {
		t.Error("yerelde kapali ekran izni uzaktan acildi")
	}
}

// Tersi SERBEST: sunucu daha fazla kisitlayabilir.
func TestRemoteCanRestrictLocallyEnabledPermissions(t *testing.T) {
	st := newSettingsState(localCaps{terminal: true, screen: true}, true)
	if !st.terminalAllowed() || !st.screenAllowed() {
		t.Fatal("yerel tavan basta acik olmaliydi")
	}
	st.apply(&protocol.AgentSettings{
		AllowTerminal: boolp(false),
		AllowScreen:   boolp(false),
	}, quiet())

	if st.terminalAllowed() {
		t.Error("sunucu terminal iznini kisitlayamadi")
	}
	if st.screenAllowed() {
		t.Error("sunucu ekran iznini kisitlayamadi")
	}
}

// nil ayar = "sunucu bir sey soylemiyor"; mevcut durum KORUNUR.
func TestNilSettingsChangeNothing(t *testing.T) {
	st := newSettingsState(localCaps{terminal: true, screen: true}, true)
	st.apply(nil, quiet())
	if !st.terminalAllowed() || !st.screenAllowed() || !st.autoUpdateEnabled() {
		t.Error("nil ayar mevcut durumu degistirdi")
	}
}

// Alan verilmediyse (nil isaretci) o ayar degismez.
func TestUnsetFieldsAreUntouched(t *testing.T) {
	st := newSettingsState(localCaps{terminal: true, screen: true}, true)
	st.apply(&protocol.AgentSettings{AllowTerminal: boolp(false)}, quiet())
	if st.terminalAllowed() {
		t.Error("terminal kapatilmaliydi")
	}
	if !st.screenAllowed() {
		t.Error("ekran izni verilmemisti, degismemeliydi")
	}
}

func TestAutoUpdateToggle(t *testing.T) {
	st := newSettingsState(localCaps{}, true)
	st.apply(&protocol.AgentSettings{AutoUpdate: boolp(false)}, quiet())
	if st.autoUpdateEnabled() {
		t.Error("oto-guncelleme kapatilamadi")
	}
	st.apply(&protocol.AgentSettings{AutoUpdate: boolp(true)}, quiet())
	if !st.autoUpdateEnabled() {
		t.Error("oto-guncelleme yeniden acilamadi")
	}
}

// Yerelde --no-auto-update verilmisse baslangic durumu kapali olmali.
func TestLocalNoAutoUpdateStartsDisabled(t *testing.T) {
	st := newSettingsState(localCaps{}, false)
	if st.autoUpdateEnabled() {
		t.Error("yerel --no-auto-update yok sayildi")
	}
}

func TestMaxBackoffOverrideAndFallback(t *testing.T) {
	st := newSettingsState(localCaps{}, true)
	if got := st.maxBackoffOr(60 * time.Second); got != 60*time.Second {
		t.Errorf("ayar yokken varsayilan donmeliydi: %s", got)
	}
	st.apply(&protocol.AgentSettings{ReconnectMaxBackoffSec: 10}, quiet())
	if got := st.maxBackoffOr(60 * time.Second); got != 10*time.Second {
		t.Errorf("uzak backoff uygulanmadi: %s", got)
	}
	// 0 "degistirme" demek: onceki deger korunmali.
	st.apply(&protocol.AgentSettings{ReconnectMaxBackoffSec: 0}, quiet())
	if got := st.maxBackoffOr(60 * time.Second); got != 10*time.Second {
		t.Errorf("0 degeri mevcut ayari ezdi: %s", got)
	}
}

// nil state guvenli olmali: ayar kurulmadan once gelen bir mesaj panik etmesin.
func TestNilStateIsSafe(t *testing.T) {
	var st *settingsState
	st.apply(&protocol.AgentSettings{AllowTerminal: boolp(true)}, quiet())
	if st.terminalAllowed() || st.screenAllowed() || st.autoUpdateEnabled() {
		t.Error("nil state izin verdi")
	}
	if got := st.maxBackoffOr(5 * time.Second); got != 5*time.Second {
		t.Errorf("nil state varsayilani dondurmedi: %s", got)
	}
	if v := cachedMetrics(st, time.Now(), func() int { return 7 }); v != 7 {
		t.Errorf("nil state metrik toplamadi: %d", v)
	}
}

// F15: panelden gelen log_level, LevelVar verildiyse CANLI uygulanir.
func TestRemoteLogLevelApplied(t *testing.T) {
	st := newSettingsState(localCaps{}, true)
	lv := new(slog.LevelVar)
	lv.Set(slog.LevelWarn)
	st.levelVar = lv
	st.apply(&protocol.AgentSettings{LogLevel: "debug"}, quiet())
	if lv.Level() != slog.LevelDebug {
		t.Errorf("log seviyesi = %v, beklenen debug", lv.Level())
	}
	// Gecersiz seviye mevcut seviyeyi bozmaz.
	st.apply(&protocol.AgentSettings{LogLevel: "bozuk"}, quiet())
	if lv.Level() != slog.LevelDebug {
		t.Errorf("gecersiz seviye uygulandi: %v", lv.Level())
	}
}

// F15: metrics_interval_sec ayarliysa aralik dolmadan yeniden toplanmaz.
func TestCachedMetricsRespectsInterval(t *testing.T) {
	st := newSettingsState(localCaps{}, true)
	n := 0
	collect := func() int { n++; return n }
	now := time.Now()
	// Aralik yok: her cagri toplar (eski davranis).
	cachedMetrics(st, now, collect)
	cachedMetrics(st, now, collect)
	if n != 2 {
		t.Fatalf("aralik yokken toplama sayisi = %d, beklenen 2", n)
	}
	st.apply(&protocol.AgentSettings{MetricsIntervalSec: 30}, quiet())
	if v := cachedMetrics(st, now, collect); v != 3 {
		t.Fatalf("ilk toplama = %d", v)
	}
	if v := cachedMetrics(st, now.Add(10*time.Second), collect); v != 3 {
		t.Errorf("aralik dolmadan yeniden toplandi: %d", v)
	}
	if v := cachedMetrics(st, now.Add(31*time.Second), collect); v != 4 {
		t.Errorf("aralik dolunca toplanmadi: %d", v)
	}
}
