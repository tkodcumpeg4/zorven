package tlscert

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"math/big"
	"testing"
	"time"

	"github.com/tkodcumpeg4/zorven/server/store"
)

type mockCertStore struct {
	store.Store
	hostnames map[string]store.Hostname
}

func (m *mockCertStore) GetHostnameByFQDN(ctx context.Context, fqdn string) (store.Hostname, error) {
	if h, ok := m.hostnames[fqdn]; ok {
		return h, nil
	}
	return store.Hostname{}, store.ErrNotFound
}

func dummyCert(name string) *tls.Certificate {
	key, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	serial, _ := rand.Int(rand.Reader, big.NewInt(10000))
	tmpl := x509.Certificate{
		SerialNumber: serial,
		Subject:      pkix.Name{CommonName: name},
		NotBefore:    time.Now().Add(-1 * time.Hour),
		NotAfter:     time.Now().Add(1 * time.Hour),
	}
	der, _ := x509.CreateCertificate(rand.Reader, &tmpl, &tmpl, &key.PublicKey, key)
	return &tls.Certificate{
		Certificate: [][]byte{der},
		PrivateKey:  key,
	}
}

func TestDualCertManager_GetCertificate(t *testing.T) {
	defaultCert := dummyCert("*.rpshell.app")
	mockSt := &mockCertStore{
		hostnames: map[string]store.Hostname{
			"verified.custom.com": {
				ID:       "hst_1",
				FQDN:     "verified.custom.com",
				Type:     store.HostTypeCustom,
				Verified: true,
			},
			"unverified.custom.com": {
				ID:       "hst_2",
				FQDN:     "unverified.custom.com",
				Type:     store.HostTypeCustom,
				Verified: false,
			},
		},
	}

	mgr := NewDualCertManager(defaultCert, "rpshell.app", []string{"localhost", "127.0.0.1"}, mockSt, "", nil)

	// 1. Platform subdomain -> defaultCert
	cert, err := mgr.GetCertificate(&tls.ClientHelloInfo{ServerName: "api.rpshell.app"})
	if err != nil || cert != defaultCert {
		t.Fatalf("platform subdomain defaultCert donmeliydi, got err=%v", err)
	}

	// 2. Control host (localhost) -> defaultCert
	cert, err = mgr.GetCertificate(&tls.ClientHelloInfo{ServerName: "localhost"})
	if err != nil || cert != defaultCert {
		t.Fatalf("control host defaultCert donmeliydi, got err=%v", err)
	}

	// 3. Dogrulanmamis custom domain -> REDDEDILMELI
	_, err = mgr.GetCertificate(&tls.ClientHelloInfo{ServerName: "unverified.custom.com"})
	if err == nil {
		t.Fatalf("dogrulanmamis custom domain handshake hatasi vermeliydi")
	}

	// 4. Veritabaninda olmayan domain -> REDDEDILMELI
	_, err = mgr.GetCertificate(&tls.ClientHelloInfo{ServerName: "unknown.org"})
	if err == nil {
		t.Fatalf("olmayan domain handshake hatasi vermeliydi")
	}

	// 5. Dogrulanmis custom domain -> basarili (dev cert uretilir)
	cert, err = mgr.GetCertificate(&tls.ClientHelloInfo{ServerName: "verified.custom.com"})
	if err != nil || cert == nil {
		t.Fatalf("dogrulanmis custom domain cert donmeliydi, got err=%v", err)
	}
}
