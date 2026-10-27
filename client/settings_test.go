package main

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/tkodcumpeg4/zorven/client/config"
	"github.com/tkodcumpeg4/zorven/client/i18n"
	"github.com/tkodcumpeg4/zorven/client/internal/tui"
)

func testSettingsModel(t *testing.T) (*settingsModel, string, string) {
	t.Helper()
	dir := t.TempDir()
	up, sp := filepath.Join(dir, "user.json"), filepath.Join(dir, "system.json")
	m := &settingsModel{
		version:    "test",
		userPath:   func() (string, error) { return up, nil },
		systemPath: func() string { return sp },
		probe:      func() string { return "çalışmıyor" },
	}
	m.init()
	return m, up, sp
}

func keys(m *settingsModel, ks ...tui.Key) {
	for _, k := range ks {
		m.handle(k)
	}
}

func typeText(s string) tui.Key { return tui.Key{Kind: tui.KeyRune, Text: s} }

var (
	kDown  = tui.Key{Kind: tui.KeyDown}
	kUp    = tui.Key{Kind: tui.KeyUp}
	kEnter = tui.Key{Kind: tui.KeyEnter}
	kEsc   = tui.Key{Kind: tui.KeyEsc}
)

func TestSettingsToggleSavesImmediately(t *testing.T) {
	m, up, _ := testSettingsModel(t)
	// Kapsam -> Dil -> Sunucu -> Token -> Insecure -> CA -> Terminal
	keys(m, kDown, kDown, kDown, kDown, kDown, kDown, kEnter)
	cfg, err := config.LoadFile(up)
	if err != nil || !cfg.EnableTerminal {
		t.Fatalf("terminal izni kaydedilmedi: %+v %v", cfg, err)
	}
	if m.done {
		t.Fatal("Enter arayuzu kapatmamali")
	}
}

func TestSettingsEditServerAndCancel(t *testing.T) {
	m, up, _ := testSettingsModel(t)
	keys(m, kDown, kDown, kEnter, tui.Key{Kind: tui.KeyCtrlU}, typeText("ornek.test:8443"), kEnter)
	if cfg, _ := config.LoadFile(up); cfg.ServerAddr != "ornek.test:8443" {
		t.Fatalf("sunucu = %q", cfg.ServerAddr)
	}
	// Esc duzenlemeyi iptal eder, dosya degismez, arayuz acik kalir.
	keys(m, kEnter, typeText("x"), kEsc)
	if cfg, _ := config.LoadFile(up); cfg.ServerAddr != "ornek.test:8443" || m.done || m.editing {
		t.Fatalf("iptal bozuk: %q done=%v editing=%v", cfg.ServerAddr, m.done, m.editing)
	}
	keys(m, kEsc)
	if !m.done {
		t.Fatal("ana ekranda Esc cikmali")
	}
}

func TestSettingsTokenValidationAndMask(t *testing.T) {
	m, up, _ := testSettingsModel(t)
	keys(m, kDown, kDown, kDown, kEnter, typeText("yanlis"), kEnter)
	if !m.msgErr || !m.editing {
		t.Fatal("gecersiz token reddedilmeli ve duzenleme acik kalmali")
	}
	tok := "zrv_live_abcdefghijklmnop"
	keys(m, tui.Key{Kind: tui.KeyCtrlU}, typeText(tok), kEnter)
	if cfg, _ := config.LoadFile(up); cfg.Token != tok {
		t.Fatalf("token kaydedilmedi")
	}
	if strings.Contains(strings.Join(m.lines(), "\n"), tok) {
		t.Fatal("token ekranda acik gorunmemeli")
	}
}

func TestSettingsScopeAndWrapAndUpdate(t *testing.T) {
	m, _, sp := testSettingsModel(t)
	keys(m, kEnter) // kapsam -> sistem
	if !m.system || m.path != sp {
		t.Fatalf("kapsam degismedi: %v %s", m.system, m.path)
	}
	keys(m, kUp) // basa sarar: Cik
	if m.cursor != siQuit {
		t.Fatalf("imlec = %d", m.cursor)
	}
	keys(m, kUp, kEnter) // Guncellemeleri denetle: arayuz ACIK kalir, arka planda baslar
	if m.done || !m.startUpdate || !m.updating {
		t.Fatal("guncelleme arayuz icinde baslamadi")
	}
	m.startUpdate = false
	keys(m, kEnter) // ikinci basis yeni denetim baslatmaz
	if m.startUpdate {
		t.Fatal("suren denetim varken ikinci denetim baslamamali")
	}
	m.updateDone([]string{"Zaten guncel"}, nil)
	if m.updating || m.msgErr {
		t.Fatal("sonuc yansimadi")
	}
}

func TestSettingsPrefersSystemWhenOnlySystemHasToken(t *testing.T) {
	dir := t.TempDir()
	sp := filepath.Join(dir, "system.json")
	if err := config.SaveFile(sp, config.Config{ServerAddr: "a:1", Token: "zrv_live_x"}); err != nil {
		t.Fatal(err)
	}
	m := &settingsModel{
		userPath:   func() (string, error) { return filepath.Join(dir, "user.json"), nil },
		systemPath: func() string { return sp },
		probe:      func() string { return "" },
	}
	m.init()
	if !m.system {
		t.Fatal("yalniz sistemde token varken sistem kapsami secilmeli")
	}
}

func TestSettingsLanguageCycleSaves(t *testing.T) {
	prev := i18n.Lang()
	defer i18n.Set(prev)
	i18n.Set(i18n.TR)
	m, up, _ := testSettingsModel(t)
	keys(m, kDown, kEnter) // tr -> en
	if cfg, _ := config.LoadFile(up); cfg.Language != "en" || i18n.Lang() != "en" {
		t.Fatalf("dil = %q etkin %q", cfg.Language, i18n.Lang())
	}
	keys(m, tui.Key{Kind: tui.KeyLeft}) // en -> tr
	if cfg, _ := config.LoadFile(up); cfg.Language != "tr" {
		t.Fatalf("dil = %q", cfg.Language)
	}
}
