package main

import (
	"reflect"
	"testing"
)

func TestShellQuote(t *testing.T) {
	cases := map[string]string{
		"up":          "up",
		"":            "''",
		"a b":         "'a b'",
		"it's":        `'it'\''s'`,
		"zrv_live_1":  "zrv_live_1",
		"x;rm -rf /":  "'x;rm -rf /'",
		"$(id)":       "'$(id)'",
		"host:443":    "host:443",
		"--token=a&b": "'--token=a&b'",
	}
	for in, want := range cases {
		if got := shellQuote(in); got != want {
			t.Errorf("shellQuote(%q)=%q want %q", in, got, want)
		}
	}
}

func TestSudoCommand(t *testing.T) {
	got := sudoCommand([]string{"/usr/local/bin/zorven", "up", "--zv-elevated", "--no-elevate", "--server", "a b:443"})
	want := "sudo /usr/local/bin/zorven up --server 'a b:443'"
	if got != want {
		t.Fatalf("%q", got)
	}
}

func TestElevatedArgs(t *testing.T) {
	got := elevatedArgs([]string{"service", "install", "--no-elevate", "-t", "x"})
	want := []string{"--zv-elevated", "service", "install", "-t", "x"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("%q", got)
	}
}

func TestWindowsCmdLine(t *testing.T) {
	cases := []struct {
		args []string
		want string
	}{
		{[]string{"up", "--server", "a:443"}, `up --server a:443`},
		{[]string{""}, `""`},
		{[]string{`C:\Program Files\x`}, `"C:\Program Files\x"`},
		{[]string{`C:\dir with space\`}, `"C:\dir with space\\"`},
		{[]string{`say "hi"`}, `"say \"hi\""`},
		{[]string{`a\"b`}, `"a\\\"b"`},
		{[]string{`plain\path`}, `plain\path`},
	}
	for _, c := range cases {
		if got := windowsCmdLine(c.args); got != c.want {
			t.Errorf("%q => %s want %s", c.args, got, c.want)
		}
	}
}
