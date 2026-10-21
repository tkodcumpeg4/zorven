package rawproxy

import (
	"context"
	"sync/atomic"
	"time"
)

// Ham TCP istatistikleri. Hem port modu (acceptLoop) hem SNI modu (HandleSNIConn)
// bridgeTCP'den gectigi icin sayaclar orada tutulur. Sayaclar tunel basina tek
// bir yapida atomiklerle tutulur: kopyalama dongusunde kilit yoktur, okuma basina
// yalnizca bir atomik toplama yapilir.

// Red nedenleri (API/panel anahtarlari).
const (
	TCPRejectIP      = "ip_denied"
	TCPRejectDoor    = "door_denied"
	TCPRejectOffline = "offline"
	TCPRejectDial    = "dial_failed"
)

type tcpStat struct {
	tunnelID, tenantID string
	started            time.Time

	connsNew atomic.Int64 // kabul edilip ajan akisi kurulan baglantilar
	active   atomic.Int64
	peak     atomic.Int64 // son toplamadan beri tepe eszamanli baglanti

	rejIP, rejDoor, rejOffline, rejDial atomic.Int64

	bytesIn  atomic.Int64 // ziyaretci -> ajan
	bytesOut atomic.Int64 // ajan -> ziyaretci

	durSumMs atomic.Int64 // biten baglantilarin toplam suresi
	durCount atomic.Int64
	durMax   atomic.Int64 // son toplamadan beri en uzun

	durMaxAll atomic.Int64 // ilk baglantidan beri en uzun

	flushed tcpSnapshot // yalnizca toplayici goroutine dokunur
}

type tcpSnapshot struct {
	connsNew                            int64
	rejIP, rejDoor, rejOffline, rejDial int64
	bytesIn, bytesOut                   int64
	durSumMs, durCount                  int64
}

func (t *tcpStat) snapshot() tcpSnapshot {
	return tcpSnapshot{
		connsNew: t.connsNew.Load(),
		rejIP:    t.rejIP.Load(), rejDoor: t.rejDoor.Load(), rejOffline: t.rejOffline.Load(), rejDial: t.rejDial.Load(),
		bytesIn: t.bytesIn.Load(), bytesOut: t.bytesOut.Load(),
		durSumMs: t.durSumMs.Load(), durCount: t.durCount.Load(),
	}
}

func (t *tcpStat) reject(reason string) {
	switch reason {
	case TCPRejectIP:
		t.rejIP.Add(1)
	case TCPRejectDoor:
		t.rejDoor.Add(1)
	case TCPRejectOffline:
		t.rejOffline.Add(1)
	default:
		t.rejDial.Add(1)
	}
}

// open, kurulan bir baglantiyi sayar ve tepe degerini gunceller.
func (t *tcpStat) open() {
	t.connsNew.Add(1)
	storeMax(&t.peak, t.active.Add(1))
}

func storeMax(v *atomic.Int64, n int64) {
	for {
		c := v.Load()
		if n <= c || v.CompareAndSwap(c, n) {
			return
		}
	}
}

// closeConn, biten baglantinin suresini kaydeder.
func (t *tcpStat) closeConn(d time.Duration) {
	t.active.Add(-1)
	ms := d.Milliseconds()
	t.durSumMs.Add(ms)
	t.durCount.Add(1)
	storeMax(&t.durMax, ms)
	storeMax(&t.durMaxAll, ms)
}

// tcpStatFor, tunelin sayac yapisini (yoksa olusturarak) doner.
func (m *Manager) tcpStatFor(ti TunnelInfo) *tcpStat {
	if v, ok := m.tcpStats.Load(ti.TunnelID); ok {
		return v.(*tcpStat)
	}
	v, _ := m.tcpStats.LoadOrStore(ti.TunnelID, &tcpStat{tunnelID: ti.TunnelID, tenantID: ti.TenantID, started: time.Now()})
	return v.(*tcpStat)
}

// TCPLiveStats, bir TCP tunelinin canli durumu (ilk baglantidan beri).
type TCPLiveStats struct {
	TunnelID   string           `json:"tunnel_id"`
	Port       int              `json:"port"`
	Since      time.Time        `json:"since"`
	Active     int64            `json:"active_conns"`
	ConnsTotal int64            `json:"conns_total"`
	Rejected   int64            `json:"conns_rejected"`
	RejectedBy map[string]int64 `json:"rejected_reasons"`
	BytesIn    int64            `json:"bytes_in"`
	BytesOut   int64            `json:"bytes_out"`
	DurAvgMs   float64          `json:"dur_avg_ms"`
	DurMaxMs   int64            `json:"dur_max_ms"`
}

// TCPMinute, bir tunelin son toplamadan bu yana biriken farki.
type TCPMinute struct {
	TunnelID, TenantID string
	Minute             time.Time
	ConnsNew           int64
	ConnsPeak          int64
	Rejected           int64
	RejectedIP         int64
	RejectedDoor       int64
	RejectedOffline    int64
	RejectedDial       int64
	BytesIn, BytesOut  int64
	DurSumMs           int64
	DurCount           int64
	DurMaxMs           int64
}

// TCPStats, tunelin canli TCP istatistikleri. Tunelin ne acik TCP dinleyicisi ne
// de sayaci varsa ok=false.
func (m *Manager) TCPStats(tunnelID string) (TCPLiveStats, bool) {
	port := 0
	m.mu.Lock()
	for k, ti := range m.byPort {
		if ti.TunnelID == tunnelID && ti.Proto == "tcp" {
			port = ti.Port
			if port == 0 {
				port = k.port
			}
			break
		}
	}
	m.mu.Unlock()
	v, has := m.tcpStats.Load(tunnelID)
	if !has {
		if port == 0 {
			return TCPLiveStats{}, false
		}
		return TCPLiveStats{TunnelID: tunnelID, Port: port, Since: time.Now(), RejectedBy: map[string]int64{}}, true
	}
	t := v.(*tcpStat)
	s := t.snapshot()
	ls := TCPLiveStats{
		TunnelID: tunnelID, Port: port, Since: t.started, Active: t.active.Load(),
		ConnsTotal: s.connsNew,
		Rejected:   s.rejIP + s.rejDoor + s.rejOffline + s.rejDial,
		RejectedBy: map[string]int64{
			TCPRejectIP: s.rejIP, TCPRejectDoor: s.rejDoor, TCPRejectOffline: s.rejOffline, TCPRejectDial: s.rejDial,
		},
		BytesIn: s.bytesIn, BytesOut: s.bytesOut, DurMaxMs: t.durMaxAll.Load(),
	}
	if s.durCount > 0 {
		ls.DurAvgMs = float64(s.durSumMs) / float64(s.durCount)
	}
	return ls, true
}

// collectTCPMinutes, her tunelin son toplamadan bu yana farkini doner. Yalnizca
// RunTCPStats goroutine'inden cagrilir (flushed alani kilitsizdir).
func (m *Manager) collectTCPMinutes(minute time.Time) []TCPMinute {
	var out []TCPMinute
	m.tcpStats.Range(func(_, v any) bool {
		t := v.(*tcpStat)
		cur := t.snapshot()
		prev := t.flushed
		t.flushed = cur
		peak := t.peak.Swap(t.active.Load())
		dmax := t.durMax.Swap(0)
		d := TCPMinute{
			TunnelID: t.tunnelID, TenantID: t.tenantID, Minute: minute,
			ConnsNew: cur.connsNew - prev.connsNew, ConnsPeak: peak,
			RejectedIP: cur.rejIP - prev.rejIP, RejectedDoor: cur.rejDoor - prev.rejDoor,
			RejectedOffline: cur.rejOffline - prev.rejOffline, RejectedDial: cur.rejDial - prev.rejDial,
			BytesIn: cur.bytesIn - prev.bytesIn, BytesOut: cur.bytesOut - prev.bytesOut,
			DurSumMs: cur.durSumMs - prev.durSumMs, DurCount: cur.durCount - prev.durCount, DurMaxMs: dmax,
		}
		d.Rejected = d.RejectedIP + d.RejectedDoor + d.RejectedOffline + d.RejectedDial
		// Tamamen sessiz dakikalar yazilmaz.
		if d.ConnsNew == 0 && d.ConnsPeak == 0 && d.Rejected == 0 && d.BytesIn == 0 && d.BytesOut == 0 && d.DurCount == 0 {
			return true
		}
		out = append(out, d)
		return true
	})
	return out
}

// RunTCPStats, her dakika tunel farklarini sink'e verir. Sink hatasi o dakikanin
// verisini kaybettirir; TCP trafigi etkilenmez.
func (m *Manager) RunTCPStats(ctx context.Context, sink func(context.Context, []TCPMinute)) {
	tk := time.NewTicker(time.Minute)
	defer tk.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case now := <-tk.C:
			if rows := m.collectTCPMinutes(now.Add(-time.Second).UTC().Truncate(time.Minute)); len(rows) > 0 {
				sink(ctx, rows)
			}
		}
	}
}
