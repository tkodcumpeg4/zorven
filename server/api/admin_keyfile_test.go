package api

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// Uretilen admin anahtari dosyaya (0600) yazilir; yol env ile degistirilebilir (F-25).
func TestWriteGeneratedAdminKey(t *testing.T) {
	p := filepath.Join(t.TempDir(), "k.txt")
	t.Setenv("ZORVEN_ADMIN_KEY_FILE", p)
	got, err := writeGeneratedAdminKey("zrv_adm_secretvalue")
	if err != nil {
		t.Fatal(err)
	}
	if got != p {
		t.Fatalf("yol %q, beklenen %q", got, p)
	}
	b, _ := os.ReadFile(p)
	if strings.TrimSpace(string(b)) != "zrv_adm_secretvalue" {
		t.Fatalf("icerik: %q", b)
	}
	if runtime.GOOS != "windows" {
		if fi, _ := os.Stat(p); fi.Mode().Perm() != 0o600 {
			t.Fatalf("izin %v, beklenen 0600", fi.Mode().Perm())
		}
	}
	// Yazilamayan yol hata doner (anahtar sessizce kaybolmaz).
	t.Setenv("ZORVEN_ADMIN_KEY_FILE", filepath.Join(t.TempDir(), "yok", "k.txt"))
	if _, err := writeGeneratedAdminKey("x"); err == nil {
		t.Fatal("hata bekleniyordu")
	}
}
