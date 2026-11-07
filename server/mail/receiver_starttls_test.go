package mail

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"math/big"
	"net"
	"net/smtp"
	"testing"
	"time"
)

// testMailCert, testler icin mail.zorven.app adina kisa omurlu oz-imzali sertifika uretir.
func testMailCert(t *testing.T) tls.Certificate {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: "mail.zorven.app"},
		DNSNames:     []string{"mail.zorven.app"},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	return tls.Certificate{Certificate: [][]byte{der}, PrivateKey: key}
}

// serveInbound, uretimdeki kurucuyla (NewServerLocalAuth) gelen sunucuyu acar.
func serveInbound(t *testing.T, tlsCfg *tls.Config) string {
	t.Helper()
	srv := NewServerLocalAuth(NewLocal(nil, "mail.zorven.app", "zorven.app", nil), "", nil)
	srv.TLSConfig = tlsCfg
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	go srv.Serve(ln)
	t.Cleanup(func() { srv.Close() })
	return ln.Addr().String()
}

// MTA-STS "enforce" yayinlandiginda MX'in STARTTLS sunmasi sarttir: TLS
// yapilandirmasi verilen gelen sunucu EHLO'da STARTTLS duyurur ve el sikisir.
func TestInboundAdvertisesSTARTTLS(t *testing.T) {
	cert := testMailCert(t)
	addr := serveInbound(t, &tls.Config{MinVersion: tls.VersionTLS12, Certificates: []tls.Certificate{cert}})
	c, err := smtp.Dial(addr)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	if err := c.Hello("gonderen.example"); err != nil {
		t.Fatal(err)
	}
	if ok, _ := c.Extension("STARTTLS"); !ok {
		t.Fatal("STARTTLS duyurulmadi")
	}
	leaf, err := x509.ParseCertificate(cert.Certificate[0])
	if err != nil {
		t.Fatal(err)
	}
	pool := x509.NewCertPool()
	pool.AddCert(leaf)
	if err := c.StartTLS(&tls.Config{ServerName: "mail.zorven.app", RootCAs: pool}); err != nil {
		t.Fatalf("STARTTLS el sikismasi: %v", err)
	}
	if st, ok := c.TLSConnectionState(); !ok || !st.HandshakeComplete {
		t.Fatal("TLS kurulmadi")
	}
	// TLS sonrasi oturum calismaya devam eder.
	if err := c.Noop(); err != nil {
		t.Fatalf("TLS sonrasi NOOP: %v", err)
	}
}

// TLS yapilandirmasi yoksa (gelistirme) STARTTLS duyurulmaz.
func TestInboundWithoutTLSConfigNoSTARTTLS(t *testing.T) {
	addr := serveInbound(t, nil)
	c, err := smtp.Dial(addr)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	if err := c.Hello("gonderen.example"); err != nil {
		t.Fatal(err)
	}
	if ok, _ := c.Extension("STARTTLS"); ok {
		t.Fatal("TLS yapilandirmasi yokken STARTTLS duyuruldu")
	}
}
