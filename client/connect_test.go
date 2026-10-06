package main

import "testing"

func TestParseAllowFrom(t *testing.T) {
	got, err := parseAllowFrom(" 192.168.2.77/24 , 10.0.0.0/8,")
	if err != nil || len(got) != 2 || got[0].String() != "192.168.2.0/24" {
		t.Errorf("= %v, %v", got, err)
	}
	if _, err := parseAllowFrom("192.168.2.0"); err == nil {
		t.Error("maskesiz adres kabul edildi")
	}
	if got, err := parseAllowFrom(""); err != nil || len(got) != 0 {
		t.Errorf("bos = %v, %v", got, err)
	}
}

func TestIsLoopbackListen(t *testing.T) {
	for _, ok := range []string{"127.0.0.1:1080", "localhost:1080", "[::1]:1080"} {
		if !isLoopbackListen(ok) {
			t.Errorf("%q loopback sayilmali", ok)
		}
	}
	for _, bad := range []string{"0.0.0.0:1080", "192.168.1.5:1080", ":1080", "bozuk"} {
		if isLoopbackListen(bad) {
			t.Errorf("%q loopback sayilmamali", bad)
		}
	}
}
