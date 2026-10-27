package i18n

import (
	"regexp"
	"testing"
)

var verbRe = regexp.MustCompile(`%[-+# 0-9.]*[a-zA-Z%]`)

// Her anahtarin uc dili dolu olmali ve bicim fiilleri ayni sirada bulunmali.
func TestMessagesComplete(t *testing.T) {
	for k, v := range messages {
		for i, s := range v {
			if s == "" && i != 2 { // acik surumde ja sutunu bostur
				t.Errorf("%s: dil %d bos", k, i)
			}
		}
		want := verbRe.FindAllString(v[1], -1)
		for i, s := range v {
			if s == "" {
				continue
			}
			got := verbRe.FindAllString(s, -1)
			if len(got) != len(want) {
				t.Errorf("%s: dil %d fiilleri %v, en %v", k, i, got, want)
				continue
			}
			for j := range got {
				if got[j] != want[j] {
					t.Errorf("%s: dil %d fiil sirasi %v, en %v", k, i, got, want)
				}
			}
		}
	}
}

func TestDetect(t *testing.T) {
	for _, k := range []string{"ZORVEN_LANG", "LC_ALL", "LC_MESSAGES", "LANG"} {
		t.Setenv(k, "")
	}
	if got := Detect("en"); got != EN {
		t.Fatalf("ayar: %s", got)
	}
	t.Setenv("LANG", "tr_TR.UTF-8")
	if got := Detect(""); got != TR {
		t.Fatalf("LANG tr: %s", got)
	}
	t.Setenv("LANG", "de_DE.UTF-8")
	if got := Detect(""); got != EN {
		t.Fatalf("LANG de: %s", got)
	}
	t.Setenv("LANG", "C.UTF-8")
	t.Setenv("ZORVEN_LANG", "tr_TR")
	if got := Detect(""); got != TR {
		t.Fatalf("ZORVEN_LANG: %s", got)
	}
}

func TestTFallback(t *testing.T) {
	if got := TL(JA, "set.version", "1"); got != TL(EN, "set.version", "1") { // bos ja sutunu Ingilizceye duser
		t.Fatal(got)
	}
	if got := TL(TR, "yok.anahtar"); got != "yok.anahtar" {
		t.Fatal(got)
	}
}
