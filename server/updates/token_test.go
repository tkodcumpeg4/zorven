package updates

import (
	"encoding/hex"
	"net/url"
	"strings"
	"testing"
	"time"
)

func TestUnsubTokenExpiry(t *testing.T) {
	now := time.Now()
	tok := UnsubToken(testKey, "user_e")
	if id, st := ParseUnsubTokenStatus(testKey, tok, now); st != TokenOK || id != "user_e" {
		t.Fatalf("yeni jeton: %q %v", id, st)
	}
	// vars. omur 365 gun: 364. gun gecerli, 366. gun dolmus
	if _, st := ParseUnsubTokenStatus(testKey, tok, now.Add(364*24*time.Hour)); st != TokenOK {
		t.Fatalf("364. gun: %v", st)
	}
	if id, st := ParseUnsubTokenStatus(testKey, tok, now.Add(366*24*time.Hour)); st != TokenExpired || id != "user_e" {
		t.Fatalf("366. gun: %q %v", id, st)
	}
	if _, ok := ParseUnsubToken(testKey, UnsubTokenAt(testKey, "u", now.Add(-48*time.Hour), time.Hour)); ok {
		t.Fatal("suresi dolmus jeton kabul edildi")
	}
	// exp alani oynanirsa imza tutmaz
	parts := strings.Split(tok, ".")
	forged := parts[0] + "." + strings.Split(UnsubTokenAt(testKey, "user_e", now, 1000*24*time.Hour), ".")[1] + "." + parts[2]
	if _, st := ParseUnsubTokenStatus(testKey, forged, now); st != TokenInvalid {
		t.Fatalf("exp degistirilmis jeton: %v", st)
	}
}

func TestUnsubTokenTTLEnv(t *testing.T) {
	t.Setenv("ZORVEN_UNSUB_TOKEN_DAYS", "7")
	if UnsubTokenTTL() != 7*24*time.Hour {
		t.Fatal("ZORVEN_UNSUB_TOKEN_DAYS uygulanmadi")
	}
	t.Setenv("ZORVEN_UNSUB_TOKEN_DAYS", "bozuk")
	if UnsubTokenTTL() != 365*24*time.Hour {
		t.Fatal("gecersiz deger varsayilana dusmedi")
	}
}

func TestUnsubLegacyTokenWindow(t *testing.T) {
	t.Setenv("ZORVEN_UNSUB_LEGACY_UNTIL", "2030-01-01")
	old := legacyUnsubToken(testKey, "user_l")
	if id, st := ParseUnsubTokenStatus(testKey, old, time.Date(2029, 12, 31, 0, 0, 0, 0, time.UTC)); st != TokenOK || id != "user_l" {
		t.Fatalf("pencere icinde eski jeton: %q %v", id, st)
	}
	if _, st := ParseUnsubTokenStatus(testKey, old, time.Date(2030, 1, 1, 0, 0, 1, 0, time.UTC)); st != TokenExpired {
		t.Fatalf("kesimden sonra eski jeton: %v", st)
	}
	if LegacyCutoff().Year() != 2030 {
		t.Fatal("kesim tarihi okunmadi")
	}
	t.Setenv("ZORVEN_UNSUB_LEGACY_UNTIL", "")
	if !LegacyCutoff().Equal(DefaultLegacyCutoff) {
		t.Fatal("varsayilan kesim")
	}
}

func TestSiteUnsubExpiredAndLegacy(t *testing.T) {
	f := newFake()
	f.sub["user_x"] = true
	_, h := siteWith(f)

	// suresi dolmus: GET ve POST abonelik degistirmez, dostca sayfa + panel baglantisi (tr/en/ja)
	rawExp := UnsubTokenAt(testKey, "user_x", time.Now().Add(-48*time.Hour), time.Hour)
	exp := url.QueryEscape(rawExp)
	for lang, want := range map[string]string{"tr": "süresi dolmuş", "en": "has expired", "ja": "有効期限", "zh": "已过期"} {
		rec := get(h, "GET", "/u/announcements?token="+exp+"&lang="+lang)
		if rec.Code != 410 || !strings.Contains(rec.Body.String(), want) || !strings.Contains(rec.Body.String(), `href="/profile"`) {
			t.Fatalf("%s GET: %d %s", lang, rec.Code, rec.Body.String())
		}
	}
	rec := post(h, "/u/announcements?lang=en", url.Values{"token": {rawExp}, "List-Unsubscribe": {"One-Click"}}.Encode())
	if rec.Code != 410 || !f.sub["user_x"] {
		t.Fatalf("suresi dolmus tek tik POST: %d", rec.Code)
	}

	// eski jeton pencere icinde: GET onay sayfasi, tek tik POST calisir
	t.Setenv("ZORVEN_UNSUB_LEGACY_UNTIL", time.Now().Add(24*time.Hour).Format(time.RFC3339))
	old := legacyUnsubToken(testKey, "user_x")
	if rec := get(h, "GET", "/u/announcements?token="+url.QueryEscape(old)); rec.Code != 200 || !f.sub["user_x"] {
		t.Fatalf("eski jeton GET: %d", rec.Code)
	}
	if rec := post(h, "/u/announcements", url.Values{"token": {old}, "List-Unsubscribe": {"One-Click"}}.Encode()); rec.Code != 200 || f.sub["user_x"] {
		t.Fatalf("eski jeton tek tik: %d", rec.Code)
	}
	// pencere kapaninca reddedilir
	f.sub["user_x"] = true
	t.Setenv("ZORVEN_UNSUB_LEGACY_UNTIL", time.Now().Add(-time.Hour).Format(time.RFC3339))
	if rec := post(h, "/u/announcements", url.Values{"token": {old}, "List-Unsubscribe": {"One-Click"}}.Encode()); rec.Code != 410 || !f.sub["user_x"] {
		t.Fatalf("kesimden sonra eski jeton: %d", rec.Code)
	}
}

// TestUnsubTokenVektoru: Node tarafinin (web/server/lib/unsub-token.ts) urettigi jetonla
// BIREBIR ayni cikti. Deneme teklifi e-postasi jetonu web servisinde uretir, /u/announcements
// burada dogrular; iki taraf sapmamali. Ayni vektor web/server/lib/unsub-token.test.ts ve
// server/secretderive_test.go (anahtar turetme) icinde de sabitlidir.
func TestUnsubTokenVektoru(t *testing.T) {
	key, err := hex.DecodeString("9ecb9492c6225e73cb7f9900636c84acef04a36b469910417d5c6bf5e2869910")
	if err != nil {
		t.Fatal(err)
	}
	now := time.Unix(1_800_000_000, 0)
	const want = "dXNyXzhmM2ExYw.MTgzMTUzNjAwMA.SkM-jsMN_FwvuSEMFVyQZESPBLkfKhWgzzj7uhc6le4"
	got := UnsubTokenAt(key, "usr_8f3a1c", now, 365*24*time.Hour)
	if got != want {
		t.Fatalf("jeton vektoru sapti:\n got %s\nwant %s", got, want)
	}
	// Node'un urettigi jeton burada gecerli sayilir (suresi icinde).
	if id, st := ParseUnsubTokenStatus(key, want, now.Add(24*time.Hour)); st != TokenOK || id != "usr_8f3a1c" {
		t.Fatalf("vektor jeton dogrulanamadi: %q %v", id, st)
	}
}
