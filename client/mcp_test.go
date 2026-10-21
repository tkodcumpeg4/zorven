package main

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/tkodcumpeg4/zorven/shared/mcptools"
)

func TestMCPBaseURL(t *testing.T) {
	cases := []struct {
		url, srv, env, cfg string
		insecure           bool
		want               string
	}{
		{"https://x.example/", "", "", "", false, "https://x.example"},
		{"", "", "", "", false, "https://zorven.app"},
		{"", "", "", "localhost:8443", false, "https://zorven.app"},
		{"", "my.host:8443", "", "zorven.app:443", false, "https://my.host:8443"},
		{"", "", "env.host:443", "zorven.app:443", false, "https://env.host"},
		{"", "127.0.0.1:8080", "", "", true, "http://127.0.0.1:8080"},
	}
	for _, c := range cases {
		got, err := mcpBaseURL(c.url, c.srv, c.env, c.cfg, c.insecure)
		if err != nil || got != c.want {
			t.Errorf("%+v -> %q %v, beklenen %q", c, got, err, c.want)
		}
	}
	if _, err := mcpBaseURL("zorven.app", "", "", "", false); err == nil {
		t.Error("semasiz --url reddedilmeli")
	}
}

// TestMCPStdioRoundTrip: zorven mcp'nin kullandigi HTTPBackend + mcptools sunucusu,
// stdio ile ayni JSON-RPC satir akisi (io.Pipe) uzerinden SDK istemcisiyle.
func TestMCPStdioRoundTrip(t *testing.T) {
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer zrv_api_t.s" {
			w.WriteHeader(401)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`[{"id":"tun_1","client_id":"c1"}]`))
	}))
	defer api.Close()

	srv := mcptools.NewServer(&mcptools.HTTPBackend{BaseURL: api.URL, Token: "zrv_api_t.s"},
		mcptools.Options{Scopes: []string{"tunnels:read"}})

	c2sR, c2sW := io.Pipe()
	s2cR, s2cW := io.Pipe()
	ctx := context.Background()
	ss, err := srv.Connect(ctx, &mcp.IOTransport{Reader: c2sR, Writer: s2cW}, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer ss.Close()
	cl := mcp.NewClient(&mcp.Implementation{Name: "t", Version: "1"}, nil)
	cs, err := cl.Connect(ctx, &mcp.IOTransport{Reader: s2cR, Writer: c2sW}, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer cs.Close()

	tools, err := cs.ListTools(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, tl := range tools.Tools {
		if tl.Name == "create_tunnel" {
			t.Error("kapsam disi arac listelendi")
		}
	}
	res, err := cs.CallTool(ctx, &mcp.CallToolParams{Name: "list_tunnels"})
	if err != nil || res.IsError {
		t.Fatalf("list_tunnels: %v %+v", err, res)
	}
}
