package agent

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"
)

func newTestKey() (pubB64 string, priv ed25519.PrivateKey) {
	pub, priv, _ := ed25519.GenerateKey(rand.Reader)
	return base64.StdEncoding.EncodeToString(pub), priv
}

// serveSigned, manifest ve .sig isteklerini yanitlar; baska yol icin false doner.
func serveSigned(w http.ResponseWriter, r *http.Request, manifest string, priv ed25519.PrivateKey) bool {
	switch r.URL.Path {
	case "/bin/manifest.json":
		io.WriteString(w, manifest)
		return true
	case "/bin/manifest.json.sig":
		io.WriteString(w, base64.StdEncoding.EncodeToString(ed25519.Sign(priv, []byte(manifest))))
		return true
	}
	return false
}

// runUpdate, verilen sunucuya karsi elle guncelleme calistirir ve indirilen
// ikili yolunu ("" = indirme yok) ile lastUpdateErr'i doner.
func runUpdate(t *testing.T, version string, keys []string, h func(w http.ResponseWriter, r *http.Request, dl *atomic.Value)) (string, error) {
	t.Helper()
	var dl atomic.Value
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { h(w, r, &dl) }))
	t.Cleanup(srv.Close)
	a := &Agent{Version: version, Insecure: true, updateKeys: keys,
		ServerAddr: strings.TrimPrefix(srv.URL, "http://"),
		Log:        slog.New(slog.NewTextHandler(io.Discard, nil))}
	a.forceUpdate.Store(true)
	a.maybeUpdate(context.Background(), "test")
	p, _ := dl.Load().(string)
	return p, a.lastUpdateErr
}

func manifestFor(version string) string {
	key := runtime.GOOS + "/" + runtime.GOARCH
	return `{"version":"` + version + `","files":{"` + key + `+cli":{"name":"cli","sha256":"x"}}}`
}

func signedHandler(manifest string, priv ed25519.PrivateKey) func(http.ResponseWriter, *http.Request, *atomic.Value) {
	return func(w http.ResponseWriter, r *http.Request, dl *atomic.Value) {
		if serveSigned(w, r, manifest, priv) {
			return
		}
		dl.Store(r.URL.Path)
		io.WriteString(w, "veri")
	}
}

func TestUpdateValidSignatureAccepted(t *testing.T) {
	pub, priv := newTestKey()
	p, err := runUpdate(t, "0.2.5", []string{pub}, signedHandler(manifestFor("0.2.6"), priv))
	if p != "/bin/cli" {
		t.Fatalf("dogru imzali manifest ile indirme yapilmadi (p=%q err=%v)", p, err)
	}
}

func TestUpdateRejectsBadSignature(t *testing.T) {
	pub, _ := newTestKey()
	_, otherPriv := newTestKey() // yanlis anahtarla imzali
	p, err := runUpdate(t, "0.2.5", []string{pub}, signedHandler(manifestFor("0.2.6"), otherPriv))
	if p != "" || err == nil {
		t.Fatalf("yanlis anahtar imzasi kabul edildi (p=%q err=%v)", p, err)
	}
}

func TestUpdateRejectsMissingSig(t *testing.T) {
	pub, _ := newTestKey()
	p, err := runUpdate(t, "0.2.5", []string{pub}, func(w http.ResponseWriter, r *http.Request, dl *atomic.Value) {
		if r.URL.Path == "/bin/manifest.json" {
			io.WriteString(w, manifestFor("0.2.6"))
			return
		}
		if r.URL.Path == "/bin/manifest.json.sig" {
			http.NotFound(w, r)
			return
		}
		dl.Store(r.URL.Path)
	})
	if p != "" || err == nil {
		t.Fatalf("imzasiz manifest kabul edildi (p=%q err=%v)", p, err)
	}
}

func TestUpdateRejectsTamperedManifest(t *testing.T) {
	pub, priv := newTestKey()
	p, err := runUpdate(t, "0.2.5", []string{pub}, func(w http.ResponseWriter, r *http.Request, dl *atomic.Value) {
		if r.URL.Path == "/bin/manifest.json" { // sunulan govde imzalanandan farkli
			io.WriteString(w, manifestFor("0.2.7"))
			return
		}
		signedHandler(manifestFor("0.2.6"), priv)(w, r, dl)
	})
	if p != "" || err == nil {
		t.Fatalf("degistirilmis manifest kabul edildi (p=%q err=%v)", p, err)
	}
}

func TestUpdateRejectsWhenNoKeysConfigured(t *testing.T) {
	_, priv := newTestKey()
	p, err := runUpdate(t, "0.2.5", []string{}, signedHandler(manifestFor("0.2.6"), priv))
	if p != "" || err == nil || !strings.Contains(err.Error(), "yapilandirilmamis") {
		t.Fatalf("anahtarsiz kabul/uyari hatali (p=%q err=%v)", p, err)
	}
}

func TestUpdateRejectsDowngradeAndSameVersion(t *testing.T) {
	pub, priv := newTestKey()
	for _, mv := range []string{"0.2.4", "0.2.5"} {
		p, _ := runUpdate(t, "0.2.5", []string{pub}, signedHandler(manifestFor(mv), priv))
		if p != "" {
			t.Fatalf("surum %s -> 0.2.5 uzerine indirme yapildi", mv)
		}
	}
}
