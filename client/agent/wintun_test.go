package agent

import (
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func shaHex(b []byte) string { s := sha256.Sum256(b); return hex.EncodeToString(s[:]) }

// wintunServer, imzali manifest + wintun dosyalari sunar. dllServed, manifestteki
// sha'dan farkli icerik sunmak icin (tahrif simulasyonu) kullanilir.
func wintunServer(t *testing.T, dll, dllServed []byte, priv ed25519.PrivateKey, withEntry bool) *httptest.Server {
	t.Helper()
	lic := []byte("Prebuilt Binaries License")
	files := ""
	if withEntry {
		files = fmt.Sprintf(`"windows/amd64+wintun":{"name":"wintun-amd64.dll","sha256":"%s","size":%d},`+
			`"any+wintun-license":{"name":"wintun-LICENSE.txt","sha256":"%s","size":%d}`,
			shaHex(dll), len(dll), shaHex(lic), len(lic))
	}
	manifest := `{"version":"1.0.0","files":{` + files + `}}`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if serveSigned(w, r, manifest, priv) {
			return
		}
		switch r.URL.Path {
		case "/bin/wintun-amd64.dll":
			w.Write(dllServed)
		case "/bin/wintun-LICENSE.txt":
			w.Write(lic)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

func wintunAgent(srv *httptest.Server, keys []string) *Agent {
	return &Agent{Insecure: true, updateKeys: keys,
		ServerAddr: strings.TrimPrefix(srv.URL, "http://"),
		Log:        slog.New(slog.NewTextHandler(io.Discard, nil))}
}

func TestEnsureWintunInstallsVerified(t *testing.T) {
	pub, priv := newTestKey()
	dll := []byte("MZ-sahte-wintun-dll")
	srv := wintunServer(t, dll, dll, priv, true)
	dir := t.TempDir()
	var verified string
	err := wintunAgent(srv, []string{pub}).ensureWintunIn(context.Background(), dir, "windows", "amd64",
		func(p string) error { verified = p; return nil })
	if err != nil {
		t.Fatalf("beklenmeyen hata: %v", err)
	}
	got, _ := os.ReadFile(filepath.Join(dir, "wintun.dll"))
	if string(got) != string(dll) {
		t.Fatalf("wintun.dll yazilmadi/icerik farkli: %q", got)
	}
	if verified == "" || verified == filepath.Join(dir, "wintun.dll") {
		t.Fatalf("ek dogrulama gecici dosyada calismali, alinan %q", verified)
	}
	if _, err := os.Stat(filepath.Join(dir, "wintun-LICENSE.txt")); err != nil {
		t.Fatalf("lisans metni yazilmadi: %v", err)
	}
	// Gecici dosya kalmamali.
	if m, _ := filepath.Glob(filepath.Join(dir, "wintun-*.tmp")); len(m) != 0 {
		t.Fatalf("gecici dosya kaldi: %v", m)
	}
}

func TestEnsureWintunRejectsWrongSHA(t *testing.T) {
	pub, priv := newTestKey()
	srv := wintunServer(t, []byte("gercek"), []byte("TAHRIF EDILMIS"), priv, true)
	dir := t.TempDir()
	err := wintunAgent(srv, []string{pub}).ensureWintunIn(context.Background(), dir, "windows", "amd64", nil)
	if err == nil || !strings.Contains(err.Error(), "sha256") {
		t.Fatalf("yanlis sha reddedilmeliydi, hata: %v", err)
	}
	if _, serr := os.Stat(filepath.Join(dir, "wintun.dll")); serr == nil {
		t.Fatal("reddedilen dll diske yazildi")
	}
}

func TestEnsureWintunRejectsBadSignature(t *testing.T) {
	_, priv := newTestKey()
	otherPub, _ := newTestKey() // sunucu baska anahtarla imzaliyor
	dll := []byte("dll")
	srv := wintunServer(t, dll, dll, priv, true)
	dir := t.TempDir()
	err := wintunAgent(srv, []string{otherPub}).ensureWintunIn(context.Background(), dir, "windows", "amd64", nil)
	if err == nil || !errors.Is(err, errUpdateSignature) {
		t.Fatalf("imza hatasi bekleniyordu: %v", err)
	}
	if _, serr := os.Stat(filepath.Join(dir, "wintun.dll")); serr == nil {
		t.Fatal("imzasiz manifestle dll yazildi")
	}
}

func TestEnsureWintunVerifyFailureRejects(t *testing.T) {
	pub, priv := newTestKey()
	dll := []byte("dll")
	srv := wintunServer(t, dll, dll, priv, true)
	dir := t.TempDir()
	err := wintunAgent(srv, []string{pub}).ensureWintunIn(context.Background(), dir, "windows", "amd64",
		func(string) error { return errors.New("imza yok") })
	if err == nil || !strings.Contains(err.Error(), "Authenticode") {
		t.Fatalf("Authenticode hatasi bekleniyordu: %v", err)
	}
	if _, serr := os.Stat(filepath.Join(dir, "wintun.dll")); serr == nil {
		t.Fatal("dogrulanamayan dll yerlestirildi")
	}
}

func TestEnsureWintunNotPublishedAndExisting(t *testing.T) {
	pub, priv := newTestKey()
	srv := wintunServer(t, nil, nil, priv, false)
	dir := t.TempDir()
	err := wintunAgent(srv, []string{pub}).ensureWintunIn(context.Background(), dir, "windows", "amd64", nil)
	if err == nil || !strings.Contains(err.Error(), "yayinlanmamis") {
		t.Fatalf("yayinlanmamis hatasi bekleniyordu: %v", err)
	}
	// Mevcut dosya varsa ag hic kullanilmaz.
	os.WriteFile(filepath.Join(dir, "wintun.dll"), []byte("elle"), 0o644)
	srv.Close()
	if err := wintunAgent(srv, []string{pub}).ensureWintunIn(context.Background(), dir, "windows", "amd64", nil); err != nil {
		t.Fatalf("mevcut dll icin hata: %v", err)
	}
}
