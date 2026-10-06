package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Varsayilan yapilandirma hicbir yerel hedef icermez: istemci kendiliginden
// bir porta tunel acmamali.
func TestDefaultConfigHasNoLocalURL(t *testing.T) {
	if got := DefaultConfig().LocalURL; got != "" {
		t.Fatalf("varsayilan LocalURL bos olmali, = %q", got)
	}
}

// Dosya yoksa da hedef bos gelir.
func TestLoadFileMissingHasNoLocalURL(t *testing.T) {
	cfg, err := LoadFile(filepath.Join(t.TempDir(), "yok.json"))
	if err != nil || cfg.LocalURL != "" {
		t.Fatalf("cfg=%+v err=%v", cfg, err)
	}
}

// Eski surumlerin yazdigi otomatik varsayilanlar yuklenirken temizlenir;
// kullanicinin kendi yazdigi hedef korunur.
func TestLoadFileMigratesLegacyLocalURL(t *testing.T) {
	cases := map[string]string{
		"http://localhost:8003":  "",
		"http://localhost:8003/": "",
		"http://localhost:8000":  "",
		"http://localhost:3000":  "",
		"http://localhost:9999":  "http://localhost:9999",
		"":                       "",
	}
	for in, want := range cases {
		p := filepath.Join(t.TempDir(), "config.json")
		body := `{"server_addr":"zorven.app:443","token":"zrv_live_x","local_url":"` + in + `"}`
		if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
		cfg, err := LoadFile(p)
		if err != nil {
			t.Fatal(err)
		}
		if cfg.LocalURL != want {
			t.Errorf("local_url %q -> %q, beklenen %q", in, cfg.LocalURL, want)
		}
	}
}

// Kaydedilen dosya local_url alanini bos iken hic yazmaz.
func TestSaveFileOmitsEmptyLocalURL(t *testing.T) {
	p := filepath.Join(t.TempDir(), "config.json")
	if err := SaveFile(p, DefaultConfig()); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(p)
	if strings.Contains(string(data), "local_url") {
		t.Fatalf("local_url yazilmamali: %s", data)
	}
}
