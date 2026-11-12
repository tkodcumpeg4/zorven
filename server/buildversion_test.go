package main

import (
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tkodcumpeg4/zorven/shared/protocol"
)

// Elle derlenen ikili "dev" gosterir; protokol surumu yerine gecmez.
func TestBuildVersionDefault(t *testing.T) {
	if buildVersion != "dev" {
		t.Fatalf("buildVersion varsayilani %q, beklenen dev", buildVersion)
	}
	if buildVersion == protocol.Version {
		t.Fatal("buildVersion protokol surumuyle ayni olmamali")
	}
}

// -ldflags -X main.buildVersion gercekten gomulur (scripts/build-server.sh ve
// deploy/Dockerfile bu adi kullanir; degisken yeniden adlandirilirsa -X sessizce
// etkisiz kalirdi).
func TestBuildVersionLdflags(t *testing.T) {
	if testing.Short() {
		t.Skip("-short: ikili derlemesi atlandi")
	}
	bin := filepath.Join(t.TempDir(), "zorven-server")
	build := exec.Command("go", "build", "-ldflags", "-X main.buildVersion=v9.9.9-test+20990101", "-o", bin, ".")
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("derleme: %v\n%s", err, out)
	}
	out, err := exec.Command(bin, "--version").CombinedOutput()
	if err != nil {
		t.Fatalf("--version: %v\n%s", err, out)
	}
	if !strings.Contains(string(out), "v9.9.9-test+20990101") || !strings.Contains(string(out), protocol.Version) {
		t.Fatalf("--version ciktisi gomulu surumu/protokolu icermiyor: %s", out)
	}
}
