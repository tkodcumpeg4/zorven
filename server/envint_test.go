package main

import "testing"

func TestEnvIntDefault(t *testing.T) {
	t.Setenv("ZV_T", "")
	if envIntDefault("ZV_T", 5) != 5 {
		t.Fatal("bos")
	}
	t.Setenv("ZV_T", "0")
	if envIntDefault("ZV_T", 5) != 0 {
		t.Fatal("0 sinirsiz")
	}
	t.Setenv("ZV_T", "77")
	if envIntDefault("ZV_T", 5) != 77 {
		t.Fatal("override")
	}
	t.Setenv("ZV_T", "-3")
	if envIntDefault("ZV_T", 5) != 5 {
		t.Fatal("negatif")
	}
}
