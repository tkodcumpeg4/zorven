package rawproxy

import (
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/tkodcumpeg4/zorven/server/tunnel"
)

func probeManager(t *testing.T, supported bool) (*Manager, *udpListener, *tunnel.Session) {
	t.Helper()
	hub := tunnel.NewHub()
	sess := tunnel.NewSession("s1", "c1", "c", "127.0.0.1", nil, slog.New(slog.NewTextHandler(io.Discard, nil)))
	sess.SetUDPProbe(supported)
	if err := hub.Register(sess); err != nil {
		t.Fatal(err)
	}
	m := &Manager{Hub: hub, udpLn: map[listenKey]*udpListener{}}
	ul := &udpListener{m: m, flows: map[string]*udpFlow{}}
	ul.setInfo(TunnelInfo{TunnelID: "t1", TenantID: "ten", ClientID: "c1"})
	m.udpLn[listenKey{port: 5000}] = ul
	return m, ul, sess
}

func TestCollectUDPMinutesIncludesProbe(t *testing.T) {
	m, ul, sess := probeManager(t, true)
	tr := sess.UDPProbes()
	now := time.Now()
	for _, rtt := range []time.Duration{10, 30, 50} {
		seq := tr.Sent("t1", now)
		tr.Echo("t1", seq, now.Add(rtt*time.Millisecond))
	}
	tr.AddDropped("t1", 4)
	rows := m.collectUDPMinutes(time.Unix(60, 0).UTC())
	if len(rows) != 1 {
		t.Fatalf("satir bekleniyordu: %+v", rows)
	}
	r := rows[0]
	if !r.ProbeSupported || r.RTTP50Ms == nil || *r.RTTP50Ms != 30 || r.RTTP95Ms == nil || *r.RTTP95Ms != 50 {
		t.Fatalf("rtt yanlis: %+v", r)
	}
	if r.ProbeLossPct == nil || *r.ProbeLossPct != 0 || r.DroppedDatagrams != 4 {
		t.Fatalf("kayip/dusen yanlis: %+v", r)
	}
	// Ikinci dakika sessiz: satir yok.
	if rows := m.collectUDPMinutes(time.Unix(120, 0).UTC()); len(rows) != 0 {
		t.Fatalf("sessiz dakika yazilmamali: %+v", rows)
	}
	_ = ul
}

func TestCollectUDPMinutesLossOnly(t *testing.T) {
	m, _, sess := probeManager(t, true)
	tr := sess.UDPProbes()
	tr.Sent("t1", time.Now().Add(-time.Minute)) // yaniti hic gelmedi
	rows := m.collectUDPMinutes(time.Unix(60, 0).UTC())
	if len(rows) != 1 || rows[0].ProbeLossPct == nil || *rows[0].ProbeLossPct != 100 || rows[0].RTTP50Ms != nil {
		t.Fatalf("tam kayip bekleniyordu: %+v", rows)
	}
}

func TestCollectUDPMinutesUnsupportedClient(t *testing.T) {
	m, ul, _ := probeManager(t, false)
	ul.stats.packetsIn.Add(1)
	rows := m.collectUDPMinutes(time.Unix(60, 0).UTC())
	if len(rows) != 1 || rows[0].ProbeSupported || rows[0].RTTP50Ms != nil || rows[0].ProbeLossPct != nil {
		t.Fatalf("destek yoksa olcum nil olmali: %+v", rows)
	}
	live, ok := m.UDPStats("t1")
	if !ok || live.ProbeSupported || live.ProbeRTTMs != nil {
		t.Fatalf("canli: %+v", live)
	}
}

func TestLiveStatsReportsEWMA(t *testing.T) {
	m, _, sess := probeManager(t, true)
	now := time.Now()
	seq := sess.UDPProbes().Sent("t1", now)
	sess.UDPProbes().Echo("t1", seq, now.Add(25*time.Millisecond))
	live, ok := m.UDPStats("t1")
	if !ok || !live.ProbeSupported || live.ProbeRTTMs == nil || *live.ProbeRTTMs != 25 {
		t.Fatalf("canli: %+v", live)
	}
}

func TestProbeOnceSkipsUnsupportedAndOffline(t *testing.T) {
	m, _, sess := probeManager(t, false)
	m.probeOnce(t.Context()) // destek yok: conn nil'e dokunmadan donmeli
	if _, ok := sess.UDPProbes().Drain("t1", time.Now()); ok {
		t.Fatal("desteksiz istemciye yoklama kaydi olusmamali")
	}
	m2 := &Manager{udpLn: map[listenKey]*udpListener{}}
	m2.probeOnce(t.Context()) // Hub nil: panik yok
}
