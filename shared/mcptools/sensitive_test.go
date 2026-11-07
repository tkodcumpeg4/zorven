package mcptools

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestSensitiveTarget(t *testing.T) {
	cases := []struct {
		target string
		want   bool
	}{
		// cekirdek kullanim: hassas DEGIL
		{"http://localhost:3000", false},
		{"localhost:3000", false},
		{"http://127.0.0.1:8080/api", false},
		{"https://localhost", false},
		{"http://[::1]:5173", false},
		{"0.0.0.0:8000", false},
		{"http://127.0.0.2:4000", false},
		{"http://example.com:8080", false},
		{"https://api.example.com", false},
		{"", false},
		// yonetim portlari
		{"localhost:22", true},
		{"tcp://127.0.0.1:22", true},
		{"[::1]:3389", true},
		{"localhost:5900", true},
		{"localhost:5902", true},
		{"localhost:5903", false},
		{"http://localhost:2375", true},
		{"localhost:2376", true},
		{"localhost:6443", true},
		{"localhost:10250", true},
		{"localhost:5432", true},
		{"localhost:3306", true},
		{"localhost:6379", true},
		{"localhost:27017", true},
		{"http://localhost:9200", true},
		{"example.com:22", true},
		{"http://user:pw@localhost:5432/db", true},
		// ozel / link-local / ic ad
		{"http://192.168.1.10:8080", true},
		{"10.0.0.5:80", true},
		{"http://172.16.0.1", true},
		{"172.31.255.1:80", true},
		{"172.32.0.1:80", false},
		{"http://169.254.169.254/latest/meta-data", true},
		{"[fe80::1]:80", true},
		{"http://[fd00::5]:80", true},
		{"[::ffff:10.0.0.1]:80", true},
		{"http://host.docker.internal:3000", true},
		{"http://db.internal:80", true},
		{"http://8.8.8.8:80", false},
	}
	for _, c := range cases {
		got, why := SensitiveTarget(c.target)
		if got != c.want {
			t.Errorf("SensitiveTarget(%q) = %v (%v), want %v", c.target, got, why, c.want)
		}
		if got && len(why) == 0 {
			t.Errorf("%q: neden bos", c.target)
		}
	}
	if _, why := SensitiveTarget("localhost:22"); len(why) == 0 || !strings.Contains(why[0], "SSH") {
		t.Errorf("neden SSH icermeli: %v", why)
	}
}

func TestSensitiveConfirmGate(t *testing.T) {
	fb := &fakeBackend{reply: func(r Request) Response { return jsonResp(200, `{"id":"t1"}`) }}
	cs := connect(t, NewServer(fb, Options{Scopes: []string{"tunnels:write"}}))

	// hassas + confirm yok -> hata, API cagrisi YOK
	_, isErr, txt := callTool(t, cs, "create_tunnel", map[string]any{"name": "ssh", "device_id": "c1", "target": "localhost:22"})
	if !isErr || !strings.Contains(txt, "confirm_required") || !strings.Contains(txt, "SSH") {
		t.Fatalf("create onaysiz: isErr=%v %q", isErr, txt)
	}
	_, isErr, txt = callTool(t, cs, "update_tunnel", map[string]any{"tunnel_id": "t1", "target": "http://192.168.0.2:80"})
	if !isErr || !strings.Contains(txt, "confirm_required") {
		t.Fatalf("update onaysiz: isErr=%v %q", isErr, txt)
	}
	if len(fb.reqs) != 0 {
		t.Fatalf("onaysiz cagri API'ye gitti: %v", fb.reqs)
	}

	// confirm=true -> cagri yapilir, confirm govdeye SIZMAZ
	if _, isErr, txt = callTool(t, cs, "create_tunnel", map[string]any{"name": "ssh", "device_id": "c1", "target": "localhost:22", "confirm": true}); isErr {
		t.Fatalf("create onayli: %q", txt)
	}
	if _, isErr, txt = callTool(t, cs, "update_tunnel", map[string]any{"tunnel_id": "t1", "target": "localhost:22", "confirm": true}); isErr {
		t.Fatalf("update onayli: %q", txt)
	}
	if len(fb.reqs) != 2 {
		t.Fatalf("2 istek beklenir: %d", len(fb.reqs))
	}
	for _, r := range fb.reqs {
		if _, ok := r.Body.(map[string]any)["confirm"]; ok {
			t.Errorf("confirm govdeye sizdi: %v", r.Body)
		}
	}

	// hassas olmayan (loopback + normal port) onaysiz gecer; yalniz enabled gecer
	fb.reqs = nil
	if _, isErr, txt = callTool(t, cs, "create_tunnel", map[string]any{"name": "web", "device_id": "c1", "target": "http://localhost:3000"}); isErr {
		t.Fatalf("normal create: %q", txt)
	}
	if _, isErr, txt = callTool(t, cs, "update_tunnel", map[string]any{"tunnel_id": "t1", "enabled": false}); isErr {
		t.Fatalf("enabled-only: %q", txt)
	}
	if len(fb.reqs) != 2 {
		t.Fatalf("2 istek beklenir: %d", len(fb.reqs))
	}
}

func TestHTTPBackendMarksMCPClient(t *testing.T) {
	var got string
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = r.Header.Get("X-Zorven-Client")
		_, _ = w.Write([]byte(`{}`))
	}))
	defer ts.Close()
	b := &HTTPBackend{BaseURL: ts.URL, Token: "zrv_api_x"}
	if _, err := b.Do(t.Context(), Request{Method: "GET", Path: "/api/v1/me"}); err != nil {
		t.Fatal(err)
	}
	if got != "mcp" {
		t.Fatalf("X-Zorven-Client = %q", got)
	}
}
