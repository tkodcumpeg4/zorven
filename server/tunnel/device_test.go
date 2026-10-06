package tunnel

import "testing"

// splitPlatform, beklenmedik bicimde SESSIZCE bos doner: yanlis bir deger
// kaydetmektense hic kaydetmemek yeglenir (bos alan mevcudu ezmez).
func TestSplitPlatform(t *testing.T) {
	cases := []struct {
		in, os, arch string
	}{
		{"windows/amd64", "windows", "amd64"},
		{"linux/arm64", "linux", "arm64"},
		{" darwin / arm64 ", "darwin", "arm64"},
		{"windows", "", ""},
		{"", "", ""},
		{"/", "", ""},
	}
	for _, c := range cases {
		gotOS, gotArch := splitPlatform(c.in)
		if gotOS != c.os || gotArch != c.arch {
			t.Errorf("splitPlatform(%q) = (%q, %q), beklenen (%q, %q)",
				c.in, gotOS, gotArch, c.os, c.arch)
		}
	}
}
