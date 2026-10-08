package store

import "testing"

func TestContainsBrandKeyword(t *testing.T) {
	for _, s := range []string{"zorven", "zorven-login", "myzorven", "zorvenpay", "RPSHELL", "x-rpshell-y", "zor-ven", "zorven1"} {
		if !ContainsBrandKeyword(s) {
			t.Errorf("%q marka deseni sayilmali", s)
		}
	}
	for _, s := range []string{"api", "benim-projem", "zor", "ven", "shell", "rp-sh", "acme"} {
		if ContainsBrandKeyword(s) {
			t.Errorf("%q marka deseni sayilmamali", s)
		}
	}
}

func TestSlugReservedReason(t *testing.T) {
	reserved := func(n string) bool { return n == "admin" || n == "support" }
	cases := map[string]string{
		"acme":         "",
		"zorven-corp":  "brand",
		"admin":        "reserved",
		"Support":      "reserved",
		"default":      "", // platform kiracisi
		"my-rpshell-x": "brand",
	}
	for slug, want := range cases {
		if got := SlugReservedReason(slug, reserved); got != want {
			t.Errorf("SlugReservedReason(%q) = %q, beklenen %q", slug, got, want)
		}
	}
}

// Otomatik slug donusumu kayit akisini bozmaz: marka sozcugu cikarilir, rezerve
// tam eslesmeye ek yapilir.
func TestSanitizeGeneratedSlug(t *testing.T) {
	reserved := func(n string) bool { return n == "admin" }
	suffix := func() string { return "abc" }
	cases := map[string]string{
		"acme":          "acme",
		"zorven":        "user",
		"myzorven":      "my",
		"zorven-login":  "login",
		"zor-ven-pay":   "pay",
		"zorzorvenven":  "user",
		"admin":         "admin-abc",
		"rpshell-admin": "admin-abc",
	}
	for in, want := range cases {
		got := SanitizeGeneratedSlug(in, reserved, suffix)
		if got != want {
			t.Errorf("SanitizeGeneratedSlug(%q) = %q, beklenen %q", in, got, want)
		}
		if ContainsBrandKeyword(got) {
			t.Errorf("sonuc hala marka iceriyor: %q", got)
		}
	}
}
