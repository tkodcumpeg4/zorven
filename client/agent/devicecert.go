package agent

// Z4 — Cihaz istemci sertifikasi: anahtar uretimi, CSR, alma, yenileme ve
// durus raporunu imzalama.
//
//   - Ozel anahtar (ECDSA P-256) CIHAZDA uretilir ve ASLA gonderilmez; yalniz
//     CSR (genel anahtar) sunucuya gider. Dosya 0600 (Windows: korumali DACL).
//   - Sertifika kisa omurludur (sunucu vars. 30 gun); omrunun 2/3'u dolunca
//     yenilenir (otomatik).
//   - Sunucu CSR'deki konuyu yok sayar; kimlik oturumdan gelir.

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"encoding/pem"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/tkodcumpeg4/zorven/client/config"
	"github.com/tkodcumpeg4/zorven/shared/protocol"
)

const (
	deviceKeyFile  = "device_key.pem"
	deviceCertFile = "device_cert.pem"
	deviceCAFile   = "device_ca.pem"
	// deviceCertCheck, yenileme denetim araligi; basarisizlikta kisa yeniden deneme.
	deviceCertCheck = 30 * time.Minute
	deviceCertRetry = time.Minute
)

// errCertDenied, kalici red (plan/sunucu anahtari): sik yeniden denenmez.
var errCertDenied = errors.New("sunucu sertifika vermiyor")

// certRequester, CSR'yi sunucuya iletip yaniti dondurur.
type certRequester func(ctx context.Context, csrPEM string, renew bool) (protocol.DeviceCert, error)

type deviceCertManager struct {
	dir      string
	clientID string
	log      *slog.Logger
	request  certRequester
	// onNew, yeni sertifika alininca cagrilir (durus raporu hemen imzali yenilensin).
	onNew func()
	now   func() time.Time

	mu        sync.Mutex
	key       *ecdsa.PrivateKey
	certPEM   string
	notBefore time.Time
	notAfter  time.Time
}

// deviceCertDir, cihaz kimligi dosyalarinin dizini (kullanici yapilandirma dizini altinda).
func deviceCertDir() string {
	if p, err := config.Path(); err == nil {
		return filepath.Join(filepath.Dir(p), "device")
	}
	return filepath.Join(os.TempDir(), "zorven-device")
}

func newDeviceCertManager(dir, clientID string, log *slog.Logger, req certRequester) (*deviceCertManager, error) {
	m := &deviceCertManager{dir: dir, clientID: clientID, log: log, request: req, now: time.Now}
	if err := m.loadOrCreateKey(); err != nil {
		return nil, err
	}
	m.loadCert()
	return m, nil
}

func (m *deviceCertManager) loadOrCreateKey() error {
	path := filepath.Join(m.dir, deviceKeyFile)
	if b, err := os.ReadFile(path); err == nil {
		if blk, _ := pem.Decode(b); blk != nil {
			if k, err := x509.ParseECPrivateKey(blk.Bytes); err == nil && k.Curve == elliptic.P256() {
				m.key = k
				// Eski gevsek izinleri daralt.
				_ = config.WriteSecretFile(path, b)
				return nil
			}
		}
	}
	k, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return err
	}
	der, err := x509.MarshalECPrivateKey(k)
	if err != nil {
		return err
	}
	if err := config.WriteSecretFile(path, pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: der})); err != nil {
		return fmt.Errorf("cihaz anahtari saklanamadi: %w", err)
	}
	m.key = k
	return nil
}

// loadCert, diskteki sertifikayi yukler; anahtarla veya cihaz kimligiyle
// uyusmuyorsa yok sayar (yeniden istenir).
func (m *deviceCertManager) loadCert() {
	b, err := os.ReadFile(filepath.Join(m.dir, deviceCertFile))
	if err != nil {
		return
	}
	if cert, ok := m.parseCert(string(b)); ok {
		m.certPEM, m.notBefore, m.notAfter = string(b), cert.NotBefore, cert.NotAfter
	}
}

func (m *deviceCertManager) parseCert(certPEM string) (*x509.Certificate, bool) {
	blk, _ := pem.Decode([]byte(certPEM))
	if blk == nil {
		return nil, false
	}
	cert, err := x509.ParseCertificate(blk.Bytes)
	if err != nil {
		return nil, false
	}
	pub, ok := cert.PublicKey.(*ecdsa.PublicKey)
	if !ok || !pub.Equal(&m.key.PublicKey) || cert.Subject.CommonName != m.clientID {
		return nil, false
	}
	return cert, true
}

// needsRenewal, sertifika yok / suresi doldu / omrunun 2/3'u gecti.
func (m *deviceCertManager) needsRenewal() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.certPEM == "" {
		return true
	}
	now := m.now()
	life := m.notAfter.Sub(m.notBefore)
	return now.After(m.notBefore.Add(life * 2 / 3))
}

func (m *deviceCertManager) csrPEM() (string, error) {
	der, err := x509.CreateCertificateRequest(rand.Reader, &x509.CertificateRequest{
		Subject: pkix.Name{CommonName: m.clientID},
	}, m.key)
	if err != nil {
		return "", err
	}
	return string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE REQUEST", Bytes: der})), nil
}

// ensure, gerekiyorsa sertifika ister/yeniler.
func (m *deviceCertManager) ensure(ctx context.Context) error {
	if !m.needsRenewal() {
		return nil
	}
	m.mu.Lock()
	renew := m.certPEM != ""
	m.mu.Unlock()
	csr, err := m.csrPEM()
	if err != nil {
		return err
	}
	resp, err := m.request(ctx, csr, renew)
	if err != nil {
		return err
	}
	if resp.Error != "" || resp.CertPEM == "" {
		if resp.ErrorCode == "plan_required" || resp.ErrorCode == "no_key" {
			return fmt.Errorf("%w (%s): %s", errCertDenied, resp.ErrorCode, resp.Error)
		}
		return fmt.Errorf("sunucu sertifika vermedi (%s): %s", resp.ErrorCode, resp.Error)
	}
	cert, ok := m.parseCert(resp.CertPEM)
	if !ok {
		return errors.New("sunucudan gelen sertifika anahtarimizla/kimligimizle uyusmuyor")
	}
	if !cert.NotAfter.After(m.now()) {
		return errors.New("sunucudan gelen sertifikanin suresi dolmus")
	}
	if err := config.WriteSecretFile(filepath.Join(m.dir, deviceCertFile), []byte(resp.CertPEM)); err != nil {
		return err
	}
	if resp.CAPEM != "" {
		_ = config.WriteSecretFile(filepath.Join(m.dir, deviceCAFile), []byte(resp.CAPEM))
	}
	m.mu.Lock()
	m.certPEM, m.notBefore, m.notAfter = resp.CertPEM, cert.NotBefore, cert.NotAfter
	m.mu.Unlock()
	if m.log != nil {
		m.log.Info("cihaz sertifikasi alindi", "bitis", cert.NotAfter.Format(time.RFC3339))
	}
	if m.onNew != nil {
		m.onNew()
	}
	return nil
}

// attest, raporu cihaz anahtariyla imzalar. Gecerli sertifika yoksa nil.
func (m *deviceCertManager) attest(rep protocol.PostureReport, challenge string) *protocol.PostureAttest {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.certPEM == "" || challenge == "" || !m.now().Before(m.notAfter) {
		return nil
	}
	h := sha256.Sum256(rep.SigningBytes(challenge))
	sig, err := ecdsa.SignASN1(rand.Reader, m.key, h[:])
	if err != nil {
		return nil
	}
	return &protocol.PostureAttest{CertPEM: m.certPEM, Sig: base64.StdEncoding.EncodeToString(sig)}
}

// run, baglanti boyunca sertifikayi guncel tutar.
func (m *deviceCertManager) run(ctx context.Context) {
	wait := time.Duration(0)
	for {
		select {
		case <-ctx.Done():
			return
		case <-time.After(wait):
		}
		if err := m.ensure(ctx); err != nil {
			if m.log != nil {
				m.log.Warn("cihaz sertifikasi alinamadi", "hata", err)
			}
			wait = deviceCertRetry
			if errors.Is(err, errCertDenied) {
				wait = 4 * deviceCertCheck
			}
			continue
		}
		wait = deviceCertCheck
	}
}
