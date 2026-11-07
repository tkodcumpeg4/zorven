package main

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/tkodcumpeg4/zorven/client/localctl"
)

func isolateAccessEnv(t *testing.T) {
	t.Helper()
	d := t.TempDir()
	t.Setenv("HOME", d)
	t.Setenv("XDG_CONFIG_HOME", d)
	t.Setenv("APPDATA", d)
	t.Setenv("ZORVEN_TOKEN", "")
	t.Setenv("ZORVEN_API_TOKEN", "")
	t.Setenv("ZORVEN_SERVER", "")
}

func runAccess(args ...string) error {
	cmd := newAccessCmd()
	cmd.SilenceUsage, cmd.SilenceErrors = true, true
	cmd.SetArgs(args)
	return cmd.Execute()
}

// Calisan ajan varsa erisim ona actirilir; token gerekmez.
func TestAccessViaRunningAgent(t *testing.T) {
	isolateAccessEnv(t)
	var got localctl.Request
	out := stub(t, &localctl.Endpoint{}, nil, func(r localctl.Request) (localctl.Response, error) {
		got = r
		return localctl.Response{OK: true, Local: "127.0.0.1:15432"}, nil
	})
	if err := runAccess("db", "--local", "15432"); err != nil {
		t.Fatal(err)
	}
	if got.Op != "access_open" || got.Target != "db" || got.Local != "127.0.0.1:15432" {
		t.Fatalf("istek: %+v", got)
	}
	if !strings.Contains(out.String(), "127.0.0.1:15432") {
		t.Fatalf("cikti: %s", out.String())
	}
	// --local yoksa port bos gider (ajan paylasimin portunu kullanir); ad:port verilmisse o port.
	if err := runAccess("db"); err != nil || got.Local != "127.0.0.1:" {
		t.Fatalf("varsayilan port bos olmali: %v %q", err, got.Local)
	}
	if err := runAccess("db:5432"); err != nil || got.Local != "127.0.0.1:5432" {
		t.Fatalf("ad:port: %v %q", err, got.Local)
	}
}

// Ajan yok, token da yok: kimlik dogrulama yolu anlatan hata.
func TestAccessNoAgentNoToken(t *testing.T) {
	isolateAccessEnv(t)
	stub(t, nil, nil, nil)
	err := runAccess("db")
	if err == nil || !strings.Contains(err.Error(), "zorven authtoken") {
		t.Fatalf("authtoken ipucu beklenir: %v", err)
	}
}

// Ajan yokken kayitli/verilen istemci token'i oturum ucuna Bearer olarak gider.
func TestAccessWithClientToken(t *testing.T) {
	isolateAccessEnv(t)
	stub(t, nil, nil, nil)
	var auth, path string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		auth, path = r.Header.Get("Authorization"), r.URL.Path
		http.Error(w, "no", http.StatusUnauthorized)
	}))
	defer srv.Close()
	host := strings.TrimPrefix(srv.URL, "http://")
	err := runAccess("db", "--token", "zrv_live_abc_def", "--server", host, "--insecure")
	if err == nil {
		t.Fatal("401 hata vermeli")
	}
	if auth != "Bearer zrv_live_abc_def" || path != "/api/v1/net/session" {
		t.Fatalf("auth=%q path=%q", auth, path)
	}
	// ZORVEN_TOKEN ortam degiskeni de ayni yolu kullanir.
	auth = ""
	t.Setenv("ZORVEN_TOKEN", "zrv_live_env_tok")
	_ = runAccess("db", "--server", host, "--insecure")
	if auth != "Bearer zrv_live_env_tok" {
		t.Fatalf("env token: %q", auth)
	}
}

// Acik API token'i ajandan ONCE gelir (mevcut davranis korunur).
func TestAccessExplicitAPITokenWins(t *testing.T) {
	isolateAccessEnv(t)
	called := false
	stub(t, &localctl.Endpoint{}, nil, func(localctl.Request) (localctl.Response, error) { called = true; return localctl.Response{}, nil })
	var auth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		auth = r.Header.Get("Authorization")
		http.Error(w, "no", http.StatusUnauthorized)
	}))
	defer srv.Close()
	t.Setenv("ZORVEN_API_TOKEN", "zrv_api_x_y")
	_ = runAccess("db", "--server", strings.TrimPrefix(srv.URL, "http://"), "--insecure")
	if called || auth != "Bearer zrv_api_x_y" {
		t.Fatalf("called=%v auth=%q", called, auth)
	}
}
