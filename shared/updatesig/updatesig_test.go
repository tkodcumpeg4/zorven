package updatesig

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"testing"
)

func TestVerify(t *testing.T) {
	pub, priv, _ := ed25519.GenerateKey(rand.Reader)
	pub2, _, _ := ed25519.GenerateKey(rand.Reader)
	k1 := base64.StdEncoding.EncodeToString(pub)
	k2 := base64.StdEncoding.EncodeToString(pub2)
	m := []byte(`{"version":"1.0.0"}`)
	sig := []byte(base64.StdEncoding.EncodeToString(ed25519.Sign(priv, m)) + "\n")

	if err := Verify(m, sig, []string{k1}); err != nil {
		t.Fatal(err)
	}
	if err := Verify(m, sig, []string{k2, k1}); err != nil { // anahtar donusu
		t.Fatal(err)
	}
	if Verify(m, sig, []string{k2}) == nil {
		t.Fatal("yanlis anahtar kabul edildi")
	}
	if Verify([]byte(`{"version":"9.9.9"}`), sig, []string{k1}) == nil {
		t.Fatal("degisen manifest kabul edildi")
	}
	if Verify(m, nil, []string{k1}) == nil || Verify(m, []byte("bozuk"), []string{k1}) == nil {
		t.Fatal("eksik/bozuk imza kabul edildi")
	}
	if err := Verify(m, sig, []string{}); !errors.Is(err, ErrNoKeys) {
		t.Fatalf("bos anahtar listesi: %v", err)
	}
}

func TestNewer(t *testing.T) {
	cases := []struct {
		a, b string
		want bool
	}{
		{"0.2.7", "0.2.6", true}, {"0.2.6", "0.2.6", false}, {"0.2.5", "0.2.6", false},
		{"v1.0.0", "0.9.9", true}, {"0.10.0", "0.9.0", true},
	}
	for _, c := range cases {
		if Newer(c.a, c.b) != c.want {
			t.Errorf("Newer(%s,%s) != %v", c.a, c.b, c.want)
		}
	}
}
