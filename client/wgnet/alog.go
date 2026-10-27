package wgnet

import (
	"context"
	"net/netip"
	"sort"
	"sync"
	"sync/atomic"
	"time"

	"github.com/tkodcumpeg4/zorven/shared/protocol"
)

// Z5: Mod B erisim gunlugu (istemci tarafi). Filtre, bu dugume GELEN akislari
// (ACL ile izin verilen ve reddedilen) bellekte ozetler; Node periyodik olarak
// net_access_log gonderir. GIZLILIK: yalniz ust veri (kaynak IP, proto, port,
// karar, sayaclar); paket icerigi saklanmaz. Bellek SINIRLI: pencere basina en
// fazla alogMaxKeys farkli anahtar; fazlasi yalniz "truncated" sayacini artirir.
// Yerel baslatilan akislarin donusleri (conntrack) gunluge GIRMEZ.

const (
	alogMaxKeys  = 512
	alogInterval = 30 * time.Second
)

type alogKey struct {
	src   netip.Addr
	proto uint8
	port  uint16
	allow bool
	rule  string
}

type alogVal struct {
	flows   int
	packets int64
	bytes   int64
}

type accessAgg struct {
	on        atomic.Bool
	mu        sync.Mutex
	m         map[alogKey]*alogVal
	truncated int
}

func (a *accessAgg) record(p *pktInfo, size int, allow bool, rule string) {
	if !a.on.Load() {
		return
	}
	k := alogKey{src: p.src, proto: p.proto, allow: allow, rule: rule}
	newFlow := false
	switch p.proto {
	case protoTCP:
		k.port = p.dstPort
		newFlow = p.tcpFlags&tcpSYN != 0 && p.tcpFlags&tcpACK == 0
	case protoUDP:
		k.port = p.dstPort
	case protoICMP:
		newFlow = p.icmpType == icmpEchoRequest
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.m == nil {
		a.m = map[alogKey]*alogVal{}
	}
	v := a.m[k]
	if v == nil {
		if len(a.m) >= alogMaxKeys {
			a.truncated++
			return
		}
		v = &alogVal{}
		a.m[k] = v
		if p.proto == protoUDP {
			v.flows = 1 // UDP: pencerede ilk paket = akis
		}
	}
	if newFlow {
		v.flows++
	}
	v.packets++
	v.bytes += int64(size)
}

func protoName(p uint8) string {
	switch p {
	case protoTCP:
		return "tcp"
	case protoUDP:
		return "udp"
	case protoICMP:
		return "icmp"
	}
	return "other"
}

// drain, birikimi bosaltir. En kalabalik anahtarlar once; MaxAccessLogEntries ile sinirli.
func (a *accessAgg) drain() (entries []protocol.NetAccessEntry, truncated int) {
	a.mu.Lock()
	m, tr := a.m, a.truncated
	a.m, a.truncated = nil, 0
	a.mu.Unlock()
	for k, v := range m {
		d := protocol.AccessDeny
		if k.allow {
			d = protocol.AccessAllow
		}
		entries = append(entries, protocol.NetAccessEntry{
			SrcIP: k.src.String(), Proto: protoName(k.proto), Port: int(k.port), Decision: d,
			Rule: k.rule, Flows: v.flows, Packets: v.packets, Bytes: v.bytes,
		})
	}
	sort.Slice(entries, func(i, j int) bool {
		if entries[i].Packets != entries[j].Packets {
			return entries[i].Packets > entries[j].Packets
		}
		return entries[i].SrcIP+entries[i].Proto < entries[j].SrcIP+entries[j].Proto
	})
	if len(entries) > protocol.MaxAccessLogEntries {
		truncated += len(entries) - protocol.MaxAccessLogEntries
		entries = entries[:protocol.MaxAccessLogEntries]
	}
	return entries, tr + truncated
}

// SetAccessLog, erisim ozetlemeyi acar/kapatir.
func (f *Filter) SetAccessLog(on bool) { f.alog.on.Store(on) }

// DrainAccessLog, birikmis ozeti alir ve sifirlar.
func (f *Filter) DrainAccessLog() ([]protocol.NetAccessEntry, int) { return f.alog.drain() }

// alogLoop, Mod B acikken ozetleri periyodik gonderir. done kapaninca durur.
func (n *Node) alogLoop(f *Filter, done <-chan struct{}) {
	iv := n.ALogInterval
	if iv <= 0 {
		iv = alogInterval
	}
	tk := time.NewTicker(iv)
	defer tk.Stop()
	for {
		select {
		case <-n.stop:
			return
		case <-done:
			return
		case <-tk.C:
			entries, trunc := f.DrainAccessLog()
			if len(entries) == 0 && trunc == 0 {
				continue
			}
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			_ = n.tr.SendControl(ctx, protocol.NetAccessLog{
				Type: protocol.TypeNetAccessLog, WindowSec: int(iv / time.Second), Entries: entries, Truncated: trunc,
			}, protocol.TypeNetAccessLog)
			cancel()
		}
	}
}
