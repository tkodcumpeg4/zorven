package tlscert

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/pem"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/tkodcumpeg4/zorven/server/store"
	"golang.org/x/crypto/acme/autocert"
)

func serialOf(t *testing.T, c *tls.Certificate) string {
	t.Helper()
	leaf, err := x509.ParseCertificate(c.Certificate[0])
	if err != nil {
		t.Fatal(err)
	}
	return leaf.SerialNumber.String()
}

func TestFileReloader_DegisinceYenilerEskiKorunur(t *testing.T) {
	dir := t.TempDir()
	crt, key := filepath.Join(dir, "server.crt"), filepath.Join(dir, "server.key")
	if err := Generate([]string{"a.test"}, crt, key); err != nil {
		t.Fatal(err)
	}
	first, err := tls.LoadX509KeyPair(crt, key)
	if err != nil {
		t.Fatal(err)
	}
	mgr := NewDualCertManager(&first, "rpshell.app", nil, &mockCertStore{}, "", nil)
	hello := &tls.ClientHelloInfo{ServerName: "x.rpshell.app"}

	r := &FileReloader{CertFile: crt, KeyFile: key, Apply: mgr.SetDefaultCert}
	r.Prime()
	if ok, err := r.Check(false); ok || err != nil {
		t.Fatalf("degisiklik yokken yeniden yuklenmemeli: ok=%v err=%v", ok, err)
	}
	c, _ := mgr.GetCertificate(hello)
	before := serialOf(t, c)

	// Yeni cift yazilir (mtime farki icin bekle).
	time.Sleep(20 * time.Millisecond)
	if err := Generate([]string{"b.test"}, crt, key); err != nil {
		t.Fatal(err)
	}
	ok, err := r.Check(false)
	if err != nil || !ok {
		t.Fatalf("yeni sertifika yuklenmeli: ok=%v err=%v", ok, err)
	}
	c, _ = mgr.GetCertificate(hello)
	after := serialOf(t, c)
	if before == after {
		t.Fatal("GetCertificate yeni sertifikayi dondurmeli")
	}

	// Bozuk / uyumsuz cift: eski korunur.
	time.Sleep(20 * time.Millisecond)
	if err := os.WriteFile(crt, []byte("bozuk"), 0o600); err != nil {
		t.Fatal(err)
	}
	if ok, err := r.Check(false); ok || err == nil {
		t.Fatalf("bozuk cift hata vermeli: ok=%v err=%v", ok, err)
	}
	c, _ = mgr.GetCertificate(hello)
	if serialOf(t, c) != after {
		t.Fatal("bozuk dosyada eski sertifika korunmali")
	}

	// Duzelince (ayni imza degil) tekrar dener ve yukler; force imzayi yok sayar.
	time.Sleep(20 * time.Millisecond)
	if err := Generate([]string{"c.test"}, crt, key); err != nil {
		t.Fatal(err)
	}
	if ok, err := r.Check(false); !ok || err != nil {
		t.Fatalf("duzelen cift yuklenmeli: ok=%v err=%v", ok, err)
	}
	if ok, err := r.Check(true); !ok || err != nil {
		t.Fatalf("force imzaya bakmadan yuklemeli: ok=%v err=%v", ok, err)
	}
}

// memCache, autocert.Cache'in bellek ici sahtesi.
type memCache map[string][]byte

func (m memCache) Get(_ context.Context, k string) ([]byte, error) {
	if v, ok := m[k]; ok {
		return v, nil
	}
	return nil, autocert.ErrCacheMiss
}
func (m memCache) Put(_ context.Context, k string, v []byte) error { m[k] = v; return nil }
func (m memCache) Delete(_ context.Context, k string) error        { delete(m, k); return nil }

func autocertPEM(t *testing.T, host string) []byte {
	t.Helper()
	dir := t.TempDir()
	crt, key := filepath.Join(dir, "c.crt"), filepath.Join(dir, "c.key")
	if err := Generate([]string{host}, crt, key); err != nil {
		t.Fatal(err)
	}
	c, _ := os.ReadFile(crt)
	k, _ := os.ReadFile(key)
	// autocert bicimi: once ozel anahtar, sonra zincir.
	return append(k, c...)
}

func TestEdge_ACMEKapaliYalnizOnbellekten(t *testing.T) {
	st := &mockCertStore{hostnames: map[string]store.Hostname{
		"musteri.example": {ID: "h1", FQDN: "musteri.example", Type: store.HostTypeCustom, Verified: true},
		"yok.example":     {ID: "h2", FQDN: "yok.example", Type: store.HostTypeCustom, Verified: true},
	}}
	cache := memCache{"musteri.example": autocertPEM(t, "musteri.example")}
	def := dummyCert("*.rpshell.app")
	// acmeEmail DOLU olsa bile EnableCacheOnly ACME'yi kapatmali.
	mgr := NewDualCertManager(def, "rpshell.app", nil, st, "a@b.c", cache)
	if mgr.AutocertManager == nil {
		t.Fatal("on kosul: e-posta ile autocert kurulmali")
	}
	mgr.EnableCacheOnly(cache)
	if mgr.AutocertManager != nil {
		t.Fatal("edge'de autocert kapali olmali")
	}

	c, err := mgr.GetCertificate(&tls.ClientHelloInfo{ServerName: "musteri.example"})
	if err != nil || c == nil {
		t.Fatalf("onbellekteki sertifika sunulmali: %v", err)
	}
	if c == def {
		t.Fatal("ozel alan adi platform sertifikasi almamali")
	}
	// Onbellekte yoksa ACME'ye gidilmez, hata doner.
	if _, err := mgr.GetCertificate(&tls.ClientHelloInfo{ServerName: "yok.example"}); err == nil {
		t.Fatal("onbellekte olmayan alan adi hata vermeli")
	}
	// Platform alt alani yine joker (varsayilan) sertifikayi alir.
	if c, err := mgr.GetCertificate(&tls.ClientHelloInfo{ServerName: "t.rpshell.app"}); err != nil || c != def {
		t.Fatalf("joker sertifika sunulmali: %v", err)
	}
}

func TestEdge_OnbellekSuresiGecmisSunulmaz(t *testing.T) {
	// Gecmis tarihli sertifika uret.
	dir := t.TempDir()
	crt, key := filepath.Join(dir, "c.crt"), filepath.Join(dir, "c.key")
	if err := Generate([]string{"eski.example"}, crt, key); err != nil {
		t.Fatal(err)
	}
	cb, _ := os.ReadFile(crt)
	blk, _ := pem.Decode(cb)
	leaf, _ := x509.ParseCertificate(blk.Bytes)
	if !leaf.NotAfter.After(time.Now()) {
		t.Fatal("on kosul")
	}
	c := &tls.Certificate{Leaf: leaf}
	if validNow(c, leaf.NotAfter.Add(time.Hour)) {
		t.Fatal("suresi gecmis sertifika gecerli sayilmamali")
	}
	if !validNow(c, time.Now()) {
		t.Fatal("gecerli sertifika gecerli sayilmali")
	}
}
