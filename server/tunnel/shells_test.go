package tunnel

import (
	"strings"
	"testing"

	"github.com/tkodcumpeg4/zorven/shared/protocol"
)

func TestSanitizeShells(t *testing.T) {
	in := []protocol.ShellInfo{
		{ID: "zsh", Name: "Zsh", Path: "/bin/zsh"},
		{ID: "zsh", Name: "dup", Path: "/x"},            // tekrar
		{ID: "../etc", Name: "kotu", Path: "/x"},       // gecersiz ID
		{ID: "Bash", Name: "buyuk harf", Path: "/x"},   // gecersiz ID
		{ID: "bash", Name: "", Path: strings.Repeat("a", 1000)},
	}
	out, def := sanitizeShells(in, "fish")
	if len(out) != 2 || out[0].ID != "zsh" || out[1].ID != "bash" {
		t.Fatalf("beklenmeyen: %+v", out)
	}
	if out[1].Name != "bash" || len(out[1].Path) != 512 {
		t.Fatalf("ad/yol duzeltilmedi: %+v", out[1])
	}
	if def != "zsh" {
		t.Fatalf("listede olmayan varsayilan ilk kabuga donmeli: %q", def)
	}
	if o, d := sanitizeShells(nil, "zsh"); o != nil || d != "" {
		t.Fatal("bos liste bos donmeli")
	}
}

func TestSessionHasShell(t *testing.T) {
	s := &Session{}
	if s.HasShell("zsh") {
		t.Fatal("eski istemcide kabuk olmamali")
	}
	s.Shells = []protocol.ShellInfo{{ID: "zsh"}}
	if !s.HasShell("zsh") || s.HasShell("bash") {
		t.Fatal("HasShell yanlis")
	}
}
