package rawproxy

import (
	"context"
	"fmt"
	"math"
	"net"
	"sync"
	"sync/atomic"
	"time"

	"github.com/tkodcumpeg4/zorven/server/bandwidth"
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

// Varsayilan PPS sinirlari: oyun/VoIP tipik hizlari (20-200 pps/flow) bunlarin
// cok altindadir. Asan paketler kuyruga alinmaz, dusurulur (dropped_rate).
//
// Ortam degiskenleri (main.go okur, SetUDPDefaultPPS ile uygular):
//
//	ZORVEN_UDP_FLOW_PPS    flow basina varsayilan (vars. 2000; 0 = sinirsiz)
//	ZORVEN_UDP_TUNNEL_PPS  tunel toplam varsayilani (vars. 20000; 0 = sinirsiz)
//
// Tunel kaydinda max_pps / max_flow_pps > 0 ise o deger gecerlidir; 0 ise bu
// varsayilanlar uygulanir. Kova 1 saniyelik patlama payi tutar.
const (
	DefaultUDPFlowPPS   = 2000
	DefaultUDPTunnelPPS = 20000

	udpRateLogEvery = time.Minute
)

var (
	udpDefFlowPPS   atomic.Int64
	udpDefTunnelPPS atomic.Int64
)

func init() {
	udpDefFlowPPS.Store(DefaultUDPFlowPPS)
	udpDefTunnelPPS.Store(DefaultUDPTunnelPPS)
}

// SetUDPDefaultPPS, tunel kaydinda deger verilmeyen (0) tuneller icin flow ve
// tunel PPS varsayilanlarini ayarlar. 0 = sinirsiz, negatif = varsayilana don.
func SetUDPDefaultPPS(flow, tunnel int) {
	if flow < 0 {
		flow = DefaultUDPFlowPPS
	}
	if tunnel < 0 {
		tunnel = DefaultUDPTunnelPPS
	}
	udpDefFlowPPS.Store(int64(flow))
	udpDefTunnelPPS.Store(int64(tunnel))
}

// UDPLimits, bir UDP tunelinin sinirlari. Sifir alanlar varsayilana duser
// (MaxPPS / MaxFlowPPS icin sifir = SetUDPDefaultPPS varsayilani; negatif = sinirsiz).
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
	switch {
	case l.MaxPPS < 0:
		l.MaxPPS = 0
	case l.MaxPPS == 0:
		l.MaxPPS = int(udpDefTunnelPPS.Load())
	}
	switch {
	case l.MaxFlowPPS < 0:
		l.MaxFlowPPS = 0
	case l.MaxFlowPPS == 0:
		l.MaxFlowPPS = int(udpDefFlowPPS.Load())
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
	stream   bridgeStream
	cancel   context.CancelFunc
	lastSeen int64       // unix nano (mu altinda)
	bucket   tokenBucket // flow PPS (yalnizca okuma dongusu)

	// Web-door: yalnizca grant sayesinde kabul edilen flow'lar icin.
	grantExp time.Time // sifir degilse flow bu ana kadar yasayabilir
	unreg    func()    // grant iptalinde kesilebilmesi icin kayit silici

	// unregLive: canli akis defteri (Reevaluate) kaydini siler.
	unregLive func()

	// clientID, flow'un yapistigi istemci (birincil ya da replika). Flow omru
	// boyunca degismez; oturum gidince flow silinir ve sonraki paket yeniden eslenir.
	clientID string
	// release, replikali tunelde istemcinin acik-flow sayacini azaltir (tek sefer;
	// nil = replikasiz flow).
	release func()
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
	rateLogAt     time.Time   // son hiz-siniri logu (yalnizca okuma dongusu)

	// meter: kiracinin aylik kullanimi + hiz siniri (nil = yok). Dinleyici
	// basina tek Batcher; datagram basina yalnizca atomikler, rateLoop (1 sn)
	// ve close'da flush.
	meter *bandwidth.Batcher

	done      chan struct{}
	closeOnce sync.Once
}

func (m *Manager) newUDPListener(bind string, port int, ti TunnelInfo) (*udpListener, error) {
	ua := &net.UDPAddr{Port: port}
	if bind != "" {
		ip := net.ParseIP(bind)
		if ip == nil {
			return nil, fmt.Errorf("gecersiz bind adresi %q", bind)
		}
		ua.IP = ip
	}
	pc, err := net.ListenUDP("udp", ua)
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
	ul.meter = bandwidth.NewBatcher(m.Usage, ti.TenantID, bandwidth.UsageTypeUDP)
	go ul.readLoop()
	go ul.idleSweep()
	go ul.rateLoop()
	return ul, nil
}

// setInfo, tunel bilgisini (istemci, sinirlar) canli gunceller; dinleyici
// yeniden acilmaz, mevcut flow'lar korunur. Tek istisna replikali tunelde aday
// kumesi degistiginde: artik aday olmayan istemciye (kaldirilan replika) yapisik
// flow'lar birakilir, sonraki paket yeniden eslenir. Replikasiz tunelde ek is yok.
func (ul *udpListener) setInfo(ti TunnelInfo) {
	ti.UDP = ti.UDP.normalized()
	old := ul.ti.Swap(&ti)
	if old != nil && (len(old.Replicas) > 0 || len(ti.Replicas) > 0) && !sameCandidates(*old, ti) {
		// Asenkron: removeFlow ajana StreamClose yazar (bloklayabilir); setInfo
		// Manager.Reload icinden, m.mu altinda cagrilir.
		go ul.dropStaleFlows()
	}
}

// dropStaleFlows, tunelin GUNCEL aday kumesinde olmayan istemciye yapisik flow'lari kapatir.
func (ul *udpListener) dropStaleFlows() {
	keep := ul.info().candidates()
	type staleFlow struct {
		key string
		f   *udpFlow
	}
	var stale []staleFlow
	ul.mu.Lock()
	for k, f := range ul.flows {
		if f.clientID != "" && !containsID(keep, f.clientID) {
			stale = append(stale, staleFlow{k, f})
		}
	}
	ul.mu.Unlock()
	for _, s := range stale {
		ul.removeFlow(s.key, s.f)
	}
}

func (ul *udpListener) info() TunnelInfo { return *ul.ti.Load() }

func (ul *udpListener) close() {
	ul.closeOnce.Do(func() {
		close(ul.done)
		ul.conn.Close()
		ul.meter.Flush()
		ul.mu.Lock()
		for k, f := range ul.flows {
			f.cancel()
			f.stream.Close("listener closed")
			if f.release != nil {
				f.release()
			}
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
		ul.logRateDrop("tunnel", lim.MaxPPS, now)
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
		ul.logRateDrop("flow", lim.MaxFlowPPS, now)
		return false
	}
	return true
}

// logRateDrop, PPS siniri asimini tunel basina dakikada en fazla bir kez loglar.
func (ul *udpListener) logRateDrop(kind string, limit int, now time.Time) {
	if !ul.rateLogAt.IsZero() && now.Sub(ul.rateLogAt) < udpRateLogEvery {
		return
	}
	ul.rateLogAt = now
	if ul.m == nil || ul.m.Log == nil {
		return
	}
	ul.m.Log.Warn("rawproxy: udp pps siniri asildi, paketler dusuruluyor",
		"kind", kind, "limit_pps", limit, "tunnel", ul.info().TunnelID)
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

	// Kiraci bant siniri (kota asilinca dusuk hiz): token yoksa datagram dusulur.
	if !bandwidth.AllowN(ul.meter.Limiter(context.Background()), len(data)) {
		ul.stats.droppedRate.Add(1)
		return
	}
	ul.meter.Add(int64(len(data)), 0)
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
	var (
		stream  bridgeStream
		cid     = ti.ClientID
		release func()
		ctx     context.Context
		cancel  context.CancelFunc
	)
	if len(ti.Replicas) > 0 || ul.m.useRemote(ti.ClientID) {
		// (Oturum baska dugumdeyse tek adayli sira ile dugumler arasi tunel kullanilir.)
		// Replikali tunel: yeni flow icin aday secimi (replicas.go). Flow bu
		// oturuma yapisik kalir; oturum gidince silinir ve yeniden eslenir.
		ctx, cancel = context.WithCancel(ul.m.ctx())
		if stream, cid, release = ul.m.openReplicaUDP(ctx, ti, key); stream == nil {
			cancel()
			return nil
		}
	} else {
		sess, online := ul.m.Hub.Get(ti.ClientID)
		if !online {
			return nil
		}
		ctx, cancel = context.WithCancel(ul.m.ctx())
		rs, err := sess.OpenStream(ctx, ti.TunnelID, "udp", key)
		if err != nil {
			cancel()
			return nil
		}
		stream = rs
	}
	f := &udpFlow{stream: stream, cancel: cancel, lastSeen: time.Now().UnixNano(), grantExp: grantExp,
		clientID: cid, release: release}
	if !grantExp.IsZero() {
		f.unreg = ul.m.doorConns.add(ti.TunnelID, src.IP.String(), func() { ul.removeFlow(key, f) })
	}
	// Z2: her flow yeniden degerlendirme icin kaydedilir.
	f.unregLive = ul.m.registerLive(ti, src.IP.String(), true, func() { ul.removeFlow(key, f) })
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
			if !bandwidth.AllowN(ul.meter.Limiter(context.Background()), len(data)) {
				ul.stats.droppedRate.Add(1)
				continue
			}
			if _, err := ul.conn.WriteToUDP(data, src); err != nil {
				ul.removeFlow(key, f)
				return
			}
			ul.stats.packetsOut.Add(1)
			ul.stats.bytesOut.Add(int64(len(data)))
			ul.meter.Add(0, int64(len(data)))
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
	if f.unregLive != nil {
		f.unregLive()
	}
	f.cancel()
	f.stream.Close("flow closed")
	if f.release != nil {
		f.release()
	}
}

func (ul *udpListener) idleSweep() {
	tk := time.NewTicker(udpSweepTick)
	defer tk.Stop()
	for {
		select {
		case <-ul.done:
			return
		case <-tk.C:
			ul.sweepIdle(time.Now())
		}
	}
}

// sweepIdle, bosta kalan (IdleTimeout) ya da grant suresi dolan flow'lari kapatir.
// Replikali tunelde de ayni: bosta kalan esleme (flow) silinir, sonraki paket yeniden eslenir.
func (ul *udpListener) sweepIdle(now time.Time) {
	cutoff := now.Add(-ul.info().UDP.IdleTimeout).UnixNano()
	type staleFlow struct {
		key string
		f   *udpFlow
	}
	var stale []staleFlow
	ul.mu.Lock()
	for k, f := range ul.flows {
		if f.lastSeen < cutoff || (!f.grantExp.IsZero() && now.After(f.grantExp)) {
			stale = append(stale, staleFlow{k, f})
		}
	}
	ul.mu.Unlock()
	for _, s := range stale {
		ul.removeFlow(s.key, s.f)
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
			ul.meter.Flush()
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

	// ProbeSupported: istemci udp_probe'u destekliyor mu (panel "desteklemiyor" gostersin).
	// ProbeRTTMs, oturum boyunca ussel ortalama gecikme (yoksa nil).
	ProbeSupported bool     `json:"probe_supported"`
	ProbeRTTMs     *float64 `json:"probe_rtt_ms"`
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

	// Olcum (FeatureUDPProbe). ProbeSupported false ise istemci desteklemiyor
	// ya da cevrimdisi: RTT/kayip nil kalir. DroppedDatagrams kuyruk tasmalaridir.
	ProbeSupported   bool
	RTTP50Ms         *float64
	RTTP95Ms         *float64
	ProbeLossPct     *float64
	DroppedDatagrams int64
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
	for k, ul := range m.udpLn {
		if ti := ul.info(); ti.TunnelID == tunnelID {
			port := ti.Port
			if port == 0 {
				port = k.port
			}
			ls := ul.live(port)
			if m.Hub != nil {
				if sess, on := m.Hub.Get(ti.ClientID); on && sess.UDPProbeSupported() {
					ls.ProbeSupported = true
					if v, ok := sess.UDPProbes().EWMA(ti.TunnelID); ok {
						ls.ProbeRTTMs = &v
					}
				}
			}
			return ls, true
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
		ul.applyProbe(&d, ti)
		// Tamamen sessiz dakikalar yazilmaz (bos satir kalabaligi olmasin).
		if d.DroppedDatagrams == 0 && d.ProbeLossPct == nil && d.RTTP50Ms == nil &&
			d.PacketsIn == 0 && d.PacketsOut == 0 && d.FlowsNew == 0 && d.FlowsPeak == 0 &&
			d.DroppedRate == 0 && d.DroppedSize == 0 && d.DroppedFlows == 0 {
			continue
		}
		out = append(out, d)
	}
	return out
}

// applyProbe, istemci oturumunun yoklama penceresini dakikalik satira ekler.
func (ul *udpListener) applyProbe(d *UDPMinute, ti TunnelInfo) {
	if ul.m == nil || ul.m.Hub == nil {
		return
	}
	sess, online := ul.m.Hub.Get(ti.ClientID)
	if !online || !sess.UDPProbeSupported() {
		return
	}
	d.ProbeSupported = true
	w, ok := sess.UDPProbes().Drain(ti.TunnelID, time.Now())
	if !ok {
		return
	}
	d.DroppedDatagrams = w.Dropped
	if w.HasRTT {
		p50, p95 := w.P50Ms, w.P95Ms
		d.RTTP50Ms, d.RTTP95Ms = &p50, &p95
	}
	if l, ok := w.LossPct(); ok {
		d.ProbeLossPct = &l
	}
}

// probeOnce, acik her UDP dinleyicisinin istemcisine bir yoklama yollar.
// Istemci desteklemiyorsa ya da cevrimdisiysa atlanir.
func (m *Manager) probeOnce(ctx context.Context) {
	if m.Hub == nil {
		return
	}
	m.mu.Lock()
	infos := make([]TunnelInfo, 0, len(m.udpLn))
	for _, ul := range m.udpLn {
		infos = append(infos, ul.info())
	}
	m.mu.Unlock()
	for _, ti := range infos {
		sess, online := m.Hub.Get(ti.ClientID)
		if !online {
			continue
		}
		_, _ = sess.SendUDPProbe(ctx, ti.TunnelID)
	}
}

// RunUDPProbes, UDP tunelleri icin periyodik gecikme/kayip yoklamasi yollar.
func (m *Manager) RunUDPProbes(ctx context.Context) {
	tk := time.NewTicker(tunnel.UDPProbeInterval)
	defer tk.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-tk.C:
			m.probeOnce(ctx)
		}
	}
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
