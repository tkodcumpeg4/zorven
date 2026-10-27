// Package i18n, istemcinin (CLI) kullaniciya gosterdigi metinleri Turkce
// ve Ingilizce sunar (acik surum; Japonca yalniz ticari surumde). Metinler messages.go'da tek tabloda durur: her
// anahtarin uc sutun (tr, en, ja) yan yana yazilir; acik surumde ja sutunu bostur ve Ingilizceye duser ki biri eksik kalmasin (test denetler).
package i18n

import (
	"fmt"
	"os"
	"strings"
	"sync/atomic"
)

// Desteklenen diller.
const (
	TR = "tr"
	EN = "en"
	JA = "ja"
)

// Supported, ayarlar arayuzunde gosterilen sira.
var Supported = []string{TR, EN}

// Name, dilin kendi dilindeki adi.
func Name(lang string) string {
	switch lang {
	case TR:
		return "Türkçe"
	}
	return "English"
}

var current atomic.Value // string

func init() { current.Store(Detect("")) }

// Set, etkin dili degistirir; gecersiz deger yok sayilir.
func Set(lang string) {
	if l := Normalize(lang); l != "" {
		current.Store(l)
	}
}

// Lang, etkin dil.
func Lang() string { return current.Load().(string) }

// Normalize, "tr_TR.UTF-8", "ja-JP", "EN" gibi degerleri tr/en'ya indirger;
// desteklenmeyen ya da bos deger icin "" doner.
func Normalize(v string) string {
	v = strings.ToLower(strings.TrimSpace(v))
	if i := strings.IndexAny(v, "_-.@"); i >= 0 {
		v = v[:i]
	}
	switch v {
	case TR, EN:
		return v
	}
	return ""
}

// Detect, dili secer: ayar dosyasindaki deger > ZORVEN_LANG > LC_ALL >
// LC_MESSAGES > LANG > isletim sistemi (Windows kullanici arayuzu dili) >
// Ingilizce. "C"/"POSIX" yereli dil bildirmez, atlanir.
func Detect(configured string) string {
	if l := Normalize(configured); l != "" {
		return l
	}
	for _, k := range []string{"ZORVEN_LANG", "LC_ALL", "LC_MESSAGES", "LANG"} {
		if v := os.Getenv(k); v != "" {
			if l := Normalize(v); l != "" {
				return l
			}
			if k != "ZORVEN_LANG" && !isNeutralLocale(v) {
				// Desteklenmeyen ama acik bir dil (or. de_DE): Ingilizce.
				return EN
			}
		}
	}
	if l := Normalize(osLanguage()); l != "" {
		return l
	}
	return EN
}

func isNeutralLocale(v string) bool {
	v = strings.ToUpper(strings.TrimSpace(v))
	return v == "C" || v == "POSIX" || strings.HasPrefix(v, "C.")
}

// T, anahtarin etkin dildeki metnini doner; args verilirse fmt.Sprintf uygular.
// Bilinmeyen anahtar oldugu gibi doner (eksik ceviri ekranda gorunur kalsin).
func T(key string, args ...any) string {
	return TL(Lang(), key, args...)
}

// TL, T'nin belirli bir dil icin olani.
func TL(lang, key string, args ...any) string {
	m, ok := messages[key]
	if !ok {
		return key
	}
	s := m[idx(lang)]
	if s == "" {
		s = m[1]
	}
	if len(args) > 0 {
		return fmt.Sprintf(s, args...)
	}
	return s
}

func idx(lang string) int {
	switch lang {
	case TR:
		return 0
	case JA:
		return 2
	}
	return 1
}
