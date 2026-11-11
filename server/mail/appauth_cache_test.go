package mail

import (
	"context"
	"errors"
	"testing"

	"github.com/tkodcumpeg4/zorven/server/auth"
)

// K1: basarili uygulama parolasi onbelleklenir (her IMAP baglantisinda argon2
// calismaz); iptal edilen parola onbellekte olsa da aninda reddedilir.
func TestAuthenticator_CacheKeepsRevocationImmediate(t *testing.T) {
	st := newMemMailStore()
	st.addTenant(testTenant, "acme", "pro")
	st.addAppPassword(testTenant, testBox, GenerateAppPassword()) // eslesmeyen ilk parola
	pw := GenerateAppPassword()
	rec := st.addAppPassword(testTenant, testBox, pw)
	a := NewAuthenticator(st, "mail.zorven.app", "zorven.app", []string{"info"}, nil)
	ctx := context.Background()

	for i := range 2 {
		if _, err := a.Authenticate(ctx, "imap", testBox, pw, "7.7.7.7"); err != nil {
			t.Fatalf("%d. giris: %v", i, err)
		}
	}
	if !auth.CachedOK("mail:"+rec.ID, NormalizeAppPassword(pw), rec.PasswordHash) {
		t.Fatal("basarili giris onbellege girmedi")
	}
	st.revokePassword(rec.ID)
	if _, err := a.Authenticate(ctx, "imap", testBox, pw, "7.7.7.7"); !errors.Is(err, ErrAuthFailed) {
		t.Fatalf("iptal edilen parola: %v, beklenen ErrAuthFailed", err)
	}
}

// Dolu argon2 kapisi gecici hatadir; IMAP/SMTP katmani ErrAuthRateLimited gibi ele alir.
func TestErrAuthBusyIsTemporary(t *testing.T) {
	if !errors.Is(ErrAuthBusy, ErrAuthRateLimited) {
		t.Fatal("ErrAuthBusy gecici hata (ErrAuthRateLimited) olarak taninmali")
	}
}
