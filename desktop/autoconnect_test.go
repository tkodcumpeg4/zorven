package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestShouldAutoConnect(t *testing.T) {
	cases := []struct {
		name  string
		auto  bool
		token string
		want  bool
	}{
		{"varsayilan kapali, token var", false, "zrv_live_x", false},
		{"acik, token yok", true, "", false},
		{"acik, token yalniz bosluk", true, "   ", false},
		{"acik, token var", true, "zrv_live_x", true},
		{"kapali, token yok", false, "", false},
	}
	for _, c := range cases {
		a := &App{cfg: defaultConfig()}
		a.cfg.AutoConnect, a.cfg.Token = c.auto, c.token
		if got := a.shouldAutoConnect(); got != c.want {
			t.Errorf("%s: %v != %v", c.name, got, c.want)
		}
	}
	// Mevcut davranis korunur: yeni kurulumda ayar kapali, token olsa da kendiliginden baglanmaz.
	a := &App{cfg: defaultConfig()}
	a.cfg.Token = "zrv_live_x"
	if a.shouldAutoConnect() || a.GetAutoConnect() {
		t.Error("otomatik baglanma varsayilan KAPALI olmali")
	}
}

func TestSetAutoConnectPersists(t *testing.T) {
	useTempConfigDir(t)
	withProtector(t, nil)
	a := &App{cfg: defaultConfig()}

	if msg := a.SetAutoConnect(true); msg != "" {
		t.Fatal(msg)
	}
	if !a.GetAutoConnect() {
		t.Error("GetAutoConnect acik olmali")
	}
	path, _ := configPath()
	raw, err := os.ReadFile(path)
	if err != nil || !strings.Contains(string(raw), `"auto_connect": true`) {
		t.Fatalf("diske yazilmadi: %v\n%s", err, raw)
	}
	// Arayuzun GetConfig'i de gorur (ayarlar sayfasi buradan okur).
	if !a.GetConfig().AutoConnect {
		t.Error("GetConfig auto_connect'i yansitmali")
	}
	// Yeniden baslatma: yuklenen ayar acik.
	cfg, err := loadConfig()
	if err != nil || !cfg.AutoConnect {
		t.Fatalf("yeniden yuklenemedi: %+v %v", cfg, err)
	}
	if msg := a.SetAutoConnect(false); msg != "" || a.GetAutoConnect() {
		t.Fatalf("kapatilamadi: %q", msg)
	}
	if cfg, _ = loadConfig(); cfg.AutoConnect {
		t.Error("kapali yazilmali")
	}
}

func TestSetAutoConnectRollsBackOnSaveFailure(t *testing.T) {
	dir := useTempConfigDir(t)
	withProtector(t, nil)
	a := &App{cfg: defaultConfig()}

	blocker := filepath.Join(dir, "dosya")
	if err := os.WriteFile(blocker, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("XDG_CONFIG_HOME", blocker)
	t.Setenv("APPDATA", blocker)
	t.Setenv("HOME", blocker)

	msg := a.SetAutoConnect(true)
	if msg == "" {
		t.Fatal("yazma hatasi bildirilmedi")
	}
	if a.GetAutoConnect() {
		t.Error("kaydedilemeyen ayar bellekte acik kalmamali")
	}
}

// Ayarlar sayfasindaki diger anahtarlar otomatik baglanmayi bozmaz.
func TestAutoConnectSurvivesOtherSettingChanges(t *testing.T) {
	useTempConfigDir(t)
	withProtector(t, nil)
	a := &App{cfg: defaultConfig(), log: testLogger()}
	a.SetAutoConnect(true)
	a.SetNotifications(false)
	a.SetTheme("dark")
	if msg := a.SaveAdvanced("zorven.app:443", ""); msg != "" {
		t.Fatal(msg)
	}
	if msg := a.UpdateBehavior(false, false, true); msg != "" {
		t.Fatal(msg)
	}
	if !a.GetAutoConnect() {
		t.Error("otomatik baglanma baska ayar degisince kapandi")
	}
	if cfg, _ := loadConfig(); !cfg.AutoConnect {
		t.Error("diskteki ayar kapandi")
	}
}
