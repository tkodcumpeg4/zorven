package store

import (
	"strings"
	"testing"
)

func TestValidateMailFolderName(t *testing.T) {
	ok := []string{"Arsiv", "Is/Musteri", "Çalışma/İş", "a b", strings.Repeat("a", 200)}
	for _, n := range ok {
		if err := ValidateMailFolderName(n); err != nil {
			t.Errorf("%q gecerli olmali: %v", n, err)
		}
	}
	bad := []string{"", "a\x00", "a\tb", "a\x7f", "a*", "a%", "a//b", "/a", "a/", ".", "a/../b",
		" a", "a /b", "INBOX", "inbox/x", "Sent", "TRASH/y", "drafts", strings.Repeat("a", 201), "a\xffb"}
	for _, n := range bad {
		if err := ValidateMailFolderName(n); err == nil {
			t.Errorf("%q gecersiz olmali", n)
		}
	}
}

func TestMailFolderHelpers(t *testing.T) {
	if got := strings.Join(MailFolderParents("a/b/c"), ","); got != "a,a/b" {
		t.Fatalf("parents: %s", got)
	}
	if len(MailFolderParents("a")) != 0 {
		t.Fatal("tek bilesen ust klasor icermez")
	}
	if n, ok := MailUserFolderName(MailUserFolderKey("x/y")); !ok || n != "x/y" {
		t.Fatalf("anahtar turu: %q %v", n, ok)
	}
	if _, ok := MailUserFolderName("inbox"); ok {
		t.Fatal("sistem klasoru kullanici klasoru sayilmamali")
	}
}
