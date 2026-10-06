package rawproxy

import (
	"testing"
	"time"
)

func TestTokenBucket(t *testing.T) {
	var b tokenBucket
	now := time.Unix(1000, 0)
	// Sinirsiz.
	for i := 0; i < 1000; i++ {
		if !b.allow(0, now) {
			t.Fatal("rate=0 sinirsiz olmali")
		}
	}
	// 10/sn: ilk saniyede 10 izin, 11. red.
	b = tokenBucket{}
	ok := 0
	for i := 0; i < 11; i++ {
		if b.allow(10, now) {
			ok++
		}
	}
	if ok != 10 {
		t.Fatalf("ilk patlamada 10 izin bekleniyordu, %d", ok)
	}
	// 0.5 sn sonra 5 jeton dolar.
	now = now.Add(500 * time.Millisecond)
	ok = 0
	for i := 0; i < 10; i++ {
		if b.allow(10, now) {
			ok++
		}
	}
	if ok != 5 {
		t.Fatalf("yarim saniyede 5 izin bekleniyordu, %d", ok)
	}
	// Uzun bekleme patlamayi 1 saniyelik payla sinirlar.
	now = now.Add(time.Hour)
	ok = 0
	for i := 0; i < 50; i++ {
		if b.allow(10, now) {
			ok++
		}
	}
	if ok != 10 {
		t.Fatalf("patlama 10 ile sinirli olmali, %d", ok)
	}
}

func TestUDPLimitsNormalized(t *testing.T) {
	l := UDPLimits{MaxPacket: 999999, MaxPPS: -5}.normalized()
	if l.IdleTimeout != defaultUDPIdle || l.MaxPacket != defaultUDPMaxPacket || l.MaxFlows != defaultUDPMaxFlows || l.MaxPPS != 0 {
		t.Fatalf("varsayilanlar uygulanmadi: %+v", l)
	}
}

func newTestListener() *udpListener {
	ul := &udpListener{flows: map[string]*udpFlow{}}
	ul.setInfo(TunnelInfo{TunnelID: "t1", TenantID: "ten"})
	return ul
}

func TestAdmitPacketSize(t *testing.T) {
	ul := newTestListener()
	lim := UDPLimits{MaxPacket: 100}.normalized()
	now := time.Unix(1, 0)
	if ul.admit(lim, 101, &udpFlow{}, 1, now) {
		t.Fatal("buyuk paket reddedilmeli")
	}
	if !ul.admit(lim, 100, &udpFlow{}, 1, now) {
		t.Fatal("sinirdaki paket kabul edilmeli")
	}
	if got := ul.stats.droppedSize.Load(); got != 1 {
		t.Fatalf("droppedSize=1 bekleniyordu, %d", got)
	}
}

func TestAdmitFlowCap(t *testing.T) {
	ul := newTestListener()
	lim := UDPLimits{MaxFlows: 2}.normalized()
	now := time.Unix(1, 0)
	if !ul.admit(lim, 10, nil, 1, now) {
		t.Fatal("sinirin altinda yeni flow kabul edilmeli")
	}
	if ul.admit(lim, 10, nil, 2, now) {
		t.Fatal("flow ust sinirinda yeni flow reddedilmeli")
	}
	// Mevcut flow'un paketleri flow sinirindan etkilenmez.
	if !ul.admit(lim, 10, &udpFlow{}, 2, now) {
		t.Fatal("mevcut flow'un paketi gecmeli")
	}
	if got := ul.stats.droppedFlows.Load(); got != 1 {
		t.Fatalf("droppedFlows=1 bekleniyordu, %d", got)
	}
}

func TestAdmitNewFlowRate(t *testing.T) {
	ul := newTestListener()
	lim := UDPLimits{MaxFlows: 100000}.normalized()
	now := time.Unix(1, 0)
	ok := 0
	for i := 0; i < udpNewFlowsPerSec+50; i++ {
		if ul.admit(lim, 10, nil, 0, now) {
			ok++
		}
	}
	if ok != udpNewFlowsPerSec {
		t.Fatalf("saniyede %d yeni flow bekleniyordu, %d", udpNewFlowsPerSec, ok)
	}
}

func TestAdmitPPS(t *testing.T) {
	ul := newTestListener()
	now := time.Unix(1, 0)
	// Tunel PPS.
	lim := UDPLimits{MaxPPS: 5}.normalized()
	f := &udpFlow{}
	ok := 0
	for i := 0; i < 20; i++ {
		if ul.admit(lim, 10, f, 1, now) {
			ok++
		}
	}
	if ok != 5 {
		t.Fatalf("tunel PPS 5 bekleniyordu, %d", ok)
	}
	// Flow PPS: iki flow ayri kovalara sahip.
	ul = newTestListener()
	lim = UDPLimits{MaxFlowPPS: 3}.normalized()
	a, b := &udpFlow{}, &udpFlow{}
	okA, okB := 0, 0
	for i := 0; i < 10; i++ {
		if ul.admit(lim, 10, a, 2, now) {
			okA++
		}
		if ul.admit(lim, 10, b, 2, now) {
			okB++
		}
	}
	if okA != 3 || okB != 3 {
		t.Fatalf("flow basina 3 bekleniyordu, a=%d b=%d", okA, okB)
	}
	if got := ul.stats.droppedRate.Load(); got != 14 {
		t.Fatalf("droppedRate=14 bekleniyordu, %d", got)
	}
}

func TestCollectUDPMinutesDeltas(t *testing.T) {
	m := &Manager{udpLn: map[int]*udpListener{}}
	ul := newTestListener()
	m.udpLn[5000] = ul
	minute := time.Unix(60, 0).UTC()

	if rows := m.collectUDPMinutes(minute); len(rows) != 0 {
		t.Fatalf("sessiz dinleyici satir uretmemeli: %+v", rows)
	}
	ul.stats.packetsIn.Add(10)
	ul.stats.bytesIn.Add(1000)
	ul.stats.flowsNew.Add(2)
	ul.flows["a"] = &udpFlow{}
	ul.peakFlows = 3
	rows := m.collectUDPMinutes(minute)
	if len(rows) != 1 || rows[0].PacketsIn != 10 || rows[0].BytesIn != 1000 || rows[0].FlowsNew != 2 || rows[0].FlowsPeak != 3 {
		t.Fatalf("ilk fark yanlis: %+v", rows)
	}
	if rows[0].TunnelID != "t1" || rows[0].TenantID != "ten" {
		t.Fatalf("kimlikler yanlis: %+v", rows[0])
	}
	// Ikinci toplama yalnizca yeni farki verir; tepe su anki flow sayisina iner.
	ul.stats.packetsIn.Add(4)
	rows = m.collectUDPMinutes(minute.Add(time.Minute))
	if len(rows) != 1 || rows[0].PacketsIn != 4 || rows[0].BytesIn != 0 || rows[0].FlowsPeak != 1 {
		t.Fatalf("ikinci fark yanlis: %+v", rows)
	}
	live, ok := m.UDPStats("t1")
	if !ok || live.PacketsIn != 14 || live.ActiveFlows != 1 || live.Port != 5000 {
		t.Fatalf("canli istatistik yanlis: %+v ok=%v", live, ok)
	}
}
