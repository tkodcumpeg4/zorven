//go:build windows || linux

package main

import "testing"

// Tepsi (tray.go) yalniz windows/linux'ta derlendigi icin sharePortList testi de
// ayni derleme etiketiyle tutulur; darwin'de tepsi yok (tray_darwin.go).
func TestSharePortList(t *testing.T) {
	got := sharePortList(4000, []int{3000, 4000, 9, 70000})
	want := []int{4000, 3000, 9, 5173, 8080}
	if len(got) != len(want) {
		t.Fatalf("%v != %v", got, want)
	}
	for i := range got {
		if got[i] != want[i] {
			t.Fatalf("%v != %v", got, want)
		}
	}
}
