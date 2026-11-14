package main

import (
	"bytes"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/tkodcumpeg4/zorven/client/config"
)

// Masaustu uygulamasi AYNI config.json'u yazar (auto_connect, no_terminal, hide_to_tray,
// token_enc, tema, son portlar...). CLI'nin yazimlari (config set/unset, authtoken, ayarlar
// arayuzu) bu alanlari ve bilinmeyen gelecek alanlari silmemeli: token_enc silinirse
// masaustu oturumu duser.

const desktopWritten = `{
  "server_addr": "zorven.app:443",
  "token": "",
  "auto_connect": true,
  "no_terminal": true,
  "no_screen": false,
  "hide_to_tray": false,
  "token_enc": "dpapi:SIFRELI",
  "notify_disabled": true,
  "theme": "dark",
  "last_port": 4000,
  "recent_ports": [4000, 4001],
  "gelecek_alan": {"ic": [1, 2.50, null], "buyuk": 12345678901234567890}
}`

var desktopKept = map[string]string{
	"auto_connect": "true", "no_terminal": "true", "no_screen": "false", "hide_to_tray": "false",
	"token_enc": `"dpapi:SIFRELI"`, "notify_disabled": "true", "theme": `"dark"`, "last_port": "4000",
	"recent_ports": "[4000,4001]", "gelecek_alan": `{"ic":[1,2.50,null],"buyuk":12345678901234567890}`,
}

func rawConfigFields(t *testing.T, path string) map[string]string {
	t.Helper()
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

func wantRaw(t *testing.T, got, want map[string]string) {
	t.Helper()
	for k, w := range want {
		if got[k] != w {
			t.Errorf("%s = %q, beklenen %q", k, got[k], w)
		}
	}
}

func TestConfigCommandsPreserveDesktopFields(t *testing.T) {
	isolateHome(t)
	old := outW
	outW = io.Discard
	t.Cleanup(func() { outW = old })

	p, err := config.Path()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(desktopWritten), 0o600); err != nil {
		t.Fatal(err)
	}

	// zorven config set enable_deploy true
	if err := mutateConfig(false, "enable_deploy", setKeyFn("enable_deploy", "true"), false); err != nil {
		t.Fatal(err)
	}
	f := rawConfigFields(t, p)
	wantRaw(t, f, desktopKept)
	wantRaw(t, f, map[string]string{"enable_deploy": "true"})

	// zorven config set enable_terminal on ; ardindan unset enable_deploy
	if err := mutateConfig(false, "enable_terminal", setKeyFn("enable_terminal", "on"), false); err != nil {
		t.Fatal(err)
	}
	unset := func(c *config.Config) error { return c.Unset("enable_deploy") }
	if err := mutateConfig(false, "enable_deploy", unset, false); err != nil {
		t.Fatal(err)
	}
	f = rawConfigFields(t, p)
	wantRaw(t, f, desktopKept)
	wantRaw(t, f, map[string]string{"enable_terminal": "true"})
	if _, ok := f["enable_deploy"]; ok {
		t.Errorf("unset enable_deploy anahtari silmeli: %v", f)
	}

	// zorven authtoken <token> (config.SetToken): duz token yazilir, sifreli token kalir;
	// masaustu ikisi de varken acik metni gecerli sayar.
	if err := config.SetToken("zrv_live_cli_yeni"); err != nil {
		t.Fatal(err)
	}
	f = rawConfigFields(t, p)
	wantRaw(t, f, desktopKept)
	wantRaw(t, f, map[string]string{"token": `"zrv_live_cli_yeni"`, "enable_terminal": "true"})

	// config.SetServer
	if err := config.SetServer("srv.example:443"); err != nil {
		t.Fatal(err)
	}
	wantRaw(t, rawConfigFields(t, p), desktopKept)
}

// `zorven settings` arayuzu (SaveFile) de masaustu alanlarini korur.
func TestSettingsTUIPreservesDesktopFields(t *testing.T) {
	m, up, _ := testSettingsModel(t)
	if err := os.WriteFile(up, []byte(desktopWritten), 0o600); err != nil {
		t.Fatal(err)
	}
	m.load()
	// Kapsam -> Dil -> Sunucu -> Token -> Insecure -> CA -> Terminal: Enter acar ve kaydeder.
	keys(m, kDown, kDown, kDown, kDown, kDown, kDown, kEnter)
	f := rawConfigFields(t, up)
	wantRaw(t, f, desktopKept)
	wantRaw(t, f, map[string]string{"enable_terminal": "true"})
	// Kapat: anahtar silinmez (false yazilmaz ama onceki deger de kalmaz), masaustu alanlari yine korunur.
	keys(m, kEnter)
	f = rawConfigFields(t, up)
	wantRaw(t, f, desktopKept)
	if _, ok := f["enable_terminal"]; ok {
		t.Errorf("terminal kapatilinca anahtar silinmeli: %v", f)
	}
}

// Dosyada sunucu yokken ilgisiz bir anahtar yazmak varsayilan sunucuyu (zorven.app:443)
// dosyaya doldurmaz; sunucu anahtari ise yazilir ve sonraki yazimlarda korunur.
func TestConfigSetUnrelatedKeyDoesNotWriteDefaultServer(t *testing.T) {
	isolateHome(t)
	old := outW
	outW = io.Discard
	t.Cleanup(func() { outW = old })
	p, err := config.Path()
	if err != nil {
		t.Fatal(err)
	}
	if err := mutateConfig(false, "enable_terminal", setKeyFn("enable_terminal", "true"), false); err != nil {
		t.Fatal(err)
	}
	f := rawConfigFields(t, p)
	if _, ok := f["server_addr"]; ok {
		t.Errorf("server_addr yazilmamali: %v", f)
	}
	if f["enable_terminal"] != "true" {
		t.Errorf("enable_terminal yazilmadi: %v", f)
	}
	if c, _ := config.LoadFile(p); c.ServerAddr != config.DefaultConfig().ServerAddr {
		t.Errorf("okurken varsayilan gelmeli: %q", c.ServerAddr)
	}
	if err := mutateConfig(false, "server", setKeyFn("server", "a.example:1"), false); err != nil {
		t.Fatal(err)
	}
	wantRaw(t, rawConfigFields(t, p), map[string]string{"server_addr": `"a.example:1"`})
	if err := mutateConfig(false, "enable_screen", setKeyFn("enable_screen", "true"), false); err != nil {
		t.Fatal(err)
	}
	wantRaw(t, rawConfigFields(t, p), map[string]string{"server_addr": `"a.example:1"`})
}
