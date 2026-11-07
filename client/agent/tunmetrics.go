package agent

import (
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/tkodcumpeg4/zorven/client/localctl"
	"github.com/tkodcumpeg4/zorven/shared/protocol"
)

// Tunel basina sayaclar ve gecikme ornekleri (localctl "status" -> tunnels).
// Sayaclar ajan omrunce birikir (yeniden baglanma sifirlamaz).

const latencyReservoir = 512

type tunnelStat struct {
	requests int64
	bytesIn  int64 // sunucudan yerel servise (istek govdesi, WS/akis yukari)
	bytesOut int64 // yerel servisten sunucuya (yanit govdesi, WS/akis asagi)
	errors   int64
	lat      [latencyReservoir]float64 // ms, halka
	latN     int
	latPos   int
}

type tunnelMetrics struct {
	mu sync.Mutex
	m  map[string]*tunnelStat
}

func newTunnelMetrics() *tunnelMetrics {
	return &tunnelMetrics{m: make(map[string]*tunnelStat)}
}

func (t *tunnelMetrics) get(id string) *tunnelStat {
	s := t.m[id]
	if s == nil {
		s = &tunnelStat{}
		t.m[id] = s
	}
	return s
}

// request bir istek/akis acilisini sayar.
func (t *tunnelMetrics) request(id string) {
	if t == nil || id == "" {
		return
	}
	t.mu.Lock()
	t.get(id).requests++
	t.mu.Unlock()
}

func (t *tunnelMetrics) addIn(id string, n int) {
	if t == nil || id == "" || n <= 0 {
		return
	}
	t.mu.Lock()
	t.get(id).bytesIn += int64(n)
	t.mu.Unlock()
}

func (t *tunnelMetrics) addOut(id string, n int) {
	if t == nil || id == "" || n <= 0 {
		return
	}
	t.mu.Lock()
	t.get(id).bytesOut += int64(n)
	t.mu.Unlock()
}

func (t *tunnelMetrics) addError(id string) {
	if t == nil || id == "" {
		return
	}
	t.mu.Lock()
	t.get(id).errors++
	t.mu.Unlock()
}

// observe, bir istegin govde dahil toplam suresini kaydeder.
func (t *tunnelMetrics) observe(id string, d time.Duration) {
	if t == nil || id == "" {
		return
	}
	t.mu.Lock()
	s := t.get(id)
	s.lat[s.latPos] = float64(d) / float64(time.Millisecond)
	s.latPos = (s.latPos + 1) % latencyReservoir
	if s.latN < latencyReservoir {
		s.latN++
	}
	t.mu.Unlock()
}

type tunnelSnap struct {
	requests, bytesIn, bytesOut, errors int64
	p50, p95                            float64
}

func (t *tunnelMetrics) snapshot(id string) tunnelSnap {
	if t == nil {
		return tunnelSnap{}
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	s := t.m[id]
	if s == nil {
		return tunnelSnap{}
	}
	out := tunnelSnap{requests: s.requests, bytesIn: s.bytesIn, bytesOut: s.bytesOut, errors: s.errors}
	if s.latN > 0 {
		v := make([]float64, s.latN)
		copy(v, s.lat[:s.latN])
		sort.Float64s(v)
		out.p50, out.p95 = percentile(v, 0.50), percentile(v, 0.95)
	}
	return out
}

// percentile, sirali v'den en-yakin-sira yontemiyle p. yuzdelik (0..1).
func percentile(v []float64, p float64) float64 {
	if len(v) == 0 {
		return 0
	}
	idx := int(p*float64(len(v))+0.5) - 1
	if idx < 0 {
		idx = 0
	}
	if idx >= len(v) {
		idx = len(v) - 1
	}
	return v[idx]
}

// tunnelProtocol, hedef bicimine gore protokol etiketi.
func tunnelProtocol(target string) string {
	t := strings.ToLower(strings.TrimSpace(target))
	switch {
	case strings.HasPrefix(t, "https://"):
		return "https"
	case strings.HasPrefix(t, "http://"):
		return "http"
	case strings.HasPrefix(t, "tcp://"):
		return "tcp"
	case strings.HasPrefix(t, "udp://"):
		return "udp"
	case strings.HasPrefix(t, subnetPrefix):
		return "subnet"
	case strings.HasPrefix(t, zappScheme):
		return "zapp"
	}
	return "tcp"
}

// tunnelInfos, tunel listesini sayaclarla birlestirir.
func tunnelInfos(specs []protocol.TunnelSpec, tm *tunnelMetrics) []localctl.TunnelInfo {
	out := make([]localctl.TunnelInfo, 0, len(specs))
	for _, sp := range specs {
		name, url := "", ""
		if len(sp.Hostnames) > 0 {
			name = sp.Hostnames[0]
			url = "https://" + sp.Hostnames[0]
		}
		sn := tm.snapshot(sp.ID)
		out = append(out, localctl.TunnelInfo{
			ID: sp.ID, Name: name, URL: url, Target: sp.Target, Protocol: tunnelProtocol(sp.Target),
			Requests: sn.requests, BytesIn: sn.bytesIn, BytesOut: sn.bytesOut, Errors: sn.errors,
			P50MS: sn.p50, P95MS: sn.p95,
		})
	}
	return out
}
