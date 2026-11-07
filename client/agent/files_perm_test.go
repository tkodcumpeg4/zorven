package agent

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestCreateUniqueMode0600(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("izin bitleri Windows'ta anlamsiz")
	}
	dir := t.TempDir()
	f, name, err := createUnique(dir, "a.txt")
	if err != nil {
		t.Fatal(err)
	}
	f.Close()
	fi, err := os.Stat(filepath.Join(dir, name))
	if err != nil {
		t.Fatal(err)
	}
	if fi.Mode().Perm() != 0o600 {
		t.Fatalf("mod %v, beklenen 0600", fi.Mode().Perm())
	}
}
