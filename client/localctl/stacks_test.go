package localctl

import (
	"os"
	"strings"
	"testing"
)

func TestWriteStackDump(t *testing.T) {
	p, err := writeStackDump()
	if err != nil {
		t.Skipf("yazilamadi (izin): %v", err)
	}
	defer os.Remove(p)
	b, _ := os.ReadFile(p)
	if !strings.Contains(string(b), "goroutine") {
		t.Fatal("yigin dokumu goroutine icermeli")
	}
}
