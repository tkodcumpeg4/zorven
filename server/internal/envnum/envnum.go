// Package envnum, sayisal ortam degiskenlerini tek kuralla okur (A5 #10).
//
// Neden ortak paket: sunucuda dort ayri yardimci vardi; bir kismi gecersiz degeri
// sessizce varsayilana dusuruyor, bir kismi ust sinir koymuyordu (or.
// ZORVEN_DB_MAX_CONNS=1000000 kabul ediliyordu). Kural: bos -> varsayilan; gecersiz
// ya da [min, max] disi -> WARN + varsayilan (Int) veya hata (IntStrict, yanlis
// degerle calismanin zararli oldugu ayarlar icin).
package envnum

import (
	"fmt"
	"log/slog"
	"strconv"
	"strings"
)

// Int64, get(name) degerini okur. Bos ise def; gecersiz ya da [lo, hi] disi ise
// WARN loglayip def doner.
func Int64(get func(string) string, name string, def, lo, hi int64) int64 {
	n, err := Int64Strict(get, name, def, lo, hi)
	if err != nil {
		slog.Warn("gecersiz sayisal ortam degiskeni, varsayilan kullaniliyor",
			"env", name, "deger", strings.TrimSpace(get(name)), "varsayilan", def, "hata", err)
		return def
	}
	return n
}

// Int, Int64'un int surumu.
func Int(get func(string) string, name string, def, lo, hi int) int {
	return int(Int64(get, name, int64(def), int64(lo), int64(hi)))
}

// Int64Strict, Int64 gibi okur ama gecersiz degerde hata doner (varsayilana dusmez).
func Int64Strict(get func(string) string, name string, def, lo, hi int64) (int64, error) {
	v := strings.TrimSpace(get(name))
	if v == "" {
		return def, nil
	}
	n, err := strconv.ParseInt(v, 10, 64)
	if err != nil {
		return def, fmt.Errorf("%s tamsayi degil: %q", name, v)
	}
	if n < lo || n > hi {
		return def, fmt.Errorf("%s %d..%d araliginda olmali: %d", name, lo, hi, n)
	}
	return n, nil
}

// IntStrict, Int64Strict'in int surumu.
func IntStrict(get func(string) string, name string, def, lo, hi int) (int, error) {
	n, err := Int64Strict(get, name, int64(def), int64(lo), int64(hi))
	return int(n), err
}
