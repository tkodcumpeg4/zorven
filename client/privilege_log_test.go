package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLogFileFromArgs(t *testing.T) {
	if got := logFileFromArgs([]string{"--zv-log-file", "/tmp/x.log", "up"}); got != "/tmp/x.log" {
		t.Fatal(got)
	}
	if got := logFileFromArgs([]string{"--zv-log-file=/tmp/y.log"}); got != "/tmp/y.log" {
		t.Fatal(got)
	}
	if logFileFromArgs([]string{"up"}) != "" {
		t.Fatal("bos olmali")
	}
}

func TestTeeOutputToFile(t *testing.T) {
	p := filepath.Join(t.TempDir(), "o.log")
	oo, oe := os.Stdout, os.Stderr
	flush := teeOutputToFile(p)
	fmt.Fprintln(os.Stdout, "merhaba cikti")
	fmt.Fprintln(os.Stderr, "hata cikti")
	flush()
	if os.Stdout != oo || os.Stderr != oe {
		t.Fatal("stdout/stderr geri konmadi")
	}
	b, _ := os.ReadFile(p)
	if !strings.Contains(string(b), "merhaba cikti") || !strings.Contains(string(b), "hata cikti") {
		t.Fatalf("dosyada cikti yok: %q", b)
	}
}
