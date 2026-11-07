package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	clientcfg "github.com/tkodcumpeg4/zorven/client/config"
)

// CLI ve masaustu AYNI config.json'u kendi yapilariyla yazar. Her iki yazici da
// bilmedigi alanlari korumali: masaustu kaydi CLI'nin enable_terminal/enable_screen'ini,
// CLI kaydi masaustunun auto_connect/no_terminal/hide_to_tray/token_enc... alanlarini
// (token_enc silinirse masaustu oturumu duser) ve ikisinin de bilmedigi gelecekteki
// alanlari silmemeli.

const futureFields = `"gelecek_alan": {"ic": [1, 2.50, null, {"x": "y"}], "buyuk": 12345678901234567890}`

// rawFields, ayar dosyasini anahtar -> sikistirilmis ham deger sozlugu olarak okur.
func rawFields(t *testing.T) map[string]string {
	t.Helper()
	path, _ := configPath()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]json.RawMessage
	if err := json.Unmarshal(data, &m); err != nil {
		t.Fatalf("ayar dosyasi gecerli JSON degil: %v\n%s", err, data)
	}
	out := map[string]string{}
	for k, v := range m {
		var b bytes.Buffer
		if err := json.Compact(&b, v); err != nil {
			t.Fatal(err)
		}
		out[k] = b.String()
	}
	return out
}

func wantFields(t *testing.T, f map[string]string, want map[string]string) {
	t.Helper()
	for k, w := range want {
		if f[k] != w {
			t.Errorf("%s = %q, beklenen %q", k, f[k], w)
		}
	}
}

const futureCompact = `{"ic":[1,2.50,null,{"x":"y"}],"buyuk":12345678901234567890}`

// Masaustu kaydi CLI alanlarini ve bilinmeyen gelecek alanini korur.
func TestSaveConfigPreservesCLIFieldsAndFutureFields(t *testing.T) {
	useTempConfigDir(t)
	withProtector(t, nil)
	path, _ := configPath()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	body := `{
  "server_addr": "zorven.app:443",
  "token": "zrv_live_cli",
  "enable_terminal": true,
  "enable_screen": true,
  "enable_deploy": true,
  "language": "ja",
  ` + futureFields + `
}`
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, notice, err := loadConfigNotice()
	if err != nil || notice != nil {
		t.Fatalf("yukleme: %v %v", err, notice)
	}
	cfg.AutoConnect, cfg.NoScreen, cfg.Theme = true, true, "dark"
	if err := saveConfig(cfg); err != nil {
		t.Fatal(err)
	}
	f := rawFields(t)
	wantFields(t, f, map[string]string{
		"enable_terminal": "true", "enable_screen": "true", // CLI'nin alanlari
		"gelecek_alan": futureCompact,                                  // ikisinin de bilmedigi alan
		"auto_connect": "true", "no_screen": "true", "theme": `"dark"`, // masaustunun yazdiklari
		"enable_deploy": "true", "language": `"ja"`, "token": `"zrv_live_cli"`, // ortak alanlar
	})
}

// CLI'nin yazimi (config set/unset, authtoken, ayarlar TUI'si SaveFile uzerinden) masaustu
// alanlarini ve sifreli tokeni korur; masaustu sonra oturumu ve ayarlari aynen okur.
func TestCLIWritesPreserveDesktopFields(t *testing.T) {
	useTempConfigDir(t)
	withProtector(t, fakeProtector{})
	cfg := defaultConfig()
	cfg.Token = "zrv_live_desktop"
	cfg.AutoConnect, cfg.NoScreen, cfg.NoTerminal = true, true, true
	cfg.HideToTray, cfg.NotifyDisabled, cfg.Theme = false, true, "dark"
	cfg.TerminalPlainZsh, cfg.LastPort, cfg.RecentPorts = true, 4000, []int{4000, 4001}
	if err := saveConfig(cfg); err != nil {
		t.Fatal(err)
	}
	f := rawFields(t)
	if !strings.HasPrefix(f["token_enc"], `"dpapi:`) || f["token"] != `""` {
		t.Fatalf("token diskte sifreli olmali: token=%s token_enc=%s", f["token"], f["token_enc"])
	}
	sealed := f["token_enc"]
	path, _ := configPath()

	// CLI: zorven config set enable_terminal true / enable_deploy true, sonra unset.
	c, err := clientcfg.LoadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := c.Set("enable_terminal", "true"); err != nil {
		t.Fatal(err)
	}
	if err := c.Set("enable_deploy", "true"); err != nil {
		t.Fatal(err)
	}
	if err := clientcfg.SaveFile(path, c); err != nil {
		t.Fatal(err)
	}
	f = rawFields(t)
	wantFields(t, f, map[string]string{"enable_terminal": "true", "enable_deploy": "true", "token_enc": sealed})
	if err := c.Unset("enable_deploy"); err != nil {
		t.Fatal(err)
	}
	if err := clientcfg.SaveFile(path, c); err != nil {
		t.Fatal(err)
	}
	// CLI: zorven config set-server (config.SetServer ayni dosyayi okuyup yazar).
	if err := clientcfg.SetServer("srv.example:443"); err != nil {
		t.Fatal(err)
	}

	f = rawFields(t)
	wantFields(t, f, map[string]string{
		"token_enc": sealed, "auto_connect": "true", "no_terminal": "true", "no_screen": "true",
		"hide_to_tray": "false", "notify_disabled": "true", "theme": `"dark"`, "terminal_plain_zsh": "true",
		"last_port": "4000", "recent_ports": "[4000,4001]", "enable_terminal": "true",
		"server_addr": `"srv.example:443"`,
	})
	if _, ok := f["enable_deploy"]; ok {
		t.Errorf("CLI unset enable_deploy anahtari silmeli: %v", f)
	}

	// Masaustu: oturum dusmedi (token cozulur), tum ayarlar ayni.
	got, notice, err := loadConfigNotice()
	if err != nil || notice != nil {
		t.Fatalf("masaustu yuklemesi: %v %v", err, notice)
	}
	want := cfg
	want.ServerAddr = "srv.example:443"
	want.Token = "zrv_live_desktop"
	want.EnableDeploy = false
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("masaustu ayarlari bozuldu:\n got %+v\nwant %+v", got, want)
	}
}

// Iki yazici art arda calisinca da ikisinin bilmedigi alan hic kaybolmaz.
func TestFutureFieldSurvivesBothWriters(t *testing.T) {
	useTempConfigDir(t)
	withProtector(t, nil)
	path, _ := configPath()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(`{"server_addr":"zorven.app:443","token":"zrv_live_x",`+futureFields+`}`), 0o600); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		cfg, _, err := loadConfigNotice()
		if err != nil {
			t.Fatal(err)
		}
		cfg.LastPort = 5000 + i
		if err := saveConfig(cfg); err != nil {
			t.Fatal(err)
		}
		c, err := clientcfg.LoadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		c.Language = []string{"en", "zh"}[i]
		if err := clientcfg.SaveFile(path, c); err != nil {
			t.Fatal(err)
		}
		if f := rawFields(t); f["gelecek_alan"] != futureCompact {
			t.Fatalf("tur %d: gelecek alan bozuldu: %s", i, f["gelecek_alan"])
		}
	}
	f := rawFields(t)
	wantFields(t, f, map[string]string{"last_port": "5001", "language": `"zh"`, "token": `"zrv_live_x"`})
}

// Cikis (token silme) sifreli tokeni de siler: korunan "bilinmeyen" alanlar arasinda
// token_enc dirilmez; sonraki CLI kaydi da geri getirmez.
func TestLogoutRemovesSealedTokenAndStaysRemoved(t *testing.T) {
	useTempConfigDir(t)
	withProtector(t, fakeProtector{})
	cfg := defaultConfig()
	cfg.Token = "zrv_live_x"
	if err := saveConfig(cfg); err != nil {
		t.Fatal(err)
	}
	if _, ok := rawFields(t)["token_enc"]; !ok {
		t.Fatal("token_enc yazilmaliydi")
	}
	cfg.Token = ""
	if err := saveConfig(cfg); err != nil {
		t.Fatal(err)
	}
	if f := rawFields(t); f["token"] != `""` {
		t.Fatalf("token bos olmali: %v", f)
	} else if _, ok := f["token_enc"]; ok {
		t.Fatalf("cikis sonrasi token_enc kalmamali: %v", f)
	}
	path, _ := configPath()
	c, _ := clientcfg.LoadFile(path)
	c.Language = "en"
	if err := clientcfg.SaveFile(path, c); err != nil {
		t.Fatal(err)
	}
	if _, ok := rawFields(t)["token_enc"]; ok {
		t.Fatal("CLI kaydi silinmis token_enc'i geri getirmemeli")
	}
}

// Kaydetme sirasinda dosya bozuksa (baska arac bozdu): kayit yine yapilir, eski icerik
// yan yana yedeklenir (yuklemedeki politikayla ayni).
func TestSaveConfigOverCorruptFileKeepsBackup(t *testing.T) {
	useTempConfigDir(t)
	withProtector(t, nil)
	path, _ := configPath()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	bad := []byte(`{"token": "zrv_live_kurtar", "enable_terminal": tr`)
	if err := os.WriteFile(path, bad, 0o600); err != nil {
		t.Fatal(err)
	}
	cfg := defaultConfig()
	cfg.Token = "zrv_live_yeni"
	if err := saveConfig(cfg); err != nil {
		t.Fatalf("bozuk dosya kaydi engellememeli: %v", err)
	}
	if kept, err := os.ReadFile(path + ".bak"); err != nil || !bytes.Equal(kept, bad) {
		t.Fatalf("yedek: %v %q", err, kept)
	}
	if got, err := loadConfig(); err != nil || got.Token != "zrv_live_yeni" {
		t.Fatalf("yeni dosya gecerli olmali: %+v %v", got, err)
	}
}

// Dosya yoksa ya da bossa kayit eski bicimle ayni icerigi yazar.
func TestSaveConfigMissingAndEmptyFileFormat(t *testing.T) {
	useTempConfigDir(t)
	withProtector(t, nil)
	cfg := defaultConfig()
	cfg.Token = "zrv_live_x"
	want, _ := json.MarshalIndent(sealToken(cfg), "", "  ")
	path, _ := configPath()

	if err := saveConfig(cfg); err != nil {
		t.Fatal(err)
	}
	if got, _ := os.ReadFile(path); !bytes.Equal(got, want) {
		t.Fatalf("yeni dosya:\n got %s\nwant %s", got, want)
	}
	if err := os.WriteFile(path, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := saveConfig(cfg); err != nil {
		t.Fatal(err)
	}
	if got, _ := os.ReadFile(path); !bytes.Equal(got, want) {
		t.Fatalf("bos dosya:\n got %s\nwant %s", got, want)
	}
	if _, err := os.Stat(path + ".bak"); err == nil {
		t.Error("bos dosya icin yedek acilmamali")
	}
}
