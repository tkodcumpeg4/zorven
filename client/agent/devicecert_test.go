package agent

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
	"math/big"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/tkodcumpeg4/zorven/shared/protocol"
)

// fakeCA, sunucunun rolunu oynar: CSR'den (yalniz genel anahtar) sertifika verir.
type fakeCA struct {
	cert *x509.Certificate
	key  *ecdsa.PrivateKey
	pem  string
	ttl  time.Duration
	n    int
	cn   string // bos degilse CN'i zorlar (kimlik uyusmazligi testi)
}

func newFakeCA(t *testing.T) *fakeCA {
	t.Helper()
	k, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	tpl := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "CA"},
		NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(24 * time.Hour * 365),
		IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign}
	der, err := x509.CreateCertificate(rand.Reader, tpl, tpl, &k.PublicKey, k)
	if err != nil {
		t.Fatal(err)
	}
	c, _ := x509.ParseCertificate(der)
	return &fakeCA{cert: c, key: k, ttl: 30 * 24 * time.Hour,
		pem: string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}))}
}

func (f *fakeCA) request(clientID string) certRequester {
	return func(_ context.Context, csrPEM string, _ bool) (protocol.DeviceCert, error) {
		f.n++
		blk, _ := pem.Decode([]byte(csrPEM))
		csr, err := x509.ParseCertificateRequest(blk.Bytes)
		if err != nil || csr.CheckSignature() != nil {
			return protocol.DeviceCert{Error: "bad csr", ErrorCode: "bad_csr"}, nil
		}
		cn := clientID
		if f.cn != "" {
			cn = f.cn
		}
		tpl := &x509.Certificate{SerialNumber: big.NewInt(int64(100 + f.n)), Subject: pkix.Name{CommonName: cn},
			NotBefore: time.Now().Add(-time.Minute), NotAfter: time.Now().Add(f.ttl),
			ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth}}
		der, err := x509.CreateCertificate(rand.Reader, tpl, f.cert, csr.PublicKey, f.key)
		if err != nil {
			return protocol.DeviceCert{}, err
		}
		return protocol.DeviceCert{
			CertPEM: string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})),
			CAPEM:   f.pem, NotAfter: tpl.NotAfter.Unix(),
		}, nil
	}
}

func TestDeviceCertIssueAttestRenew(t *testing.T) {
	dir := t.TempDir()
	ca := newFakeCA(t)
	m, err := newDeviceCertManager(dir, "cli_1", nil, ca.request("cli_1"))
	if err != nil {
		t.Fatal(err)
	}
	kicked := 0
	m.onNew = func() { kicked++ }

	// Anahtar dosyasi yalniz sahibine acik.
	if runtime.GOOS != "windows" {
		st, err := os.Stat(filepath.Join(dir, deviceKeyFile))
		if err != nil || st.Mode().Perm() != 0o600 {
			t.Fatalf("anahtar dosyasi 0600 olmali: %v %v", st, err)
		}
	}
	rep := protocol.PostureReport{OSName: "linux", OSVersion: "22.04", DiskEncrypted: "yes"}
	if m.attest(rep, "ch") != nil {
		t.Fatal("sertifika yokken imza uretilmemeli")
	}
	if !m.needsRenewal() {
		t.Fatal("sertifika yok: istenmeli")
	}
	if err := m.ensure(context.Background()); err != nil {
		t.Fatal(err)
	}
	if kicked != 1 || ca.n != 1 {
		t.Fatalf("kicked=%d n=%d", kicked, ca.n)
	}
	if m.needsRenewal() {
		t.Fatal("yeni sertifika yenilenmemeli")
	}
	// Imza sertifikanin genel anahtariyla dogrulanir.
	at := m.attest(rep, "ch")
	if at == nil {
		t.Fatal("imza yok")
	}
	blk, _ := pem.Decode([]byte(at.CertPEM))
	cert, _ := x509.ParseCertificate(blk.Bytes)
	sig, _ := base64.StdEncoding.DecodeString(at.Sig)
	h := sha256.Sum256(rep.SigningBytes("ch"))
	if !ecdsa.VerifyASN1(cert.PublicKey.(*ecdsa.PublicKey), h[:], sig) {
		t.Fatal("imza dogrulanamadi")
	}
	h2 := sha256.Sum256(rep.SigningBytes("baska-oturum"))
	if ecdsa.VerifyASN1(cert.PublicKey.(*ecdsa.PublicKey), h2[:], sig) {
		t.Fatal("imza baska challenge ile gecmemeli")
	}

	// Yeniden baslatma: diskten yuklenir, yeniden istenmez.
	m2, err := newDeviceCertManager(dir, "cli_1", nil, ca.request("cli_1"))
	if err != nil {
		t.Fatal(err)
	}
	if m2.needsRenewal() {
		t.Fatal("diskteki gecerli sertifika kullanilmali")
	}
	// Omrun 2/3'u gecince yenilenir.
	m2.now = func() time.Time { return time.Now().Add(21 * 24 * time.Hour) }
	if !m2.needsRenewal() {
		t.Fatal("omrun 2/3'u gecti: yenilenmeli")
	}
	if err := m2.ensure(context.Background()); err != nil || ca.n != 2 {
		t.Fatalf("yenileme: %v n=%d", err, ca.n)
	}
	// Baska cihaz kimligiyle yeniden baslatma: eski sertifika reddedilir.
	m3, _ := newDeviceCertManager(dir, "cli_BASKA", nil, ca.request("cli_BASKA"))
	if !m3.needsRenewal() {
		t.Fatal("kimlik uyusmuyor: yeni sertifika istenmeli")
	}
}

func TestDeviceCertRejectsMismatchedServerCert(t *testing.T) {
	ca := newFakeCA(t)
	ca.cn = "baska_cihaz"
	m, _ := newDeviceCertManager(t.TempDir(), "cli_1", nil, ca.request("cli_1"))
	if err := m.ensure(context.Background()); err == nil {
		t.Fatal("kimligi uyusmayan sertifika kabul edilmemeli")
	}
	if m.attest(protocol.PostureReport{}, "ch") != nil {
		t.Fatal("reddedilen sertifikayla imza uretilmemeli")
	}
}

func TestDeviceCertServerDenied(t *testing.T) {
	m, _ := newDeviceCertManager(t.TempDir(), "cli_1", nil,
		func(context.Context, string, bool) (protocol.DeviceCert, error) {
			return protocol.DeviceCert{Error: "plan", ErrorCode: "plan_required"}, nil
		})
	err := m.ensure(context.Background())
	if err == nil || !isDenied(err) {
		t.Fatalf("kalici red bekleniyordu: %v", err)
	}
}

func isDenied(err error) bool { return err != nil && errors.Is(err, errCertDenied) }
