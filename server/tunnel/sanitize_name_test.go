package tunnel

import (
	"strings"
	"testing"
)

// "--" kiraci ayracidir (ad--<slug>); sanitizeTunnelName asla "--" uretmemeli.
func TestSanitizeTunnelName(t *testing.T) {
	cases := []struct{ in, want string }{
		{"My Laptop", "my-laptop"},
		{"x--v1", "x-v1"},
		{"a---b----c", "a-b-c"},
		{"--lead--trail--", "lead-trail"},
		{"a_-_b", "a-b"},
		{"!!!", "port"},
		{"", "port"},
		{"ok-8080", "ok-8080"},
	}
	for _, c := range cases {
		got := sanitizeTunnelName(c.in)
		if got != c.want {
			t.Errorf("sanitizeTunnelName(%q) = %q, beklenen %q", c.in, got, c.want)
		}
		if strings.Contains(got, "--") {
			t.Errorf("sanitizeTunnelName(%q) = %q '--' iceriyor", c.in, got)
		}
	}
}
