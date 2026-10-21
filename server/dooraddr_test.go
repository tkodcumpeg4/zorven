package main

import "testing"

func TestDoorAddrHosts(t *testing.T) {
	v4, v6 := doorAddrHosts("zorven.app", "", "")
	if v4 != "door4.zorven.app" || v6 != "door6.zorven.app" {
		t.Fatalf("varsayilan: %q %q", v4, v6)
	}
	v4, v6 = doorAddrHosts("zorven.app", "A4.example.com", "off")
	if v4 != "a4.example.com" || v6 != "" {
		t.Fatalf("env: %q %q", v4, v6)
	}
	if v4, v6 = doorAddrHosts("", "", ""); v4 != "" || v6 != "" {
		t.Fatal("platform domain yokken kapali olmali")
	}
}
