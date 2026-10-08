package store

import "strings"

// Marka koruma kurallari (kimlik avi / taklit onleme).
//
// IKI katman vardir:
//
//  1. TAM EŞLEŞME: reserved_names tablosu (migration 0004, 0055, 0066...).
//     Kisa ad (ad.<platform>) alirken Store.IsReservedName ile bakilir.
//  2. DESEN: asagidaki BrandKeywords'ten HERHANGI BIRINI iceren etiket
//     ("zorven-login", "myzorven", "zorvenpay", "z-o-r-v-e-n" degil ama
//     "zor-ven" evet: tireler yok sayilir) su yerlerde reddedilir:
//     - kisa ad (platform yoneticisi muaf),
//     - kiraci kapsamli adin <ad> kismi (ad--kiraci),
//     - kiraci/organizasyon slug'i.
//
// YENI BIR MARKA SOZCUGU EKLEMEK icin:
//   - Desen olarak engellenecekse BrandKeywords'e ekleyin (kucuk harf, tiresiz).
//   - Tam eslesme olarak engellenecekse yeni bir migration ile reserved_names'e
//     satir ekleyin (INSERT ... ON CONFLICT (name) DO NOTHING).
//   - web/server/lib/brand.ts icindeki BRAND_KEYWORDS listesini de guncelleyin
//     (Better Auth organizasyon slug'lari orada denetlenir).
//
// Mevcut kayitlar silinmez; kural yalnizca YENI olusturmalarda uygulanir.
var BrandKeywords = []string{"zorven", "rpshell"}

// ContainsBrandKeyword, etiketin bir marka sozcugu icerip icermedigini soyler.
// Karsilastirma kucuk harfle ve tireler atilarak yapilir ("zor-ven" yakalanir).
func ContainsBrandKeyword(label string) bool {
	l := strings.ReplaceAll(strings.ToLower(label), "-", "")
	for _, k := range BrandKeywords {
		if strings.Contains(l, k) {
			return true
		}
	}
	return false
}

// SanitizeGeneratedSlug, OTOMATIK uretilen (kullanicinin secmedigi) bir slug'i
// marka/rezerve kurallarina uydurur: kayit akisi hata vermesin diye reddetmek
// yerine donusturur. Marka sozcukleri cikarilir (bos kalirsa "user"), rezerve
// tam eslesme ise suffix() eklenir.
func SanitizeGeneratedSlug(slug string, isReserved func(string) bool, suffix func() string) string {
	s := strings.ToLower(strings.TrimSpace(slug))
	for ContainsBrandKeyword(s) {
		before := s
		for _, k := range BrandKeywords {
			s = strings.ReplaceAll(s, k, "")
		}
		if s == before {
			// Tireyle bolunmus sozcuk ("zor-ven"): tireleri at ve tekrar dene.
			s = strings.ReplaceAll(s, "-", "")
		}
	}
	s = strings.Trim(s, "-")
	if s == "" {
		s = "user"
	}
	if SlugReservedReason(s, isReserved) != "" {
		s += "-" + suffix()
	}
	return s
}

// SlugReservedReason, kiraci/organizasyon slug'i olarak KULLANILAMAYACAK bir ad
// icin kisa bir neden ("brand" | "reserved") doner; kullanilabilirse "".
// isReserved, tam eslesme (reserved_names) denetimidir; nil olabilir.
func SlugReservedReason(slug string, isReserved func(string) bool) string {
	s := strings.ToLower(strings.TrimSpace(slug))
	if s == DefaultTenantSlug {
		return "" // platform kiracisi
	}
	if ContainsBrandKeyword(s) {
		return "brand"
	}
	if isReserved != nil && isReserved(s) {
		return "reserved"
	}
	return ""
}
