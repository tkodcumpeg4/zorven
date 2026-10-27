package main

import (
	"os"
	"regexp"
	"testing"

	"github.com/tkodcumpeg4/zorven/client/i18n"
)

// Mevcut testler Turkce metinlere bakar; dil sabitlenir.
func TestMain(m *testing.M) {
	i18n.Set(i18n.TR)
	os.Exit(m.Run())
}

var verbRe = regexp.MustCompile(`%[-+# 0-9.]*[a-zA-Z]`)

func TestDesktopMessagesComplete(t *testing.T) {
	for k, m := range dmessages {
		for i, s := range m {
			if s == "" && i != 2 { // acik surumde ja sutunu bostur
				t.Errorf("%s: dil %d bos", k, i)
			}
		}
		base := verbRe.FindAllString(m[0], -1)
		for i := 1; i < 3; i++ {
			if m[i] == "" {
				continue
			}
			got := verbRe.FindAllString(m[i], -1)
			if len(got) != len(base) {
				t.Errorf("%s: bicim fiilleri farkli: %v / %v", k, base, got)
				continue
			}
			for j := range got {
				if got[j] != base[j] {
					t.Errorf("%s: fiil sirasi farkli: %v / %v", k, base, got)
				}
			}
		}
	}
}

func TestTmLanguages(t *testing.T) {
	if got := tml("en", "tray.quit"); got != "Quit" {
		t.Errorf("en: %q", got)
	}
	if got := tml("ja", "tray.quit"); got != "Quit" { // ja yok: Ingilizceye duser
		t.Errorf("ja: %q", got)
	}
	if got := tml("tr", "upd.httpFail", 500); got != "sürüm bilgisi alınamadı (HTTP 500)" {
		t.Errorf("tr: %q", got)
	}
	if tml("en", "yok.anahtar") != "yok.anahtar" {
		t.Error("bilinmeyen anahtar oldugu gibi donmeli")
	}
}

func TestSetLanguagePersists(t *testing.T) {
	useTempConfigDir(t)
	t.Cleanup(func() { i18n.Set(i18n.TR) })
	a := &App{cfg: defaultConfig()}
	if msg := a.SetLanguage("xx"); msg == "" {
		t.Fatal("gecersiz dil kabul edildi")
	}
	if msg := a.SetLanguage("en"); msg != "" {
		t.Fatal(msg)
	}
	if i18n.Lang() != "en" || a.GetLanguage().Setting != "en" {
		t.Fatalf("dil uygulanmadi: %s %+v", i18n.Lang(), a.GetLanguage())
	}
	cfg, err := loadConfig()
	if err != nil || cfg.Language != "en" {
		t.Fatalf("kaydedilmedi: %+v %v", cfg, err)
	}
	// SaveConfig dili ezmemeli.
	a.SaveConfig(Config{ServerAddr: "x:443"})
	if a.GetLanguage().Setting != "en" {
		t.Error("SaveConfig dil ayarini sildi")
	}
	// Bos = otomatik.
	t.Setenv("ZORVEN_LANG", "en")
	if msg := a.SetLanguage(""); msg != "" {
		t.Fatal(msg)
	}
	if i18n.Lang() != "en" || a.GetLanguage().Setting != "" {
		t.Errorf("otomatik: %s %+v", i18n.Lang(), a.GetLanguage())
	}
}
