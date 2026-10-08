package rawproxy

import (
	"context"
	"math"
	"net"
	"sync"
	"sync/atomic"
	"time"

	"github.com/tkodcumpeg4/zorven/server/tunnel"
)

// UDP rezerve-port tuneli (FAZ 3 / D2, Mod A). UDP baglantisizdir: her kaynak
// adres (ziyaretci) bir "flow" olusturur; flow tunelin ajan oturumuna bir ham
// akis (RawStream) ile baglanir. Bosta kalan flow'lar idle timeout ile silinir.
//
// FAZ 4 / F24: tunel basina sinirlar (paket boyu, tunel/flow PPS, es zamanli
// flow), yeni-flow hiz siniri ve istatistik sayaclari. Sinirlar okuma
// dongusunde, HERHANGI bir arama (IP filtresi, oturum) yapilmadan ONCE
// uygulanir: sahte kaynak adresli bir sel veritabanina veya ajana ulasamaz.

const (
	udpSweepTick = 5 * time.Second
	udpReadBuf   = 64 * 1024

	// udpNewFlowsPerSec, bir dinleyicide saniyede acilabilecek yeni flow sayisi.
	// Her yeni flow ajan tarafinda bir soket actigi icin sabit bir ust sinir;
	// kullanici ayari degil, platform korumasi.
	udpNewFlowsPerSec = 200

	defaultUDPIdle      = 90 * time.Second
	defaultUDPMaxPacket = 65507
	defaultUDPMaxFlows  = 1024
)

// UDPLimits, bir UDP tunelinin sinirlari. Sifir alanlar varsayilana duser
// (MaxPPS / MaxFlowPPS icin sifir = sinirsiz).
type UDPLimits struct {
	IdleTimeout time.Duration
	MaxPacket   int
	MaxPPS      int
	MaxFlowPPS  int
	MaxFlows    int
}

func (l UDPLimits) normalized() UDPLimits {
	if l.IdleTimeout <= 0 {
		l.IdleTimeout = defaultUDPIdle
	}
	if l.MaxPacket <= 0 || l.MaxPacket > defaultUDPMaxPacket {
		l.MaxPacket = defaultUDPMaxPacket
	}
	if l.MaxFlows <= 0 {
		l.MaxFlows = defaultUDPMaxFlows
	}
	if l.MaxPPS < 0 {
		l.MaxPPS = 0
	}
	if l.MaxFlowPPS < 0 {
		l.MaxFlowPPS = 0
	}
	return l
}

// tokenBucket, saniyede rate adet olaya izin verir (patlama = 1 saniyelik pay).
// Yalnizca tek goroutine'den (okuma dongusu) kullanilir; kilit yok.
type tokenBucket struct {
	tokens float64
	last   time.Time
}

func (b *tokenBucket) allow(rate int, now time.Time) bool {
	if rate <= 0 {
		return true
	}
	if b.last.IsZero() {
		b.tokens = float64(rate)
	} else {
		b.tokens += now.Sub(b.last).Seconds() * float64(rate)
		if b.tokens > float64(rate) {
			b.tokens = float64(rate)
		}
	}
	b.last = now
	if b.tokens < 1 {
		return false
	}
	b.tokens--
	return true
}

type udpFlow struct {
	stream   *tunnel.RawStream
	cancel   context.CancelFunc
	lastSeen int64       // unix nano (mu altinda)
	bucket   tokenBucket // flow PPS (yalnizca okuma dongusu)

	// Web-door: yalnizca grant sayesinde kabul edilen flow'lar icin.
	grantExp time.Time // sifir degilse flow bu ana kadar yasayabilir
	unreg    func()    // grant iptalinde kesilebilmesi icin kayit silici
}

// udpCounters, dinleyici basladigindan beri biriken sayaclar.
type udpCounters struct {
	packetsIn, packetsOut    atomic.Int64
	bytesIn, bytesOut        atomic.Int64
	flowsNew                 atomic.Int64
	droppedRate, droppedSize atomic.Int64
	droppedFlows             atomic.Int64
	ppsInBits, ppsOutBits    atomic.Uint64 // float64 bitleri
}

type udpListener struct {
	conn *net.UDPConn
	ti   atomic.Pointer[TunnelInfo]
	m    *Manager

	mu        sync.Mutex
	flows     map[string]*udpFlow
	peakFlows int // son istatistik toplamasindan beri en yuksek es zamanli flow (mu altinda)

	stats   udpCounters
	started time.Time
	flushed udpSnapshot // son dakikalik toplamadaki sayaclar (yalnizca toplayici)

	tunnelBucket  tokenBucket // tunel PPS (yalnizca okuma dongusu)
	newFlowBucket tokenBucket // yeni flow hizi (yalnizca okuma dongusu)

	done      chan struct{}
	closeOnce sync.Once
}

func (m *Manager) newUDPListener(port int, ti TunnelInfo) (*udpListener, error) {
	pc, err := net.ListenUDP("udp", &net.UDPAddr{Port: port})
	if err != nil {
		return nil, err
	}
	ul := &udpListener{
		conn:    pc,
		m:       m,
		flows:   map[string]*udpFlow{},
		done:    make(chan struct{}),
		started: time.Now(),
	}
	ul.setInfo(ti)
	go ul.readLoop()
	go ul.idleSweep()
	go ul.rateLoop()
	return ul, nil
}

// setInfo, tunel bilgisini (istemci, sinirlar) canli gunceller; dinleyici
// yeniden acilmaz, mevcut flow'lar korunur.
func (ul *udpListener) setInfo(ti TunnelInfo) {
	ti.UDP = ti.UDP.normalized()
	ul.ti.Store(&ti)
}

func (ul *udpListener) info() TunnelInfo { return *ul.ti.Load() }

func (ul *udpListener) close() {
	ul.closeOnce.Do(func() {
		close(ul.done)
		ul.conn.Close()
		ul.mu.Lock()
		for k, f := range ul.flows {
			f.cancel()
			f.stream.Close("listener closed")
			delete(ul.flows, k)
		}
		ul.mu.Unlock()
	})
}

func (ul *udpListener) readLoop() {
	buf := make([]byte, udpReadBuf)
	for {
		n, src, err := ul.conn.ReadFromUDP(buf)
		if err != nil {
			return // dinleyici kapandi
		}
		ul.handle(src, buf[:n])
	}
}

// admit, gelen datagramin sinirlardan gecip gecmedigini soyler; flow henuz
// yoksa (f == nil) yeni flow acilabilir mi ona da bakar. Yalnizca okuma
// dongusunden cagrilir. Arama (IP filtresi, oturum) YAPMAZ.
func (ul *udpListener) admit(lim UDPLimits, size int, f *udpFlow, active int, now time.Time) bool {
	if size > lim.MaxPacket {
		ul.stats.droppedSize.Add(1)
		return false
	}
	if !ul.tunnelBucket.allow(lim.MaxPPS, now) {
		ul.stats.droppedRate.Add(1)
		return false
	}
	if f == nil {
		if active >= lim.MaxFlows || !ul.newFlowBucket.allow(udpNewFlowsPerSec, now) {
			ul.stats.droppedFlows.Add(1)
			return false
		}
		return true
	}
	if !f.bucket.allow(lim.MaxFlowPPS, now) {
		ul.stats.droppedRate.Add(1)
		return false
	}
	return true
}

// handle, tek bir gelen datagrami isler. Yalnizca okuma dongusunden cagrilir.
func (ul *udpListener) handle(src *net.UDPAddr, data []byte) {
	ti := ul.info()
	now := time.Now()

	key := src.String()
	ul.mu.Lock()
	f := ul.flows[key]
	active := len(ul.flows)
	ul.mu.Unlock()

	if !ul.admit(ti.UDP, len(data), f, active, now) {
		return
	}
	if f == nil {
		if f = ul.openFlow(ti, src, key); f == nil {
			return
		}
		// Yeni flow'un ilk paketi flow PPS kovasindan da duser.
		f.bucket.allow(ti.UDP.MaxFlowPPS, now)
	}

	ul.touch(f)
	ul.stats.packetsIn.Add(1)
	ul.stats.bytesIn.Add(int64(len(data)))
	payload := make([]byte, len(data))
	copy(payload, data)
	// Datagrami ajana ilet. Akis henuz hazir degilse ajan tarafinda dusulur;
	// UDP zaten kayipli oldugundan istemci yeniden gonderir.
	_ = f.stream.Send(context.Background(), payload)
}

// openFlow, yeni kaynak adres icin ajana akis acar. Basarisizsa nil.
func (ul *udpListener) openFlow(ti TunnelInfo, src *net.UDPAddr, key string) *udpFlow {
	// IP izin listesi ve/veya web-door grant'i (yalnizca yeni flow'da; paket basina degil).
	ok, grantExp := ul.m.admit(ul.m.ctx(), ti, src.IP.String())
	if !ok {
		return nil
	}
	sess, online := ul.m.Hub.Get(ti.ClientID)
	if !online {
		return nil
	}
	ctx, cancel := context.WithCancel(ul.m.ctx())
	stream, err := sess.OpenStream(ctx, ti.TunnelID, "udp", key)
	if err != nil {
		cancel()
		return nil
	}
	f := &udpFlow{stream: stream, cancel: cancel, lastSeen: time.Now().UnixNano(), grantExp: grantExp}
	if !grantExp.IsZero() {
		f.unreg = ul.m.doorConns.add(ti.TunnelID, src.IP.String(), func() { ul.removeFlow(key, f) })
	}
	ul.mu.Lock()
	ul.flows[key] = f
	if len(ul.flows) > ul.peakFlows {
		ul.peakFlows = len(ul.flows)
	}
	ul.mu.Unlock()
	ul.stats.flowsNew.Add(1)
	go ul.pumpFromLocal(f, src, key)
	return f
}

// pumpFromLocal, ajandan gelen (yerel->ziyaretci) datagramlari kaynak adrese yazar.
func (ul *udpListener) pumpFromLocal(f *udpFlow, src *net.UDPAddr, key string) {
	// Baglanti sonucunu bekle.
	select {
	case ack := <-f.stream.Accept():
		if ack.Code != "" {
			ul.removeFlow(key, f)
			return
		}
	case <-time.After(streamAckTimeout):
		ul.removeFlow(key, f)
		return
	case <-f.stream.Closed():
		ul.removeFlow(key, f)
		return
	case <-ul.done:
		return
	}

	for {
		select {
		case data := <-f.stream.FromLocal():
			if _, err := ul.conn.WriteToUDP(data, src); err != nil {
				ul.removeFlow(key, f)
				return
			}
			ul.stats.packetsOut.Add(1)
			ul.stats.bytesOut.Add(int64(len(data)))
			ul.touch(f)
		case <-f.stream.Closed():
			ul.removeFlow(key, f)
			return
		case <-ul.done:
			return
		}
	}
}

func (ul *udpListener) touch(f *udpFlow) {
	ul.mu.Lock()
	f.lastSeen = time.Now().UnixNano()
	ul.mu.Unlock()
}

func (ul *udpListener) removeFlow(key string, f *udpFlow) {
	ul.mu.Lock()
	if cur, ok := ul.flows[key]; ok && cur == f {
		delete(ul.flows, key)
	}
	ul.mu.Unlock()
	if f.unreg != nil {
		f.unreg()
	}
	f.cancel()
	f.stream.Close("flow closed")
}

func (ul *udpListener) idleSweep() {
	tk := time.NewTicker(udpSweepTick)
	defer tk.Stop()
	for {
		select {
		case <-ul.done:
			return
		case <-tk.C:
			cutoff := time.Now().Add(-ul.info().UDP.IdleTimeout).UnixNano()
			type staleFlow struct {
				key string
				f   *udpFlow
			}
			var stale []staleFlow
			ul.mu.Lock()
			for k, f := range ul.flows {
				if f.lastSeen < cutoff || (!f.grantExp.IsZero() && time.Now().After(f.grantExp)) {
					stale = append(stale, staleFlow{k, f})
				}
			}
			ul.mu.Unlock()
			for _, s := range stale {
				ul.removeFlow(s.key, s.f)
			}
		}
	}
}

// rateLoop, saniyelik paket hizlarini (PPS) olcer.
func (ul *udpListener) rateLoop() {
	tk := time.NewTicker(time.Second)
	defer tk.Stop()
	lastIn, lastOut := ul.stats.packetsIn.Load(), ul.stats.packetsOut.Load()
	last := time.Now()
	for {
		select {
		case <-ul.done:
			return
		case now := <-tk.C:
			in, out := ul.stats.packetsIn.Load(), ul.stats.packetsOut.Load()
			if dt := now.Sub(last).Seconds(); dt > 0 {
				ul.stats.ppsInBits.Store(math.Float64bits(float64(in-lastIn) / dt))
				ul.stats.ppsOutBits.Store(math.Float64bits(float64(out-lastOut) / dt))
			}
			lastIn, lastOut, last = in, out, now
		}
	}
}

// --- Istatistikler -----------------------------------------------------------

// UDPLiveStats, bir UDP tunelinin canli durumu (dinleyici acildigindan beri).
type UDPLiveStats struct {
	TunnelID     string    `json:"tunnel_id"`
	Port         int       `json:"port"`
	Since        time.Time `json:"since"`
	ActiveFlows  int       `json:"active_flows"`
	PPSIn        float64   `json:"pps_in"`
	PPSOut       float64   `json:"pps_out"`
	PacketsIn    int64     `json:"packets_in"`
	PacketsOut   int64     `json:"packets_out"`
	BytesIn      int64     `json:"bytes_in"`
	BytesOut     int64     `json:"bytes_out"`
	FlowsTotal   int64     `json:"flows_total"`
	DroppedRate  int64     `json:"dropped_rate"`
	DroppedSize  int64     `json:"dropped_size"`
	DroppedFlows int64     `json:"dropped_flows"`
}

// UDPMinute, bir dinleyicinin son toplamadan bu yana biriken farki.
type UDPMinute struct {
	TunnelID, TenantID                     string
	Minute                                 time.Time
	PacketsIn, PacketsOut                  int64
	BytesIn, BytesOut                      int64
	FlowsNew                               int64
	FlowsPeak                              int
	DroppedRate, DroppedSize, DroppedFlows int64
}

type udpSnapshot struct {
	packetsIn, packetsOut, bytesIn, bytesOut, flowsNew int64
	droppedRate, droppedSize, droppedFlows             int64
}

func (ul *udpListener) snapshot() udpSnapshot {
	return udpSnapshot{
		packetsIn: ul.stats.packetsIn.Load(), packetsOut: ul.stats.packetsOut.Load(),
		bytesIn: ul.stats.bytesIn.Load(), bytesOut: ul.stats.bytesOut.Load(),
		flowsNew:    ul.stats.flowsNew.Load(),
		droppedRate: ul.stats.droppedRate.Load(), droppedSize: ul.stats.droppedSize.Load(),
		droppedFlows: ul.stats.droppedFlows.Load(),
	}
}

func (ul *udpListener) live(port int) UDPLiveStats {
	ul.mu.Lock()
	active := len(ul.flows)
	ul.mu.Unlock()
	s := ul.snapshot()
	return UDPLiveStats{
		TunnelID: ul.info().TunnelID, Port: port, Since: ul.started, ActiveFlows: active,
		PPSIn:     math.Float64frombits(ul.stats.ppsInBits.Load()),
		PPSOut:    math.Float64frombits(ul.stats.ppsOutBits.Load()),
		PacketsIn: s.packetsIn, PacketsOut: s.packetsOut, BytesIn: s.bytesIn, BytesOut: s.bytesOut,
		FlowsTotal: s.flowsNew, DroppedRate: s.droppedRate, DroppedSize: s.droppedSize,
		DroppedFlows: s.droppedFlows,
	}
}

// UDPStats, tunelin canli UDP istatistikleri. Tunelin acik UDP dinleyicisi
// yoksa ok=false.
func (m *Manager) UDPStats(tunnelID string) (UDPLiveStats, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for port, ul := range m.udpLn {
		if ul.info().TunnelID == tunnelID {
			return ul.live(port), true
		}
	}
	return UDPLiveStats{}, false
}

// collectUDPMinutes, her dinleyicinin son toplamadan bu yana farkini doner.
// Yalnizca RunUDPStats goroutine'inden cagrilir.
func (m *Manager) collectUDPMinutes(minute time.Time) []UDPMinute {
	m.mu.Lock()
	lns := make([]*udpListener, 0, len(m.udpLn))
	for _, ul := range m.udpLn {
		lns = append(lns, ul)
	}
	m.mu.Unlock()

	out := make([]UDPMinute, 0, len(lns))
	for _, ul := range lns {
		cur := ul.snapshot()
		prev := ul.flushed
		ul.flushed = cur
		ul.mu.Lock()
		peak := ul.peakFlows
		ul.peakFlows = len(ul.flows)
		ul.mu.Unlock()
		ti := ul.info()
		d := UDPMinute{
			TunnelID: ti.TunnelID, TenantID: ti.TenantID, Minute: minute,
			PacketsIn: cur.packetsIn - prev.packetsIn, PacketsOut: cur.packetsOut - prev.packetsOut,
			BytesIn: cur.bytesIn - prev.bytesIn, BytesOut: cur.bytesOut - prev.bytesOut,
			FlowsNew: cur.flowsNew - prev.flowsNew, FlowsPeak: peak,
			DroppedRate: cur.droppedRate - prev.droppedRate, DroppedSize: cur.droppedSize - prev.droppedSize,
			DroppedFlows: cur.droppedFlows - prev.droppedFlows,
		}
		// Tamamen sessiz dakikalar yazilmaz (bos satir kalabaligi olmasin).
		if d.PacketsIn == 0 && d.PacketsOut == 0 && d.FlowsNew == 0 && d.FlowsPeak == 0 &&
			d.DroppedRate == 0 && d.DroppedSize == 0 && d.DroppedFlows == 0 {
			continue
		}
		out = append(out, d)
	}
	return out
}

// RunUDPStats, her dakika dinleyicilerin farklarini sink'e verir. Sink hatasi
// o dakikanin verisini kaybettirir; UDP trafigi hicbir sekilde etkilenmez.
func (m *Manager) RunUDPStats(ctx context.Context, sink func(context.Context, []UDPMinute)) {
	tk := time.NewTicker(time.Minute)
	defer tk.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case now := <-tk.C:
			if rows := m.collectUDPMinutes(now.Add(-time.Second).UTC().Truncate(time.Minute)); len(rows) > 0 {
				sink(ctx, rows)
			}
		}
	}
}
