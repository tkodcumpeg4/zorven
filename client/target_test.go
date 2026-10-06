package main

import "testing"

func TestLooksLikeTarget(t *testing.T) {
	for in, want := range map[string]bool{
		"http://localhost:8080": true,
		"http://localhost":      true,
		"http://example.com":    true,
		"http://10.0.0.5":       true,
		"http://versoin":        false,
		"http://status":         false,
		"":                      false,
	} {
		if got := looksLikeTarget(in); got != want {
			t.Errorf("looksLikeTarget(%q) = %v", in, got)
		}
	}
}

// Hedef yalnizca kullanici acikca verirse belirlenir; aksi halde bos kalir
// (istemci hicbir yerel port icin tunel istemez).
func TestResolveTarget(t *testing.T) {
	cases := []struct {
		local string
		args  []string
		want  string
	}{
		{"", nil, ""},
		{"  ", nil, ""},
		{"", []string{"8080"}, "http://localhost:8080"},
		{"", []string{"http", "3000"}, "http://localhost:3000"},
		{"", []string{"http://127.0.0.1:9000/"}, "http://127.0.0.1:9000"},
		{"5173", nil, "http://localhost:5173"},
		{"", []string{""}, ""},
	}
	for _, c := range cases {
		if got := resolveTarget(c.local, c.args); got != c.want {
			t.Errorf("resolveTarget(%q, %v) = %q, beklenen %q", c.local, c.args, got, c.want)
		}
	}
}
