package main

import "testing"

func TestParseLegacyUnit(t *testing.T) {
	cases := []struct {
		name   string
		unit   string
		legacy bool
		term   bool
		screen bool
	}{
		{"eski bayraksiz", "[Service]\nExecStart=/usr/local/bin/zorven\n", true, false, false},
		{"eski terminal", "[Service]\nExecStart=/usr/local/bin/zorven --enable-terminal\nRestart=always\n", true, true, false},
		{"eski ikisi", "[Service]\nExecStart=/usr/local/bin/zorven --enable-terminal --enable-screen=true\n", true, true, true},
		{"eski false", "[Service]\nExecStart=/usr/local/bin/zorven --enable-terminal=false\n", true, false, false},
		{"tirnakli yol", "[Service]\nExecStart=\"/opt/my dir/zorven\" -enable-screen\n", true, false, true},
		{"guncel", "[Service]\nExecStart=/usr/local/bin/zorven service run\n", false, false, false},
		{"guncel tirnakli", "[Service]\nExecStart=\"/usr/local/bin/zorven\" \"service\" \"run\"\n", false, false, false},
		{"baska program", "[Service]\nExecStart=/usr/bin/other --enable-terminal\n", false, false, false},
		{"execstart yok", "[Unit]\nDescription=x\n", false, false, false},
		{"bos", "", false, false, false},
	}
	for _, c := range cases {
		lu, ok := parseLegacyUnit(c.unit)
		if ok != c.legacy || lu.terminal != c.term || lu.screen != c.screen {
			t.Errorf("%s: got legacy=%v %+v", c.name, ok, lu)
		}
	}
}

func TestSplitExecStart(t *testing.T) {
	got := splitExecStart(`-"/a b/zorven"  x 'y z'`)
	want := []string{"/a b/zorven", "x", "y z"}
	if len(got) != len(want) {
		t.Fatalf("%q", got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("%q", got)
		}
	}
}
