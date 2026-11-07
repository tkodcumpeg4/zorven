//go:build linux

package agent

import "testing"

func TestWaylandOnly(t *testing.T) {
	cases := []struct {
		st, wd, d string
		want      bool
	}{
		{"wayland", "wayland-0", "", true},
		{"wayland", "", "", true},
		{"", "wayland-0", "", true},
		{"wayland", "wayland-0", ":0", false}, // XWayland var
		{"x11", "", ":0", false},
		{"", "", "", false},
		{"tty", "", "", false},
	}
	for _, c := range cases {
		if got := waylandOnly(c.st, c.wd, c.d); got != c.want {
			t.Errorf("%+v: got %v", c, got)
		}
	}
}
