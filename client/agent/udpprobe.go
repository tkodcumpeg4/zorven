package agent

import (
	"context"
	"sync"
	"time"

	"github.com/tkodcumpeg4/zorven/shared/protocol"
)

// UDP tunel olcumu (protocol.FeatureUDPProbe): sunucunun udp_probe mesajlarini
// aynen yankilar ve UDP datagram dusurmelerini periyodik bildirir.

// udpAgentStatsInterval, dusurme sayaclarinin bildirim araligi.
const udpAgentStatsInterval = 30 * time.Second

// udpDropCounter, tunel basina dusurulen datagram sayaci (fark mantigiyla alinir).
type udpDropCounter struct {
	mu sync.Mutex
	m  map[string]int64
}

func (c *udpDropCounter) add(tunnelID string, n int64) {
	if tunnelID == "" || n <= 0 {
		return
	}
	c.mu.Lock()
	if c.m == nil {
		c.m = map[string]int64{}
	}
	c.m[tunnelID] += n
	c.mu.Unlock()
}

// take, birikmis sayaclari alir ve sifirlar. Bos ise nil doner.
func (c *udpDropCounter) take() map[string]int64 {
	c.mu.Lock()
	defer c.mu.Unlock()
	if len(c.m) == 0 {
		return nil
	}
	out := c.m
	c.m = nil
	return out
}

// echoUDPProbe, yoklamayi aynen sunucuya geri yollar. Yerel servise gitmez:
// olcum yalnizca tunel ayagidir.
func (cs *clientSession) echoUDPProbe(ctx context.Context, p protocol.UDPProbe) {
	_ = cs.sendControl(ctx, protocol.UDPProbeEcho{
		Type: protocol.TypeUDPProbeEcho, TunnelID: p.TunnelID, Seq: p.Seq, TS: p.TS,
	}, protocol.TypeUDPProbeEcho)
}

// flushUDPAgentStats, sifirdan buyuk dusurme varsa tek mesajla bildirir.
func (cs *clientSession) flushUDPAgentStats(ctx context.Context) {
	d := cs.udpDrops.take()
	if d == nil {
		return
	}
	if err := cs.sendControl(ctx, protocol.UDPAgentStats{
		Type: protocol.TypeUDPAgentStats, Dropped: d,
	}, protocol.TypeUDPAgentStats); err != nil {
		// Gonderilemedi: sayaclari geri koy ki sonraki turda kaybolmasin.
		for id, n := range d {
			cs.udpDrops.add(id, n)
		}
	}
}

// runUDPAgentStats, oturum bitene kadar periyodik bildirim yapar.
func (cs *clientSession) runUDPAgentStats(ctx context.Context) {
	tk := time.NewTicker(udpAgentStatsInterval)
	defer tk.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-tk.C:
			cs.flushUDPAgentStats(ctx)
		}
	}
}
