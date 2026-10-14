package main

import (
	"crypto/rand"
	"encoding/base64"
	"testing"
)

func TestCaptureEnvDefaults(t *testing.T) {
	c := captureEnvFrom(func(string) string { return "" })
	if c.MaxBody != 256<<10 || c.TenantQuotaBytes != 200<<20 || c.GlobalLimitBytes != 20<<30 || c.CacheBytes != 32<<20 {
		t.Fatalf("varsayilanlar yanlis: %+v", c)
	}
	env := map[string]string{"ZORVEN_CAPTURE_MAX_BODY": "1024", "ZORVEN_CAPTURE_TENANT_QUOTA_MB": "5",
		"ZORVEN_CAPTURE_GLOBAL_LIMIT_GB": "bozuk", "ZORVEN_CAPTURE_TABLESPACE": " ek_disk "}
	c = captureEnvFrom(func(k string) string { return env[k] })
	if c.MaxBody != 1024 || c.TenantQuotaBytes != 5<<20 || c.GlobalLimitBytes != 20<<30 || c.Tablespace != "ek_disk" {
		t.Fatalf("env okunmadi: %+v", c)
	}
}

func TestCaptureCodecFromEnv(t *testing.T) {
	if captureCodecFromEnv(func(string) string { return "" }) != nil {
		t.Fatal("anahtar yoksa kodlayici nil olmali (kalici yakalama kapali)")
	}
	if captureCodecFromEnv(func(string) string { return "kisa" }) != nil {
		t.Fatal("gecersiz anahtar nil olmali")
	}
	k := make([]byte, 32)
	_, _ = rand.Read(k)
	enc := base64.StdEncoding.EncodeToString(k)
	if captureCodecFromEnv(func(string) string { return enc }) == nil {
		t.Fatal("gecerli anahtarla kodlayici olmali")
	}
}
