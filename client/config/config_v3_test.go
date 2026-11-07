package config

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func isolate(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("HOME", dir)
	t.Setenv("XDG_CONFIG_HOME", dir)
	t.Setenv("APPDATA", dir)
}

func TestSetTokenDoesNotCopySystemConfig(t *testing.T) {
	isolate(t)
	p, _ := Path()
	if err := SetToken("zrv_live_abc"); err != nil {
		t.Fatal(err)
	}
	c, _ := LoadFile(p)
	if c.EnableTerminal || c.EnableScreen || c.Insecure || c.Token != "zrv_live_abc" {
		t.Fatalf("beklenmeyen: %+v", c)
	}
}

func TestCorruptConfigBackedUpAndAtomicSave(t *testing.T) {
	isolate(t)
	p, _ := Path()
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte("{bozuk"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(); err == nil {
		t.Fatal("bozuk dosya hata vermeli")
	}
	if b, err := os.ReadFile(p + ".bak"); err != nil || string(b) != "{bozuk" {
		t.Fatalf("yedek yok: %v %q", err, b)
	}
	if err := SetToken("zrv_live_x"); err != nil {
		t.Fatal(err)
	}
	if c, err := LoadFile(p); err != nil || c.Token != "zrv_live_x" {
		t.Fatalf("%v %+v", err, c)
	}
	left, _ := filepath.Glob(filepath.Join(filepath.Dir(p), "*.tmp-*"))
	if len(left) != 0 {
		t.Fatalf("gecici dosya kaldi: %v", left)
	}
	if runtime.GOOS != "windows" {
		if fi, _ := os.Stat(p); fi.Mode().Perm() != 0o600 {
			t.Fatalf("izin: %v", fi.Mode())
		}
	}
}

func TestKeysAndAPIToken(t *testing.T) {
	isolate(t)
	c := DefaultConfig()
	if err := c.Set("enable_screen", "on"); err != nil || !c.EnableScreen {
		t.Fatal("set bool")
	}
	if err := c.Set("insecure", "maybe"); err == nil {
		t.Fatal("gecersiz bool kabul edildi")
	}
	if err := c.Unset("enable_screen"); err != nil || c.EnableScreen {
		t.Fatal("unset")
	}
	if _, err := c.Get("nope"); err == nil {
		t.Fatal("bilinmeyen anahtar")
	}
	if err := SaveAPIToken("zrv_api_1"); err != nil {
		t.Fatal(err)
	}
	if LoadAPIToken() != "zrv_api_1" {
		t.Fatal("api token okunamadi")
	}
	if ok, err := DeleteAPIToken(); !ok || err != nil {
		t.Fatal("silinemedi")
	}
	if LoadAPIToken() != "" {
		t.Fatal("silinmedi")
	}
}
