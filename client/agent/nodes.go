package agent

// Dugum listesi, yakin dugum secimi ve yuk devri (Faz 0C-4).
//
// Ajan, ayar dosyasindaki sunucu adres(ler)ini tohum olarak kullanir; ilk
// baglantida HelloAck'in bildirdigi dugum listesini (protocol.NodeHint) dosyaya
// onbellekler. Baglanti koparsa once sunucunun atadigi dugume, sonra siralamaya
// gore digerlerine gecer; her basarisiz denemede bir sonraki aday kullanilir
// (araya Run dongusunun ustel geri cekilmesi girer).
//
// SIRALAMA KURALI (rankNodes), anahtar sirasiyla:
//  1. yakin zamanda basarisiz olan dugumler en sona (failCooldown suresince);
//     hepsi basarisizsa en ESKI basarisiz olan once (dongusel, tek adrese takilma yok)
//  2. saglikli olmayanlar saglikli olanlardan sonra
//  3. sunucunun atadigi dugum once (saglikli ve basarisiz degilse)
//  4. oncelik (kucuk once); tohum adresler hepsi seedPriority'dedir
//  5. TCP+TLS el sikisma suresi (kucuk once; olculmemis en sona)
//  6. tohum/liste sirasi (kararli)
//
// Kullanici sunucuyu sabitlerse (ServerPinned: `zorven config set server`,
// --server, ZORVEN_SERVER) liste hic kullanilmaz: tek adres, eski davranis.

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"log/slog"
	"net"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/tkodcumpeg4/zorven/shared/protocol"
)

const (
	// seedPriority, ayar dosyasindan gelen adreslerin onceligi: sunucunun
	// bildirdigi (normalde 0..n) dugumlerden sonra gelirler.
	seedPriority = 1000
	// failCooldown, ajanin en uzun geri cekilmesinden (60 sn; panelden artirilabilir)
	// cok uzun olmali: 0.3.4-0.3.6'da 30 sn idi, geri cekilme 60 sn'ye cikinca
	// isaret her denemede dusuyor ve ajan ayni bozuk dugume takilip tohuma hic
	// donmuyordu (2026-11-07 olayi).
	failCooldown = 15 * time.Minute
	probeTTL     = 2 * time.Minute
	probeTimeout = 3 * time.Second
	maxProbes    = 8
	unknownRTT   = time.Duration(1<<62 - 1)

	nodeCacheVersion  = 1
	maxNodeCacheBytes = 256 << 10
	maxNodeCacheAge   = 30 * 24 * time.Hour
	maxCachedNodes    = 64
)

// nodeCandidate, siralanmis bir baglanti adayi.
type nodeCandidate struct {
	Addr     string
	NodeID   string
	Region   string
	Priority int
	Healthy  bool
	Assigned bool
	Failed   bool
	FailedAt time.Time     // Failed ise son basarisizlik zamani
	RTT      time.Duration // 0: olculmedi
	order    int
}

type rankInput struct {
	Seeds    []string
	Nodes    []protocol.NodeHint
	Assigned string // atanmis dugum kimligi
	RTT      map[string]time.Duration
	Failed   map[string]bool
	FailedAt map[string]time.Time // istege bagli: basarisizlar arasi siralama
}

// isIPHost, "host:port" ya da "host" adresinin ana bilgisayar kisminin bir IP
// olup olmadigini soyler.
func isIPHost(addr string) bool {
	h := protocol.NormalizeNodeAddress(strings.TrimSpace(addr))
	if hh, _, err := net.SplitHostPort(h); err == nil {
		h = hh
	}
	return net.ParseIP(strings.Trim(h, "[]")) != nil
}

func sameAddr(a, b string) bool { return strings.EqualFold(strings.TrimSpace(a), strings.TrimSpace(b)) }

// rankNodes, adaylari yukaridaki kurala gore siralar (saf fonksiyon).
func rankNodes(in rankInput) []nodeCandidate {
	var out []nodeCandidate
	idx := map[string]int{}
	add := func(c nodeCandidate) {
		k := strings.ToLower(c.Addr)
		if c.Addr == "" {
			return
		}
		if i, ok := idx[k]; ok {
			// Ayni adres hem tohum hem listede: liste bilgisi (kimlik/oncelik) kazanir.
			if c.NodeID != "" && out[i].NodeID == "" {
				c.order = out[i].order
				out[i] = c
			}
			return
		}
		c.order = len(out)
		idx[k] = len(out)
		out = append(out, c)
	}
	ipSeed := len(in.Seeds) == 0 || isIPHost(in.Seeds[0])
	for _, n := range in.Nodes {
		addr := protocol.NormalizeNodeAddress(n.Address)
		// Tohum bir alan adiyken IP ipucu kullanilmaz: sunucu sertifikasi IP
		// icermez ve el sikisma "IP SANs" hatasiyla duser (0.3.4-0.3.6 sunuculari
		// ipucuya ic kume adresini koyuyordu). Tohum IP ise (self-host) serbest.
		if !ipSeed && isIPHost(addr) {
			continue
		}
		add(nodeCandidate{Addr: addr, NodeID: n.NodeID, Region: n.Region, Priority: n.Priority,
			Healthy: n.Healthy, Assigned: in.Assigned != "" && n.NodeID == in.Assigned})
	}
	for _, s := range in.Seeds {
		add(nodeCandidate{Addr: strings.TrimSpace(s), Priority: seedPriority, Healthy: true})
	}
	for i := range out {
		out[i].RTT = in.RTT[strings.ToLower(out[i].Addr)]
		out[i].Failed = in.Failed[strings.ToLower(out[i].Addr)]
		out[i].FailedAt = in.FailedAt[strings.ToLower(out[i].Addr)]
	}
	rtt := func(c nodeCandidate) time.Duration {
		if c.RTT <= 0 {
			return unknownRTT
		}
		return c.RTT
	}
	sort.SliceStable(out, func(i, j int) bool {
		a, b := out[i], out[j]
		if a.Failed != b.Failed {
			return !a.Failed
		}
		if a.Failed && !a.FailedAt.Equal(b.FailedAt) {
			return a.FailedAt.Before(b.FailedAt)
		}
		if a.Healthy != b.Healthy {
			return a.Healthy
		}
		if a.Assigned != b.Assigned {
			return a.Assigned
		}
		if a.Priority != b.Priority {
			return a.Priority < b.Priority
		}
		if ra, rb := rtt(a), rtt(b); ra != rb {
			return ra < rb
		}
		return a.order < b.order
	})
	return out
}

// ---- onbellek dosyasi -------------------------------------------------------

type nodeCacheFile struct {
	Version    int                 `json:"version"`
	Seed       string              `json:"seed"`
	Nodes      []protocol.NodeHint `json:"nodes"`
	Assigned   string              `json:"assigned,omitempty"`
	LastNodeID string              `json:"last_node_id,omitempty"`
	LastAddr   string              `json:"last_addr,omitempty"`
	SavedAt    time.Time           `json:"saved_at"`
}

// loadNodeCache, onbellegi okur. Dosya yok/bozuk/eski/baska tohuma ait ise
// (nil, false) doner: onbellek yalnizca bir ipucudur, asla hata sebebi olmaz.
func loadNodeCache(path, seed string, now time.Time) (*nodeCacheFile, bool) {
	if path == "" {
		return nil, false
	}
	fi, err := os.Stat(path)
	if err != nil || fi.Size() > maxNodeCacheBytes || !fi.Mode().IsRegular() {
		return nil, false
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, false
	}
	var c nodeCacheFile
	if err := json.Unmarshal(data, &c); err != nil {
		return nil, false
	}
	if c.Version != nodeCacheVersion || !sameAddr(c.Seed, seed) || len(c.Nodes) == 0 || len(c.Nodes) > maxCachedNodes {
		return nil, false
	}
	if !c.SavedAt.IsZero() && now.Sub(c.SavedAt) > maxNodeCacheAge {
		return nil, false
	}
	for i := range c.Nodes {
		c.Nodes[i].Address = protocol.NormalizeNodeAddress(c.Nodes[i].Address)
		if c.Nodes[i].Address == "" || c.Nodes[i].NodeID == "" {
			return nil, false
		}
	}
	return &c, true
}

// saveNodeCache, onbellegi atomik yazar (gecici dosya + rename, 0600).
func saveNodeCache(path string, c nodeCacheFile) error {
	if path == "" {
		return nil
	}
	c.Version = nodeCacheVersion
	data, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return nil
}

// ---- dugum defteri ----------------------------------------------------------

type rttEntry struct {
	d  time.Duration
	at time.Time
}

type probeFunc func(ctx context.Context, addr string) (time.Duration, error)

type nodeBook struct {
	mu        sync.Mutex
	seeds     []string // [ServerAddr, Servers...]
	cachePath string
	log       *slog.Logger
	probe     probeFunc
	now       func() time.Time

	nodes      []protocol.NodeHint
	assigned   string
	lastNodeID string
	lastAddr   string
	current    string
	failedAt   map[string]time.Time
	rtt        map[string]rttEntry
}

func newNodeBook(seeds []string, cachePath string, log *slog.Logger, probe probeFunc) *nodeBook {
	b := &nodeBook{cachePath: cachePath, log: log, probe: probe, now: time.Now,
		failedAt: map[string]time.Time{}, rtt: map[string]rttEntry{}}
	b.setSeeds(seeds)
	return b
}

// setSeeds tohum adresleri ayarlar; ilk tohum degisirse (reload) eski liste
// gecersizdir ve onbellekten yeniden yuklenir. Cagiran kilidi tutmalidir.
func (b *nodeBook) setSeeds(seeds []string) {
	var clean []string
	for _, s := range seeds {
		if s = strings.TrimSpace(s); s != "" {
			dup := false
			for _, c := range clean {
				dup = dup || sameAddr(c, s)
			}
			if !dup {
				clean = append(clean, s)
			}
		}
	}
	if len(b.seeds) > 0 && len(clean) > 0 && sameAddr(b.seeds[0], clean[0]) {
		b.seeds = clean
		return
	}
	b.seeds = clean
	b.nodes, b.assigned, b.lastNodeID, b.lastAddr, b.current = nil, "", "", "", ""
	b.failedAt = map[string]time.Time{}
	b.rtt = map[string]rttEntry{}
	if len(clean) > 0 {
		if c, ok := loadNodeCache(b.cachePath, clean[0], b.now()); ok {
			b.nodes, b.assigned, b.lastNodeID, b.lastAddr = c.Nodes, c.Assigned, c.LastNodeID, c.LastAddr
		}
	}
}

func (b *nodeBook) input() rankInput {
	now := b.now()
	in := rankInput{Seeds: b.seeds, Nodes: b.nodes, Assigned: b.assigned,
		RTT: map[string]time.Duration{}, Failed: map[string]bool{}, FailedAt: map[string]time.Time{}}
	for a, t := range b.failedAt {
		if now.Sub(t) < failCooldown {
			in.Failed[strings.ToLower(a)] = true
			in.FailedAt[strings.ToLower(a)] = t
		}
	}
	for a, e := range b.rtt {
		if now.Sub(e.at) < probeTTL {
			in.RTT[strings.ToLower(a)] = e.d
		}
	}
	return in
}

// needsProbe: atanmis ve calisir bir dugum varsa olcum gereksiz; aksi halde
// birden cok aday ve en az birinin taze olcumu yoksa gerekir.
func needsProbe(c []nodeCandidate) bool {
	if len(c) > 0 && c[0].Assigned && !c[0].Failed {
		return false
	}
	live, missing := 0, 0
	for _, x := range c {
		if x.Failed {
			continue
		}
		live++
		if x.RTT <= 0 {
			missing++
		}
	}
	return live > 1 && missing > 0
}

// pick, bu deneme icin kullanilacak adresi doner (bos aday listesinde
// fallback). Gerekirse adaylarin TCP+TLS el sikisma suresini paralel olcer.
func (b *nodeBook) pick(ctx context.Context, fallback string) string {
	b.mu.Lock()
	if len(b.seeds) == 0 || !sameAddr(b.seeds[0], fallback) {
		b.setSeeds(append([]string{fallback}, b.seeds[min(1, len(b.seeds)):]...))
	}
	cands := rankNodes(b.input())
	probe := b.probe
	b.mu.Unlock()

	if len(cands) == 0 {
		return fallback
	}
	if probe != nil && needsProbe(cands) {
		b.measure(ctx, cands, probe)
		b.mu.Lock()
		cands = rankNodes(b.input())
		b.mu.Unlock()
	}
	return cands[0].Addr
}

// measure, adaylari paralel olcer; basarisiz olcum dugumu basarisiz isaretler.
func (b *nodeBook) measure(ctx context.Context, cands []nodeCandidate, probe probeFunc) {
	var targets []string
	for _, c := range cands {
		if !c.Failed && c.RTT <= 0 && len(targets) < maxProbes {
			targets = append(targets, c.Addr)
		}
	}
	pctx, cancel := context.WithTimeout(ctx, probeTimeout+time.Second)
	defer cancel()
	type res struct {
		addr string
		d    time.Duration
		err  error
	}
	ch := make(chan res, len(targets))
	for _, a := range targets {
		go func(a string) {
			d, err := probe(pctx, a)
			ch <- res{a, d, err}
		}(a)
	}
	for range targets {
		r := <-ch
		b.mu.Lock()
		k := strings.ToLower(r.addr)
		if r.err != nil {
			b.failedAt[k] = b.now()
		} else {
			if r.d <= 0 {
				r.d = time.Nanosecond
			}
			b.rtt[k] = rttEntry{d: r.d, at: b.now()}
		}
		b.mu.Unlock()
	}
}

// markFail, adresin bu denemede baglanamadigini kaydeder.
func (b *nodeBook) markFail(addr string) {
	b.mu.Lock()
	b.failedAt[strings.ToLower(addr)] = b.now()
	b.mu.Unlock()
}

// prevNodeFor, addr'a baglanirken sunucuya bildirilecek onceki dugum kimligi
// (ayni dugume donuluyorsa bos).
func (b *nodeBook) prevNodeFor(addr string) string {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.lastNodeID == "" || b.lastAddr == "" || sameAddr(b.lastAddr, addr) {
		return ""
	}
	return b.lastNodeID
}

// connected, basarili el sikismadan sonra durumu gunceller ve onbellegi yazar.
func (b *nodeBook) connected(addr string, ack protocol.HelloAck) {
	b.mu.Lock()
	b.current = addr
	delete(b.failedAt, strings.ToLower(addr))
	if len(ack.Nodes) > 0 {
		var ns []protocol.NodeHint
		for _, n := range ack.Nodes {
			n.Address = protocol.NormalizeNodeAddress(n.Address)
			if n.Address != "" && n.NodeID != "" && len(ns) < maxCachedNodes {
				ns = append(ns, n)
			}
		}
		if len(ns) > 0 {
			b.nodes = ns
		}
	}
	self := ack.NodeID
	if self == "" {
		for _, n := range b.nodes {
			if sameAddr(n.Address, addr) {
				self = n.NodeID
			}
		}
	}
	if ack.AssignedNode != "" {
		b.assigned = ack.AssignedNode
	} else if self != "" && len(ack.Nodes) > 0 {
		b.assigned = self
	}
	if self != "" {
		b.lastNodeID = self
	}
	b.lastAddr = addr
	snap := nodeCacheFile{Seed: "", Nodes: b.nodes, Assigned: b.assigned,
		LastNodeID: b.lastNodeID, LastAddr: b.lastAddr, SavedAt: b.now()}
	if len(b.seeds) > 0 {
		snap.Seed = b.seeds[0]
	}
	path, log := b.cachePath, b.log
	b.mu.Unlock()
	if len(snap.Nodes) == 0 {
		return // liste yok (eski sunucu / tek dugum): yazilacak bir sey yok
	}
	if err := saveNodeCache(path, snap); err != nil && log != nil {
		log.Debug("dugum onbellegi yazilamadi", "hata", err)
	}
}

// snapshot, durum gosterimi icin dugum listesini ve mevcut dugumu doner.
func (b *nodeBook) snapshot() (current string, list []nodeCandidate) {
	b.mu.Lock()
	defer b.mu.Unlock()
	in := b.input()
	list = rankNodes(in)
	return b.current, list
}

// ---- Agent entegrasyonu -----------------------------------------------------

// book, dugum defterini tembel kurar; sunucu sabitlenmisse nil (liste yok).
func (a *Agent) book() *nodeBook {
	if a.ServerPinned {
		return nil
	}
	a.nodesOnce.Do(func() {
		seeds := append([]string{a.serverAddr()}, a.Servers...)
		a.nodes = newNodeBook(seeds, a.NodeCachePath, a.Log, a.probeNode)
	})
	return a.nodes
}

// pickAddr, bu baglanti denemesinin sunucu adresi.
func (a *Agent) pickAddr(ctx context.Context) string {
	base := a.serverAddr()
	b := a.book()
	if b == nil {
		return base
	}
	addr := b.pick(ctx, base)
	if addr == "" {
		return base
	}
	return addr
}

// probeNode, adrese TCP baglanip (Insecure degilse) TLS el sikismasi yapar ve
// toplam sureyi doner. Kimlik dogrulamasi (token) GONDERILMEZ.
func (a *Agent) probeNode(ctx context.Context, addr string) (time.Duration, error) {
	ctx, cancel := context.WithTimeout(ctx, probeTimeout)
	defer cancel()
	start := time.Now()
	d := net.Dialer{Timeout: probeTimeout}
	c, err := d.DialContext(ctx, "tcp", addr)
	if err != nil {
		return 0, err
	}
	defer c.Close()
	if a.Insecure {
		return time.Since(start), nil
	}
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		host = addr
	}
	cfg := &tls.Config{MinVersion: tls.VersionTLS12, ServerName: host, RootCAs: a.probeRoots}
	tc := tls.Client(c, cfg)
	if err := tc.HandshakeContext(ctx); err != nil {
		return 0, err
	}
	return time.Since(start), nil
}

// NodeView, durum gosterimi icin bir dugum satiri.
type NodeView struct {
	ID       string
	Address  string
	Region   string
	Priority int
	Healthy  bool
	Current  bool
	RTTMs    float64
}

// NodeStatus, bagli adresi, sabit mi bilgisini ve siralanmis dugum listesini
// doner. Sabit sunucuda ya da liste yokken liste bos olur.
func (a *Agent) NodeStatus() (current string, pinned bool, nodes []NodeView) {
	b := a.book()
	if b == nil {
		return "", true, nil
	}
	cur, list := b.snapshot()
	b.mu.Lock()
	hasList := len(b.nodes) > 0
	b.mu.Unlock()
	if !hasList {
		return cur, false, nil
	}
	for _, c := range list {
		nodes = append(nodes, NodeView{ID: c.NodeID, Address: c.Addr, Region: c.Region, Priority: c.Priority,
			Healthy: c.Healthy, Current: cur != "" && sameAddr(cur, c.Addr), RTTMs: float64(c.RTT) / float64(time.Millisecond)})
	}
	return cur, false, nodes
}
