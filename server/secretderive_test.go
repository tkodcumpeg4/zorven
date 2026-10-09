package main

import (
	"bytes"
	"testing"
)

func TestDeriveKey(t *testing.T) {
	m := []byte("ana-secret")
	a := deriveKey(m, keyPurposeVisitor)
	if len(a) != 32 || !bytes.Equal(a, deriveKey(m, keyPurposeVisitor)) {
		t.Fatal("turetme deterministik ve 32 bayt olmali")
	}
	if bytes.Equal(a, deriveKey(m, keyPurposeBasicAuth)) {
		t.Fatal("farkli amac farkli anahtar vermeli")
	}
	if bytes.Equal(a, m) || bytes.Equal(a, deriveKey([]byte("baska"), keyPurposeVisitor)) {
		t.Fatal("anahtar ana secret'a bagli ama ona esit olmamali")
	}
}

func TestResolveVisitorSecret(t *testing.T) {
	if s, w, err := resolveVisitorSecret("x", "zorven.app"); err != nil || w || s != "x" {
		t.Fatal("verilen secret aynen kullanilmali")
	}
	if _, _, err := resolveVisitorSecret("", "zorven.app"); err == nil {
		t.Fatal("uretimde secret yoksa hata beklenir")
	}
	for _, pd := range []string{"", "localhost", "dev.localhost", "x.test"} {
		s, w, err := resolveVisitorSecret("", pd)
		if err != nil || !w || s == "" {
			t.Fatalf("gelistirmede (%q) uyari ile varsayilan beklenir", pd)
		}
	}
}
