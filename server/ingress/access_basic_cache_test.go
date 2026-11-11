package ingress

import (
	"context"
	"testing"

	"github.com/tkodcumpeg4/zorven/server/auth"
)

// K1: Basic parola her istekte argon2'den gecmesin diye basari onbelleklenir;
// parola degisince (yeni hash) eski parola onbellekten KABUL EDILMEZ.
func TestCheckBasicCredsCacheAndRotation(t *testing.T) {
	ctx := context.Background()
	h1, err := auth.HashSecret("eski-parola")
	if err != nil {
		t.Fatal(err)
	}
	cfg := basicConfig{Username: "ali", PasswordHash: h1}
	for i := range 2 {
		if ok, err := checkBasicCreds(ctx, cfg, "ali", "eski-parola"); !ok || err != nil {
			t.Fatalf("%d. deneme ok=%v err=%v", i, ok, err)
		}
	}
	if !auth.CachedOK("basic:ali", "eski-parola", h1) {
		t.Fatal("basarili giris onbellege girmedi")
	}
	if ok, _ := checkBasicCreds(ctx, cfg, "ali", "yanlis"); ok {
		t.Fatal("yanlis parola kabul edildi")
	}
	if ok, _ := checkBasicCreds(ctx, cfg, "veli", "eski-parola"); ok {
		t.Fatal("yanlis kullanici kabul edildi")
	}
	h2, _ := auth.HashSecret("yeni-parola")
	cfg.PasswordHash = h2
	if ok, _ := checkBasicCreds(ctx, cfg, "ali", "eski-parola"); ok {
		t.Fatal("parola degisti, eski parola onbellekten kabul edildi")
	}
	if ok, _ := checkBasicCreds(ctx, cfg, "ali", "yeni-parola"); !ok {
		t.Fatal("yeni parola reddedildi")
	}
}
