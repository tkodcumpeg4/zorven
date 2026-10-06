package main

import "testing"

func TestNewerVersion(t *testing.T) {
	cases := []struct {
		a, b string
		want bool
	}{
		{"0.2.0", "0.1.6", true}, {"0.1.6", "0.2.0", false}, {"0.1.6", "0.1.6", false},
		{"v1.0.0", "0.9.9", true}, {"0.10.0", "0.9.0", true}, {"0.2.0-rc1", "0.1.9", true},
		{"0.2", "0.1.9", true}, {"", "0.1.0", false},
	}
	for _, c := range cases {
		if got := newerVersion(c.a, c.b); got != c.want {
			t.Errorf("newerVersion(%q,%q)=%v", c.a, c.b, got)
		}
	}
}
