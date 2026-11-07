package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestReadTokenFileKeepsUntilSuccess(t *testing.T) {
	p := filepath.Join(t.TempDir(), "t")
	_ = os.WriteFile(p, []byte("zrv_live_abc\n"), 0o600)
	tok, rm, err := readTokenFileKeep(p)
	if err != nil || tok != "zrv_live_abc" {
		t.Fatalf("%q %v", tok, err)
	}
	if _, err := os.Stat(p); err != nil {
		t.Fatal("kurulum bitmeden token dosyasi silinmemeli")
	}
	rm()
	if _, err := os.Stat(p); !os.IsNotExist(err) {
		t.Fatal("token dosyasi basari sonrasi silinmeli")
	}
	_ = os.WriteFile(p, []byte("a b"), 0o600)
	if _, _, err := readTokenFileKeep(p); err == nil {
		t.Fatal("bosluklu token reddedilmeli")
	}
}
