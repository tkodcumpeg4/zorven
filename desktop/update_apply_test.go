package main

import (
	"os"
	"path/filepath"
	"testing"
)

// Yardimci kip yanlis arguman ya da dogrulanamayan dosyada hicbir seyi
// degistirmeden cikmali ve gecici dosyayi silmeli.
func TestRunApplyUpdateRejects(t *testing.T) {
	if got := runApplyUpdate(nil); got != 2 {
		t.Fatalf("arguman yok: %d, beklenen 2", got)
	}
	p := filepath.Join(t.TempDir(), "zorven-update-x.exe")
	if err := os.WriteFile(p, []byte("MZ sahte"), 0o600); err != nil {
		t.Fatal(err)
	}
	if got := runApplyUpdate([]string{p, "00"}); got != 3 {
		t.Fatalf("sha uyusmuyor: %d, beklenen 3", got)
	}
	if _, err := os.Stat(p); !os.IsNotExist(err) {
		t.Fatalf("gecici dosya silinmedi: %v", err)
	}
}
