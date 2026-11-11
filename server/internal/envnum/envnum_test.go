package envnum

import "testing"

func TestInt(t *testing.T) {
	env := map[string]string{}
	get := func(k string) string { return env[k] }
	cases := []struct {
		v    string
		want int
	}{
		{"", 20}, {" 7 ", 7}, {"abc", 20}, {"-3", 20}, {"1000000", 20}, {"1", 1}, {"500", 500},
	}
	for _, c := range cases {
		env["X"] = c.v
		if got := Int(get, "X", 20, 1, 500); got != c.want {
			t.Fatalf("%q: %d, beklenen %d", c.v, got, c.want)
		}
	}
	env["X"] = "abc"
	if _, err := IntStrict(get, "X", 1, 0, 9); err == nil {
		t.Fatal("strict gecersizde hata vermeli")
	}
	env["X"] = "10"
	if _, err := IntStrict(get, "X", 1, 0, 9); err == nil {
		t.Fatal("strict aralik disinda hata vermeli")
	}
	env["X"] = ""
	if n, err := IntStrict(get, "X", 4, 0, 9); err != nil || n != 4 {
		t.Fatalf("strict bos: %d %v", n, err)
	}
}
