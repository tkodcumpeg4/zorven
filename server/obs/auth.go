package obs

import (
	"bytes"
	"fmt"
	"sync/atomic"
)

// Basarisiz kimlik dogrulama sayaclari (O16). Etiketler SABIT kumelerdir:
// tur (hangi kimlik turu) x neden. Kiraci, IP ya da token kimligi etiketi
// ASLA yoktur (kardinalite ve sir sizintisi).
var authKindNames = [...]string{"api_token", "client_token", "admin_key", "agent", "basic", "mail", "other"}

var authReasonNames = [...]string{"malformed", "unknown", "invalid", "expired", "rate_limited", "busy", "store_error", "other"}

var authFails [len(authKindNames)][len(authReasonNames)]atomic.Uint64

func authIndex(names []string, v string) int {
	for i, n := range names {
		if n == v {
			return i
		}
	}
	return len(names) - 1 // "other"
}

// CountAuthFailure, basarisiz (ya da gecici olarak yapilamayan) bir kimlik
// dogrulamasini sayar. Bilinmeyen tur/neden "other" kovasina duser.
func CountAuthFailure(kind, reason string) {
	authFails[authIndex(authKindNames[:], kind)][authIndex(authReasonNames[:], reason)].Add(1)
}

// AuthFailures, (tur, neden) sayacinin degeri (test/teshis).
func AuthFailures(kind, reason string) uint64 {
	return authFails[authIndex(authKindNames[:], kind)][authIndex(authReasonNames[:], reason)].Load()
}

func writeAuth(b *bytes.Buffer) {
	head(b, "zorven_auth_failures_total", "Basarisiz kimlik dogrulamalarinin tur ve nedene gore sayisi.", "counter")
	for k, kn := range authKindNames {
		for rs, rn := range authReasonNames {
			fmt.Fprintf(b, "zorven_auth_failures_total{kind=%q,reason=%q} %d\n", kn, rn, authFails[k][rs].Load())
		}
	}
}
