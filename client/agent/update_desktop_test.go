package agent

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"
)

// Masaustu gomulu ajani manifest'e hic bakmamali (CLI'yi masaustu exe'sinin
// uzerine yazma olayi, 2026-10-09).
func TestDesktopAgentNeverSelfUpdates(t *testing.T) {
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		io.WriteString(w, `{"version":"9.9.9","files":{}}`)
	}))
	defer srv.Close()

	a := &Agent{Version: "0.2.4", AppKind: "desktop", Insecure: true,
		ServerAddr: strings.TrimPrefix(srv.URL, "http://"),
		Log:        slog.New(slog.NewTextHandler(io.Discard, nil))}
	a.forceUpdate.Store(true) // elle bile olsa
	a.maybeUpdate(context.Background(), "test")
	if hits.Load() != 0 {
		t.Fatalf("masaustu ajani manifest istedi (%d)", hits.Load())
	}
}

// CLI, "+cli" anahtarini duz anahtara tercih eder; sha uyusmazliginda uygulamaz.
func TestCLIUpdatePrefersCLIKey(t *testing.T) {
	key := runtime.GOOS + "/" + runtime.GOARCH
	manifest := `{"version":"9.9.9","files":{"` + key + `":{"name":"duz","sha256":"x"},"` + key + `+cli":{"name":"cli","sha256":"x"}}}`
	pub, priv := newTestKey()
	var got atomic.Value
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if serveSigned(w, r, manifest, priv) {
			return
		}
		got.Store(r.URL.Path)
		io.WriteString(w, "veri")
	}))
	defer srv.Close()

	a := &Agent{Version: "0.2.5", Insecure: true, updateKeys: []string{pub},
		ServerAddr: strings.TrimPrefix(srv.URL, "http://"),
		Log:        slog.New(slog.NewTextHandler(io.Discard, nil))}
	a.forceUpdate.Store(true)
	a.maybeUpdate(context.Background(), "test")
	if p, _ := got.Load().(string); p != "/bin/cli" {
		t.Fatalf("indirilen = %q, /bin/cli bekleniyordu", p)
	}
	if a.lastUpdateErr == nil {
		t.Fatal("sha uyusmazligi hata olarak donmeliydi")
	}
}
