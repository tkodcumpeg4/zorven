package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/tkodcumpeg4/zorven/server/rawproxy"
	"github.com/tkodcumpeg4/zorven/server/store"
)

// tcpFakeStore, yalnizca TCP/UDP istatistik uclarinin kullandigi yuzeyi doldurur.
type tcpFakeStore struct {
	store.Store
	tunnels map[string]store.Tunnel // id -> tunel (TenantID kiraci)
	tcp     []store.TCPStatMinute
	udp     []store.UDPStatMinute
}

func (f *tcpFakeStore) GetTunnel(_ context.Context, tenantID, id string) (store.Tunnel, error) {
	t, ok := f.tunnels[id]
	if !ok || t.TenantID != tenantID {
		return store.Tunnel{}, store.ErrNotFound
	}
	return t, nil
}

func (f *tcpFakeStore) ListTCPStats(_ context.Context, tenantID, id string, since time.Time) ([]store.TCPStatMinute, error) {
	out := []store.TCPStatMinute{}
	for _, m := range f.tcp {
		if m.TenantID == tenantID && m.TunnelID == id && !m.Minute.Before(since) {
			out = append(out, m)
		}
	}
	return out, nil
}

func (f *tcpFakeStore) ListUDPStats(_ context.Context, tenantID, id string, since time.Time) ([]store.UDPStatMinute, error) {
	out := []store.UDPStatMinute{}
	for _, m := range f.udp {
		if m.TenantID == tenantID && m.TunnelID == id && !m.Minute.Before(since) {
			out = append(out, m)
		}
	}
	return out, nil
}

func tcpTestServer() http.Handler {
	now := time.Now().UTC().Truncate(time.Minute)
	fs := &tcpFakeStore{
		tunnels: map[string]store.Tunnel{
			"tcp1":  {ID: "tcp1", TenantID: "A", Proto: store.ProtoTCP},
			"udp1":  {ID: "udp1", TenantID: "A", Proto: store.ProtoUDP},
			"http1": {ID: "http1", TenantID: "A", Proto: store.ProtoHTTP},
		},
		tcp: []store.TCPStatMinute{
			{TunnelID: "tcp1", TenantID: "A", Minute: now.Add(-2 * time.Minute), ConnsNew: 4, ConnsPeak: 2, ConnsRejected: 1, RejectedIP: 1,
				BytesIn: 100, BytesOut: 300, DurSumMs: 400, DurCount: 4, DurMaxMs: 250, DurAvgMs: 100},
			{TunnelID: "tcp1", TenantID: "A", Minute: now.Add(-1 * time.Minute), ConnsNew: 1, ConnsPeak: 5,
				BytesIn: 10, BytesOut: 20, DurSumMs: 600, DurCount: 1, DurMaxMs: 600, DurAvgMs: 600},
		},
		udp: []store.UDPStatMinute{
			{TunnelID: "udp1", TenantID: "A", Minute: now.Add(-1 * time.Minute), PacketsIn: 7, PacketsOut: 8,
				BytesIn: 70, BytesOut: 80, FlowsNew: 2, FlowsPeak: 3, DroppedRate: 1},
		},
	}
	srv := &Server{Store: fs,
		TCPStats: func(id string) (rawproxy.TCPLiveStats, bool) {
			if id == "tcp1" {
				return rawproxy.TCPLiveStats{TunnelID: id, Port: 10000, Active: 2, ConnsTotal: 5}, true
			}
			return rawproxy.TCPLiveStats{}, false
		}}
	return srv.Routes()
}

func tcpReq(h http.Handler, tenant, path string) *httptest.ResponseRecorder {
	req := httptest.NewRequest("GET", path, nil)
	req = req.WithContext(withTenant(req.Context(), tenant))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func TestTunnelTCPEndpointShape(t *testing.T) {
	h := tcpTestServer()
	rec := tcpReq(h, "A", "/api/v1/tunnels/tcp1/tcp")
	if rec.Code != 200 {
		t.Fatalf("durum %d: %s", rec.Code, rec.Body)
	}
	var out struct {
		Live   map[string]any   `json:"live"`
		Series []map[string]any `json:"series"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	if out.Live["active_conns"] != float64(2) || out.Live["port"] != float64(10000) {
		t.Fatalf("live: %v", out.Live)
	}
	if len(out.Series) != 2 || out.Series[0]["conns_new"] != float64(4) || out.Series[0]["dur_avg_ms"] != float64(100) {
		t.Fatalf("series: %v", out.Series)
	}
	// Proto uyusmazligi.
	if rec := tcpReq(h, "A", "/api/v1/tunnels/udp1/tcp"); rec.Code != 409 {
		t.Fatalf("udp tunel 409 olmali: %d", rec.Code)
	}
}

func TestTunnelTCPTenantIsolation(t *testing.T) {
	h := tcpTestServer()
	for _, p := range []string{"/api/v1/tunnels/tcp1/tcp", "/api/v1/tunnels/tcp1/metrics?window=1h"} {
		if rec := tcpReq(h, "B", p); rec.Code != 404 {
			t.Fatalf("%s baska kiraciya 404 olmali: %d %s", p, rec.Code, rec.Body)
		}
	}
}

func TestMetricsRawSection(t *testing.T) {
	h := tcpTestServer()
	rec := tcpReq(h, "A", "/api/v1/tunnels/tcp1/metrics?window=1h")
	if rec.Code != 200 {
		t.Fatalf("durum %d: %s", rec.Code, rec.Body)
	}
	var out struct {
		Buckets []any `json:"buckets"`
		Raw     struct {
			Proto   string           `json:"proto"`
			Buckets []map[string]any `json:"buckets"`
			Summary map[string]any   `json:"summary"`
		} `json:"raw"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	if out.Raw.Proto != "tcp" || len(out.Buckets) != 0 || len(out.Raw.Buckets) == 0 {
		t.Fatalf("raw: %+v", out)
	}
	s := out.Raw.Summary
	if s["conns_new"] != float64(5) || s["conns_peak"] != float64(5) || s["conns_rejected"] != float64(1) ||
		s["bytes_in"] != float64(110) || s["bytes_out"] != float64(320) || s["dur_max_ms"] != float64(600) || s["dur_avg_ms"] != float64(200) {
		t.Fatalf("summary: %v", s)
	}

	rec = tcpReq(h, "A", "/api/v1/tunnels/udp1/metrics?window=1h")
	if rec.Code != 200 {
		t.Fatalf("udp durum %d: %s", rec.Code, rec.Body)
	}
	var u struct {
		Raw struct {
			Proto   string         `json:"proto"`
			Summary map[string]any `json:"summary"`
		} `json:"raw"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &u); err != nil {
		t.Fatal(err)
	}
	if u.Raw.Proto != "udp" || u.Raw.Summary["packets_in"] != float64(7) || u.Raw.Summary["flows_peak"] != float64(3) || u.Raw.Summary["dropped"] != float64(1) {
		t.Fatalf("udp raw: %+v", u.Raw)
	}
}

func TestTCPRouteOnTokenSurface(t *testing.T) {
	if !tokenMayAccess("GET", "/api/v1/tunnels/t1/tcp") {
		t.Fatal("GET .../tcp token yuzeyinde olmali")
	}
}
