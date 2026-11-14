package main

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/tkodcumpeg4/zorven/server/mcpserver"
	"github.com/tkodcumpeg4/zorven/server/ratelimit"
)

const revokeTestToken = "zrv_api_" + "0123456789abcdef0123456789abcdef0123456789abcdef"

// Kanca, main.go'daki gibi isleyici SONRADAN baglanarak kurulur; iptal sonrasi MCP
// kimlik onbellegi hemen dusmeli (onbellekli kimlik 401'e donmeli).
func TestMCPIdentityInvalidatorWiring(t *testing.T) {
	var p atomic.Pointer[mcpserver.Handler]
	hook := mcpIdentityInvalidator(&p)
	hook() // isleyici henuz yok: no-op, panik yok

	var revoked atomic.Bool
	api := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if revoked.Load() || r.Header.Get("Authorization") != "Bearer "+revokeTestToken {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"authenticated":true,"tenant":{"id":"ten_1","slug":"a"},"scopes":["*"]}`))
	})
	h := mcpserver.New(mcpserver.Config{API: api, FailLimiter: ratelimit.New(1000, 1000), Version: "t"})
	p.Store(h)

	post := func() int {
		req := httptest.NewRequest(http.MethodPost, "/mcp", strings.NewReader(
			`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-03-26","capabilities":{},"clientInfo":{"name":"t","version":"1"}}}`))
		req.Header.Set("Authorization", "Bearer "+revokeTestToken)
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Accept", "application/json, text/event-stream")
		w := httptest.NewRecorder()
		h.ServeHTTP(w, req)
		return w.Code
	}
	if c := post(); c == http.StatusUnauthorized {
		t.Fatalf("gecerli token reddedildi: %d", c)
	}
	revoked.Store(true)
	if c := post(); c == http.StatusUnauthorized {
		t.Fatalf("onbellekli kimlik beklenirdi (kanca henuz cagrilmadi): %d", c)
	}
	hook()
	if c := post(); c != http.StatusUnauthorized {
		t.Fatalf("kanca sonrasi 401 beklenirdi: %d", c)
	}
}
