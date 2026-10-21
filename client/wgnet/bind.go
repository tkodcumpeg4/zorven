package wgnet

import (
	"encoding/binary"
	"errors"
	"net"
	"net/netip"
	"sync"
	"sync/atomic"

	"github.com/tkodcumpeg4/zorven/shared/protocol"
	"golang.zx2c4.com/wireguard/conn"
)

const (
	bindQueue = 512
	frameBuf  = 2048
)

// relayEP, es dugumun ic IPv4 adresi (FrameWG.ReqID). Port anlamsizdir.
type relayEP struct{ ip netip.Addr }

func (e relayEP) ClearSrc()           {}
func (e relayEP) SrcToString() string { return "" }
func (e relayEP) DstToString() string { return netip.AddrPortFrom(e.ip, 1).String() }
func (e relayEP) DstToBytes() []byte  { return e.ip.AsSlice() }
func (e relayEP) DstIP() netip.Addr   { return e.ip }
func (e relayEP) SrcIP() netip.Addr   { return netip.Addr{} }

type inPkt struct {
	ip  netip.Addr
	buf []byte
}

var framePool = sync.Pool{New: func() any { b := make([]byte, frameBuf); return &b }}

// relayBind, WireGuard paketlerini agent WSS'i uzerinden sunucu relay'ine
// tasiyan conn.Bind. Kuyruklar SINIRLIDIR ve dolunca paket DUSURULUR
// (datagram semantigi; TCP-over-TCP birikmesini onler).
type relayBind struct {
	in  chan inPkt
	out chan *[]byte // tam WS ikili cerceve (14 bayt baslik + sifreli paket)

	mu   sync.Mutex
	done chan struct{}
	seq  atomic.Uint32

	Dropped atomic.Int64
}

func newRelayBind() *relayBind {
	return &relayBind{in: make(chan inPkt, bindQueue), out: make(chan *[]byte, bindQueue)}
}

func (b *relayBind) Open(port uint16) ([]conn.ReceiveFunc, uint16, error) {
	b.mu.Lock()
	b.done = make(chan struct{})
	done := b.done
	b.mu.Unlock()
	fn := func(pk [][]byte, sizes []int, eps []conn.Endpoint) (int, error) {
		select {
		case m := <-b.in:
			sizes[0] = copy(pk[0], m.buf)
			eps[0] = relayEP{m.ip}
			return 1, nil
		case <-done:
			return 0, net.ErrClosed
		}
	}
	return []conn.ReceiveFunc{fn}, 1, nil
}

func (b *relayBind) Close() error {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.done != nil {
		select {
		case <-b.done:
		default:
			close(b.done)
		}
	}
	return nil
}

func (b *relayBind) SetMark(uint32) error { return nil }
func (b *relayBind) BatchSize() int       { return 1 }

func (b *relayBind) ParseEndpoint(s string) (conn.Endpoint, error) {
	ap, err := netip.ParseAddrPort(s)
	if err != nil {
		return nil, err
	}
	return relayEP{ap.Addr()}, nil
}

func (b *relayBind) Send(bufs [][]byte, ep conn.Endpoint) error {
	re, ok := ep.(relayEP)
	if !ok {
		return errors.New("beklenmeyen endpoint")
	}
	id := protocol.IPToRelayID(re.ip)
	for _, p := range bufs {
		if len(p)+protocol.HeaderSize > frameBuf {
			continue
		}
		bp := framePool.Get().(*[]byte)
		f := (*bp)[:protocol.HeaderSize+len(p)]
		f[0] = protocol.FrameWG
		binary.BigEndian.PutUint64(f[1:9], id)
		binary.BigEndian.PutUint32(f[9:13], b.seq.Add(1))
		f[13] = 0
		copy(f[protocol.HeaderSize:], p)
		*bp = f
		select {
		case b.out <- bp:
		default:
			b.Dropped.Add(1)
			framePool.Put(bp)
		}
	}
	return nil
}

// deliver: sunucudan gelen sifreli paketi wireguard-go'ya verir (dolu ise dusurur).
func (b *relayBind) deliver(srcID uint64, payload []byte) {
	cp := make([]byte, len(payload))
	copy(cp, payload)
	select {
	case b.in <- inPkt{ip: protocol.RelayIDToIP(srcID), buf: cp}:
	default:
		b.Dropped.Add(1)
	}
}

func (b *relayBind) release(bp *[]byte) {
	*bp = (*bp)[:cap(*bp)]
	framePool.Put(bp)
}
