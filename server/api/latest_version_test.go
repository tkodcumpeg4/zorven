package api

import (
	"os"
	"path/filepath"
	"testing"
)

func TestIsOutdated(t *testing.T) {
	cases := []struct {
		cur, latest string
		want        bool
	}{
		{"0.1.0", "0.2.2", true},
		{"0.2.2", "0.2.2", false},
		{"0.2.3", "0.2.2", false},
		{"v0.2.1", "0.2.2", true},
		{"0.2.9", "0.10.0", true},
		{"dev", "0.2.2", false},
		{"", "0.2.2", false},
		{"0.1.0", "", false},
	}
	for _, c := range cases {
		if got := isOutdated(c.cur, c.latest); got != c.want {
			t.Errorf("isOutdated(%q,%q)=%v, beklenen %v", c.cur, c.latest, got, c.want)
		}
	}
}

func TestLatestClientVersionReadsManifest(t *testing.T) {
	dir := t.TempDir()
	old, _ := os.Getwd()
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	defer os.Chdir(old)
	_ = os.MkdirAll(filepath.Join(dir, "bin"), 0o755)
	_ = os.WriteFile(filepath.Join(dir, "bin", "zz_test_manifest.json"), []byte(`{"version":"0.2.3","files":{}}`), 0o644)
	if got := latestClientVersion("zz_test_manifest.json"); got != "0.2.3" {
		t.Fatalf("latest=%q", got)
	}
	if got := latestClientVersion("zz_yok.json"); got != "" {
		t.Fatalf("olmayan manifest icin %q", got)
	}
	if manifestFor("desktop") != "desktop.json" || manifestFor("") != "manifest.json" {
		t.Fatal("manifestFor yanlis")
	}
}
