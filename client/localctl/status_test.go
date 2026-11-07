package localctl

import (
	"context"
	"encoding/json"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

type richFake struct {
	fake
	reloaded int
}

func (r *richFake) StatusInfo() StatusInfo {
	return StatusInfo{
		Connected: true, State: "connected", LastError: "x", Server: "s:443",
		StartedAt: "2026-10-28T00:00:00Z", Reconnects: 3,
		Disconnects: []DisconnectInfo{{At: "2026-10-28T01:00:00Z", Reason: "r"}},
		Tunnels:     []TunnelInfo{{ID: "t1", Name: "a.zorven.app", URL: "https://a.zorven.app", Target: "http://localhost:80", Protocol: "http", Requests: 5, BytesIn: 1, BytesOut: 2, Errors: 1, P50MS: 1.5, P95MS: 9}},
	}
}

func (r *richFake) Reload(ctx context.Context) (ReloadInfo, error) {
	r.reloaded++
	return ReloadInfo{Changed: []string{"terminal"}, Reconnecting: true}, nil
}

func serveCtl(t *testing.T, ctl Controller) Endpoint {
	t.Helper()
	dir, err := os.MkdirTemp("", "zv")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	ep := Endpoint{Path: filepath.Join(dir, "d", "ctl.sock")}
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	if err := Serve(ctx, ep, ctl, "t", nil); err != nil {
		t.Fatal(err)
	}
	return ep
}

func TestStatusAndReloadContract(t *testing.T) {
	f := &richFake{}
	ep := serveCtl(t, f)
	c, err := dial(ep, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	_ = json.NewEncoder(c).Encode(Request{Op: "status"})
	var raw map[string]any
	if err := json.NewDecoder(c).Decode(&raw); err != nil {
		t.Fatal(err)
	}
	for _, k := range []string{"connected", "state", "last_error", "server", "started_at", "reconnects", "disconnects", "tunnels"} {
		if _, ok := raw[k]; !ok {
			t.Errorf("status yanitinda %q yok", k)
		}
	}
	tun := raw["tunnels"].([]any)[0].(map[string]any)
	for _, k := range []string{"id", "name", "url", "target", "protocol", "requests", "bytes_in", "bytes_out", "errors", "p50_ms", "p95_ms"} {
		if _, ok := tun[k]; !ok {
			t.Errorf("tunnels[0].%s yok", k)
		}
	}
	d := raw["disconnects"].([]any)[0].(map[string]any)
	if d["at"] == nil || d["reason"] == nil {
		t.Errorf("disconnects alanlari: %v", d)
	}

	r, err := Call(ep, Request{Op: "status"})
	if err != nil || !r.Connected || r.Reconnects != 3 || r.Tunnels[0].P95MS != 9 {
		t.Fatalf("status: %v %+v", err, r)
	}
	r, err = Call(ep, Request{Op: "reload"})
	if err != nil || r.Reload == nil || !r.Reload.Reconnecting || r.Reload.Changed[0] != "terminal" || f.reloaded != 1 {
		t.Fatalf("reload: %v %+v", err, r)
	}
}

func TestReloadUnsupportedController(t *testing.T) {
	ep := serveCtl(t, &fake{})
	if _, err := Call(ep, Request{Op: "reload"}); err == nil {
		t.Fatal("reload desteklemeyen denetleyicide hata beklenir")
	}
	// status yine calisir (alanlar bos).
	if r, err := Call(ep, Request{Op: "status"}); err != nil || r.Connected {
		t.Fatalf("status: %v %+v", err, r)
	}
}

func TestRequestSizeLimit(t *testing.T) {
	ep := serveCtl(t, &fake{})
	c, err := dial(ep, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	_ = c.SetDeadline(time.Now().Add(3 * time.Second))
	big := `{"op":"status","target":"` + strings.Repeat("a", maxRequestBytes*2) + `"}` + "\n"
	go func() { _, _ = c.Write([]byte(big)) }()
	var resp Response
	_ = json.NewDecoder(c).Decode(&resp)
	if resp.OK {
		t.Fatal("asiri buyuk istek reddedilmeli")
	}
}

func TestConnectionLimit(t *testing.T) {
	ep := serveCtl(t, &fake{})
	var conns []net.Conn
	defer func() {
		for _, c := range conns {
			c.Close()
		}
	}()
	// Tavani dolduran bosta baglantilar (istek gondermeyen).
	for i := 0; i < maxConns; i++ {
		c, err := dial(ep, time.Second)
		if err != nil {
			t.Skipf("dial: %v", err)
		}
		conns = append(conns, c)
	}
	time.Sleep(100 * time.Millisecond)
	c, err := dial(ep, time.Second)
	if err != nil {
		return // reddedildi
	}
	defer c.Close()
	_ = c.SetDeadline(time.Now().Add(2 * time.Second))
	_ = json.NewEncoder(c).Encode(Request{Op: "status"})
	var resp Response
	if err := json.NewDecoder(c).Decode(&resp); err == nil && resp.OK {
		t.Fatal("tavan asilinca yeni baglanti islenmemeli")
	}
}
