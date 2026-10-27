package main

import (
	"errors"
	"os"
	"strings"
	"testing"
)

func TestBuildServiceInstallParams(t *testing.T) {
	exe := `C:\Program Files\Zorven\zorven.exe`
	tf := `C:\Users\a\AppData\Local\zorven\svc-token-0011`
	p, err := buildServiceInstallParams(exe, tf, "zorven.app:443")
	if err != nil {
		t.Fatal(err)
	}
	want := `/c ""C:\Program Files\Zorven\zorven.exe" service install --token-file "C:\Users\a\AppData\Local\zorven\svc-token-0011" --server zorven.app:443 && "C:\Program Files\Zorven\zorven.exe" service start"`
	if p != want {
		t.Fatalf("parametre farkli:\n got %s\nwant %s", p, want)
	}
	if strings.Contains(p, "zrv_live") {
		t.Fatal("token komut satirinda olmamali")
	}
	bad := []struct{ exe, tf, srv string }{
		{exe, tf + `" & calc & "`, "zorven.app:443"},
		{exe, tf, "zorven.app:443 & calc"},
		{exe, tf, "a\"b"},
		{`C:\x" & calc & "`, tf, "zorven.app"},
		{"", tf, "zorven.app"},
		{exe, "", "zorven.app"},
	}
	for _, b := range bad {
		if _, err := buildServiceInstallParams(b.exe, b.tf, b.srv); err == nil {
			t.Errorf("gecersiz girdi kabul edildi: %+v", b)
		}
	}
}

func TestWriteInstallTokenFile(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	p, err := writeInstallTokenFile("zrv_live_abc")
	if err != nil {
		t.Fatal(err)
	}
	defer os.Remove(p)
	b, err := os.ReadFile(p)
	if err != nil || string(b) != "zrv_live_abc" {
		t.Fatalf("icerik: %q %v", b, err)
	}
	if fi, _ := os.Stat(p); fi.Mode().Perm()&0o077 != 0 {
		t.Fatalf("izinler gevsek: %v", fi.Mode())
	}
}

func TestTunErrView(t *testing.T) {
	v := tunErrView(os.ErrNotExist)
	if !v.NeedService || v.Hint == "" || v.Peers == nil {
		t.Fatalf("servis yok gorunumu hatali: %+v", v)
	}
	v = tunErrView(os.ErrPermission)
	if !v.NeedElevation || !v.ServiceFound {
		t.Fatalf("izin gorunumu hatali: %+v", v)
	}
	v = tunErrView(errors.New("yetkisiz: baglanti reddedildi"))
	if !v.NeedElevation {
		t.Fatalf("yetkisiz izin sayilmali: %+v", v)
	}
	v = tunErrView(errors.New("dial unix: connect: connection refused"))
	if !v.NeedService || !strings.Contains(v.Error, "erisilemedi") {
		t.Fatalf("baglanti hatasi gorunumu hatali: %+v", v)
	}
}
