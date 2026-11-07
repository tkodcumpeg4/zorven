package mailauth

import (
	"bytes"
	"context"
	"crypto"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/base64"
	"net"
	"strings"
	"sync"
	"testing"

	"github.com/emersion/go-msgauth/dkim"
)

// fakeResolver, agsiz sahte DNS cozumleyicisidir. Bilinmeyen ad NXDOMAIN doner.
// hang kumesindeki adlar (ya da hangAll) baglam bitene kadar bekler. Gercek
// cozumleyiciler gibi, baglam zaten bitmisse hemen baglam hatasi doner.
type fakeResolver struct {
	mu      sync.Mutex
	txt     map[string][]string
	errs    map[string]error
	mx      map[string][]*net.MX
	ips     map[string][]net.IPAddr
	ptr     map[string][]string
	hang    map[string]bool
	hangAll bool
	queries []string
}

func newFakeResolver() *fakeResolver {
	return &fakeResolver{
		txt:  map[string][]string{},
		errs: map[string]error{},
		mx:   map[string][]*net.MX{},
		ips:  map[string][]net.IPAddr{},
		ptr:  map[string][]string{},
		hang: map[string]bool{},
	}
}

var (
	errTempDNS = &net.DNSError{Err: "server misbehaving", IsTemporary: true}
	errTimeout = &net.DNSError{Err: "i/o timeout", IsTimeout: true}
)

func nxdomain(name string) error {
	return &net.DNSError{Err: "no such host", Name: name, IsNotFound: true}
}

// begin, sorguyu kaydeder; baglam bitmisse ya da ad takilacaksa hata doner.
func (f *fakeResolver) begin(ctx context.Context, name string) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	n := strings.ToLower(strings.TrimSuffix(name, "."))
	f.mu.Lock()
	f.queries = append(f.queries, n)
	h := f.hangAll || f.hang[n]
	f.mu.Unlock()
	if h {
		<-ctx.Done()
		return "", ctx.Err()
	}
	return n, nil
}

func (f *fakeResolver) LookupTXT(ctx context.Context, name string) ([]string, error) {
	n, err := f.begin(ctx, name)
	if err != nil {
		return nil, err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if e, ok := f.errs[n]; ok {
		return nil, e
	}
	if v, ok := f.txt[n]; ok {
		return v, nil
	}
	return nil, nxdomain(n)
}

func (f *fakeResolver) LookupMX(ctx context.Context, name string) ([]*net.MX, error) {
	n, err := f.begin(ctx, name)
	if err != nil {
		return nil, err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if e, ok := f.errs[n]; ok {
		return nil, e
	}
	if v, ok := f.mx[n]; ok {
		return v, nil
	}
	return nil, nxdomain(n)
}

func (f *fakeResolver) LookupIPAddr(ctx context.Context, host string) ([]net.IPAddr, error) {
	n, err := f.begin(ctx, host)
	if err != nil {
		return nil, err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if e, ok := f.errs[n]; ok {
		return nil, e
	}
	if v, ok := f.ips[n]; ok {
		return v, nil
	}
	return nil, nxdomain(n)
}

func (f *fakeResolver) LookupAddr(ctx context.Context, addr string) ([]string, error) {
	n, err := f.begin(ctx, addr)
	if err != nil {
		return nil, err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if e, ok := f.errs[n]; ok {
		return nil, e
	}
	if v, ok := f.ptr[n]; ok {
		return v, nil
	}
	return nil, nxdomain(n)
}

// queried, adin en az bir kez sorgulanip sorgulanmadigini soyler.
func (f *fakeResolver) queried(name string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, q := range f.queries {
		if q == name {
			return true
		}
	}
	return false
}

// testMsg, From'u verilen basit bir iletidir; extra, ek baslik satirlaridir
// (her biri CRLF ile biter).
func testMsg(from, extra string) []byte {
	return []byte("From: " + from + "\r\n" +
		"To: acme@mail.zorven.app\r\n" +
		"Subject: test iletisi\r\n" +
		"Date: Mon, 02 Jan 2006 15:04:05 +0000\r\n" +
		"Message-ID: <1@example.com>\r\n" +
		extra +
		"\r\n" +
		"govde satiri\r\n")
}

// signed, iletiyi dkim.Sign ile imzalar (baslik ve govde "relaxed").
func signed(t *testing.T, raw []byte, domain, selector string, key crypto.Signer) []byte {
	t.Helper()
	var b bytes.Buffer
	err := dkim.Sign(&b, bytes.NewReader(raw), &dkim.SignOptions{
		Domain:                 domain,
		Selector:               selector,
		Signer:                 key,
		HeaderCanonicalization: dkim.CanonicalizationRelaxed,
		BodyCanonicalization:   dkim.CanonicalizationRelaxed,
	})
	if err != nil {
		t.Fatalf("imzalama: %v", err)
	}
	return b.Bytes()
}

func newEdKey(t *testing.T) ed25519.PrivateKey {
	t.Helper()
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	return priv
}

// edTXT, ed25519 genel anahtarinin DKIM TXT kaydidir.
func edTXT(priv ed25519.PrivateKey) string {
	pub := priv.Public().(ed25519.PublicKey)
	return "v=DKIM1; k=ed25519; p=" + base64.StdEncoding.EncodeToString(pub)
}

// rsaTXT, RSA genel anahtarinin DKIM TXT kaydidir.
func rsaTXT(t *testing.T, k *rsa.PrivateKey) string {
	t.Helper()
	der, err := x509.MarshalPKIXPublicKey(&k.PublicKey)
	if err != nil {
		t.Fatal(err)
	}
	return "v=DKIM1; k=rsa; p=" + base64.StdEncoding.EncodeToString(der)
}
