package main

import (
	"strings"
	"testing"

	"github.com/spf13/cobra"
)

// Komut adlari duz sozcuk olmali (bosluk, <, [ yok) ve kardesler arasinda
// tekrarlanmamali: "access<paylasim..." gibi bir Use hatasi komutu bozar.
func TestCommandNamesSane(t *testing.T) {
	root := buildRoot()
	var walk func(c *cobra.Command, path string)
	walk = func(c *cobra.Command, path string) {
		seen := map[string]bool{}
		for _, sub := range c.Commands() {
			n := sub.Name()
			p := path + " " + n
			if n == "" || strings.ContainsAny(n, " <>[]|") {
				t.Errorf("gecersiz komut adi %q (%s)", n, p)
			}
			if seen[n] {
				t.Errorf("tekrarlanan komut adi %q altinda %s", n, path)
			}
			seen[n] = true
			for _, a := range sub.Aliases {
				if seen[a] {
					t.Errorf("takma ad %q baska komutla cakisiyor (%s)", a, p)
				}
				seen[a] = true
			}
			walk(sub, p)
		}
	}
	walk(root, "zorven")
}

func TestAccessCommandReachable(t *testing.T) {
	root := buildRoot()
	c, _, err := root.Find([]string{"access"})
	if err != nil || c == nil || c.Name() != "access" {
		t.Fatalf("access bulunamadi: %v %v", c, err)
	}
}
