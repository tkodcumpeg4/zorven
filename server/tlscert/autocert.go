package tlscert

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"fmt"
	"math/big"
	"strings"
	"sync"
	"time"

	"github.com/tkodcumpeg4/zorven/server/ingress"
	"github.com/tkodcumpeg4/zorven/server/store"
	"golang.org/x/crypto/acme/autocert"
)

// DualCertManager, platform sertifikasi ile on-demand custom domain sertifikalarini
// tek bir GetCertificate icinde birlestirir.
type DualCertManager struct {
	DefaultCert     *tls.Certificate
	PlatformDomain  string
	ControlHosts    []string
	Store           store.Store
	AutocertManager *autocert.Manager

	mu       sync.RWMutex
	devCerts map[string]*tls.Certificate
}

func NewDualCertManager(defaultCert *tls.Certificate, platformDomain string, controlHosts []string, st store.Store, acmeEmail string, cache autocert.Cache) *DualCertManager {
	m := &DualCertManager{
		DefaultCert:    defaultCert,
		PlatformDomain: strings.ToLower(strings.TrimSuffix(platformDomain, ".")),
		ControlHosts:   controlHosts,
		Store:          st,
		devCerts:       make(map[string]*tls.Certificate),
	}

	if acmeEmail != "" {
		m.AutocertManager = &autocert.Manager{
			Prompt:     autocert.AcceptTOS,
			HostPolicy: m.HostPolicy,
			Email:      acmeEmail,
			Cache:      cache,
		}
	}

	return m
}

// HostPolicy, autocert'in yalnizca veritabaninda dogrulanmis custom domainler icin
// sertifika talep etmesini saglar.
func (m *DualCertManager) HostPolicy(ctx context.Context, host string) error {
	host = strings.ToLower(strings.TrimSuffix(host, "."))
	if m.Store == nil {
		return fmt.Errorf("store tanimli degil")
	}
	h, err := m.Store.GetHostnameByFQDN(ctx, host)
	if err != nil || !h.Verified {
		return fmt.Errorf("alan adi dogrulanmadi: %s", host)
	}
	return nil
}

// GetCertificate, TLS el sikismasinda SNI'a gore dogru sertifikayi doner.
func (m *DualCertManager) GetCertificate(hello *tls.ClientHelloInfo) (*tls.Certificate, error) {
	sni := strings.ToLower(strings.TrimSuffix(strings.TrimSpace(hello.ServerName), "."))

	// 1. SNI yoksa, IP ise veya kontrol hostlarindan biri ise varsayilan sertifika:
	if sni == "" || ingress.IsIPHost(sni) || ingress.HostMatchesAny(sni, m.ControlHosts) {
		return m.DefaultCert, nil
	}

	// 2. Platform domaini veya platform alt alani ise varsayilan (wildcard) sertifika:
	if m.PlatformDomain != "" {
		if sni == m.PlatformDomain || strings.HasSuffix(sni, "."+m.PlatformDomain) {
			return m.DefaultCert, nil
		}
	}

	// 3. Aksi halde bu bir CUSTOM DOMAIN'dir:
	if m.Store == nil {
		return nil, fmt.Errorf("custom domain sertifikasi icin store bulunamadi")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	h, err := m.Store.GetHostnameByFQDN(ctx, sni)
	if err != nil || !h.Verified {
		return nil, fmt.Errorf("ozel alan adi bulunamadi veya henuz dogrulanmadi: %s", sni)
	}

	// 4. ACME manager varsa gercek Let's Encrypt sertifikasi uret/al:
	if m.AutocertManager != nil {
		return m.AutocertManager.GetCertificate(hello)
	}

	// 5. ACME email yapilandirilmamissa (or. lokal test veya gelistirme):
	// On-the-fly self-signed sertifika uret ve onbellekle.
	return m.getOrCreateDevCert(sni)
}

func (m *DualCertManager) getOrCreateDevCert(host string) (*tls.Certificate, error) {
	m.mu.RLock()
	if c, ok := m.devCerts[host]; ok {
		m.mu.RUnlock()
		return c, nil
	}
	m.mu.RUnlock()

	m.mu.Lock()
	defer m.mu.Unlock()

	if c, ok := m.devCerts[host]; ok {
		return c, nil
	}

	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, err
	}
	serial, _ := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	now := time.Now()
	tmpl := x509.Certificate{
		SerialNumber: serial,
		Subject: pkix.Name{
			Organization: []string{"Reverse Proxy Shell Custom Domain (Dev)"},
			CommonName:   host,
		},
		NotBefore:             now.Add(-1 * time.Hour),
		NotAfter:              now.Add(30 * 24 * time.Hour),
		KeyUsage:              x509.KeyUsageDigitalSignature,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		BasicConstraintsValid: true,
		DNSNames:              []string{host},
	}

	der, err := x509.CreateCertificate(rand.Reader, &tmpl, &tmpl, &key.PublicKey, key)
	if err != nil {
		return nil, err
	}

	cert := &tls.Certificate{
		Certificate: [][]byte{der},
		PrivateKey:  key,
	}
	m.devCerts[host] = cert
	return cert, nil
}
