package main

import "strings"

// Acik surum: Zorven Apps komutlari (deploy/apps) ticari katmandir. Ozel depodaki
// apps_cmds.go'dan yalniz "tablo hucresi bossa tire" yardimcisi acikta kalir
// (statuscmd.go kullanir).
func dash(s string) string {
	if strings.TrimSpace(s) == "" {
		return "-"
	}
	return s
}
