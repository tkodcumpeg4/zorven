package main

import (
	"crypto/sha256"
	"encoding/hex"
	"runtime"
	"testing"
)

func TestNewerVersion(t *testing.T) {
	cases := []struct {
		a, b string
		want bool
	}{
		{"0.2.0", "0.1.6", true}, {"0.1.6", "0.2.0", false}, {"0.1.6", "0.1.6", false},
		{"v1.0.0", "0.9.9", true}, {"0.10.0", "0.9.0", true}, {"0.2.0-rc1", "0.1.9", true},
		{"0.2", "0.1.9", true}, {"", "0.1.0", false},
	}
	for _, c := range cases {
		if got := newerVersion(c.a, c.b); got != c.want {
			t.Errorf("newerVersion(%q,%q)=%v", c.a, c.b, got)
		}
	}
}

func TestVerifyDownload(t *testing.T) {
	data := []byte("MZ-fake-exe")
	sum := sha256.Sum256(data)
	good := hex.EncodeToString(sum[:])
	if m := verifyDownload(data, good, int64(len(data))); m != "" {
		t.Fatalf("gecerli dosya reddedildi: %s", m)
	}
	if verifyDownload(data, "00", 0) == "" {
		t.Error("yanlis sha256 kabul edildi")
	}
	if verifyDownload(data, good, 5) == "" {
		t.Error("yanlis boyut kabul edildi")
	}
	if runtime.GOOS == "windows" {
		bad := []byte("PK-installer")
		s2 := sha256.Sum256(bad)
		if verifyDownload(bad, hex.EncodeToString(s2[:]), 0) == "" {
			t.Error("MZ olmayan dosya kabul edildi")
		}
	}
}

func TestNewerVersionPatch(t *testing.T) {
	if !newerVersion("0.2.3", "0.2.1") || newerVersion("0.2.1", "0.2.3") || newerVersion("0.2.3", "0.2.3") {
		t.Error("yama surumu karsilastirmasi hatali")
	}
}
