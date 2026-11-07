package sdk

import "testing"

func TestFeaturesDefaultOff(t *testing.T) {
	var c Config
	if !c.terminalOff() || !c.screenOff() {
		t.Fatal("sifir deger Config terminal/ekrani KAPALI tutmali")
	}
	c = Config{EnableTerminal: true, EnableScreen: true}
	if c.terminalOff() || c.screenOff() {
		t.Fatal("Enable* acik olmali")
	}
	c = Config{EnableTerminal: true, EnableScreen: true, NoTerminal: true, NoScreen: true}
	if !c.terminalOff() || !c.screenOff() {
		t.Fatal("No* zorla kapatmali")
	}
}

func TestNormalizeTarget(t *testing.T) {
	for in, want := range map[string]string{
		"8080":          "http://localhost:8080",
		"localhost:80":  "http://localhost:80",
		"https://x.y/z": "https://x.y/z",
	} {
		got, err := normalizeTarget(in)
		if err != nil || got != want {
			t.Fatalf("%q: %q %v", in, got, err)
		}
	}
	if _, err := normalizeTarget(" "); err == nil {
		t.Fatal("bos hedef hata vermeli")
	}
}
