package netctl

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"log/slog"
	"net/netip"
	"regexp"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/tkodcumpeg4/zorven/server/bandwidth"
	"github.com/tkodcumpeg4/zorven/shared/protocol"
)

const (
	maxSharesPerTenant = 500
	maxGrantees        = 50
	// SessionNodeTTL, `zorven access` dugumunun en uzun omru.
	SessionNodeTTL = 12 * time.Hour
)

var shareNameRe = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]{0,40}[a-z0-9])?$`)

// Manager, tum kiracilarin ag durumunu tutar. Bellek kaynak dogrudur;
// cihaz dugumleri ve paylasimlar Store'a yazilir (write-through).
type Manager struct {
	st    Store
	dir   Directory
	vis   Visibility
	log   *slog.Logger
	Hooks Hooks
	// MTU, wg arayuzu MTU'su (relay yolu icin 1380).
	MTU int
	// Pool, kiraci ic adres havuzu.
	Pool netip.Prefix
	// Now, testler icin saat.
	Now func() time.Time
	// Usage, relay trafiginin aylik kiraci kullanimina yazildigi ve hiz
	// sinirinin alindigi kaynak (nil = olcum/sinir yok). Baglanti kurulmadan once
	// atanmalidir.
	Usage bandwidth.UsageSource

	// W2 dogrudan yol: Punch false ise sunucu disco/aday koordinasyonu yapmaz
	// (yalniz relay). Stun/StunPort NetRegistered ile istemciye bildirilir.
	Punch    bool
	Stun     []string
	StunPort int
	// AllowLoopbackEndpoints: aday olarak loopback kabul edilir (yalniz testler).
	AllowLoopbackEndpoints bool

	// W3 Mod B (OS TUN): Tun false ise wg_tun pazarlanmaz (ZORVEN_NET_TUN=off).
	Tun bool
	// Z5: ALog false ise erisim gunlugu (wg_alog) pazarlanmaz.
	ALog        bool
	alogLimiter accessLimiter

	mu      sync.RWMutex
	tenants map[string]*tenant
	pubs    map[string]string // wg_pub -> tenantID (global benzersizlik)
}

type tenant struct {
	id       string
	alloc    *ipAlloc
	nodes    map[string]*node
	byIP     map[netip.Addr]*node
	byClient map[string]*node
	shares   map[string]*Share
	rules    map[string]*TunRule // W3: Mod B izin kurallari
	slug     string              // W3: DNS soneki (kurulus kisa adi), tembel
	epoch    int64
}

type node struct {
	rec     NodeRec
	sub     Subject
	ob      *Outbox
	expires time.Time

	vis atomic.Pointer[map[uint32]struct{}]

	sentFull bool
	sentPeer map[string]string
	sentIn   map[string]string
	sentOut  map[string]string
	sentACL  string // W3: son gonderilen ACL imzasi ("" = hic gonderilmedi)
	sentDNS  string

	// tun: dugum Mod B'de (OS TUN). Her kayitta (yeni oturum) false'tan baslar.
	tun bool
	// lastALog: son kabul edilen net_access_log (hiz siniri).
	lastALog time.Time

	relayed atomic.Int64
	denied  atomic.Int64

	// W2 (yalniz m.mu altinda): disco anahtari, UDP adaylari, esler icin
	// raporlanan yol durumu.
	discoPub   string
	punch      bool
	endpoints  []string
	epAt       time.Time
	paths      map[string]pathStat
	punchReqAt map[string]time.Time
}

// New, yoneticiyi kurar. vis nil ise OpenVisibility{dir} kullanilir.
func New(st Store, dir Directory, vis Visibility, log *slog.Logger) *Manager {
	if vis == nil {
		vis = OpenVisibility{Dir: dir}
	}
	if log == nil {
		log = slog.Default()
	}
	return &Manager{
		st: st, dir: dir, vis: vis, log: log,
		MTU: protocol.WGDefaultMTU, Pool: DefaultPool, Now: time.Now, Tun: true, ALog: true,
		tenants: map[string]*tenant{}, pubs: map[string]string{},
	}
}

func randHex(n int) string {
	b := make([]byte, n)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

func (m *Manager) audit(tenantID, actor, action, target, detail string) {
	if m.Hooks.Audit != nil {
		m.Hooks.Audit(tenantID, actor, action, target, detail)
	}
}

// tenantLocked: kiraci durumunu (gerekirse DB'den) yukler. m.mu yazma kilidi altinda.
func (m *Manager) tenantLocked(ctx context.Context, id string) (*tenant, error) {
	if t, ok := m.tenants[id]; ok {
		return t, nil
	}
	pool := m.Pool
	ts, hasTun := m.st.(TunStore)
	if hasTun {
		if p, ok, err := ts.GetNetwork(ctx, id); err != nil {
			return nil, err
		} else if ok && ValidateNetworkCIDR(p) == nil {
			pool = p
		}
	}
	t := &tenant{
		id: id, alloc: newAlloc(pool),
		nodes: map[string]*node{}, byIP: map[netip.Addr]*node{},
		byClient: map[string]*node{}, shares: map[string]*Share{}, rules: map[string]*TunRule{},
	}
	if hasTun {
		rs, err := ts.ListTunRules(ctx, id)
		if err != nil {
			return nil, err
		}
		for i := range rs {
			r := rs[i]
			t.rules[r.ID] = &r
		}
	}
	nodes, err := m.st.ListNodes(ctx, id)
	if err != nil {
		return nil, err
	}
	for _, r := range nodes {
		if r.Kind != KindDevice || !t.alloc.contains(r.IP) {
			continue
		}
		n := &node{rec: r, sub: Subject{Kind: KindDevice, ClientID: r.ClientID}}
		t.nodes[r.ID] = n
		t.byIP[r.IP] = n
		t.byClient[r.ClientID] = n
		m.pubs[r.WGPub] = id
	}
	shares, err := m.st.ListShares(ctx, id)
	if err != nil {
		return nil, err
	}
	for i := range shares {
		s := shares[i]
		t.shares[s.ID] = &s
	}
	m.tenants[id] = t
	if m.Hooks.Prepare != nil {
		m.Hooks.Prepare(ctx, id)
	}
	return t, nil
}

// gateOK, ek cihaz-baglami kapisi (Hooks.Gate); kanca yoksa her zaman true.
func (m *Manager) gateOK(tenantID string, s *Share, sub Subject) bool {
	return m.Hooks.Gate == nil || m.Hooks.Gate(tenantID, s.OwnerClientID, sub)
}

// Recompute (Z2), kiracinin netmap'ini yeniden hesaplar: politika/posture/zaman
// degisince erisimi kalkan dugumler eslerden cikarilir ve relay gorunurlugu kapanir.
func (m *Manager) Recompute(tenantID string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if t, ok := m.tenants[tenantID]; ok {
		m.recomputeLocked(t)
	}
}

// TenantIDs, bellekte yuklu kiracilar (periyodik yeniden degerlendirme icin).
func (m *Manager) TenantIDs() []string {
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := make([]string, 0, len(m.tenants))
	for id := range m.tenants {
		out = append(out, id)
	}
	return out
}

// --- kimlik / baglanti ---------------------------------------------------------

// Identity, tasiyicinin dogruladigi dugum kimligi.
type Identity struct {
	TenantID   string
	Kind       string
	ClientID   string // device
	ClientName string // device adi (DNS adi turetmek icin)
	UserID     string // session
	Role       string // session
	// Punch: tasiyici bu baglantida dogrudan yolu (wg_punch) pazarladi.
	Punch bool
	// Tun: tasiyici bu baglantida Mod B'yi (wg_tun) pazarladi.
	Tun bool
	// ALog: tasiyici bu baglantida erisim gunlugunu (wg_alog) pazarladi.
	ALog bool
}

// NodeConn, tek bir WSS baglantisinin dugum tarafi.
type NodeConn struct {
	m    *Manager
	id   Identity
	ob   *Outbox
	node atomic.Pointer[node]
	seq  atomic.Uint32
	// meter: bu baglantinin gonderdigi relay baytlari (lazy; Usage nil ise nil).
	meter     atomic.Pointer[bandwidth.Batcher]
	meterOnce sync.Once
}

func (c *NodeConn) usage() *bandwidth.Batcher {
	c.meterOnce.Do(func() {
		c.meter.Store(bandwidth.NewBatcher(c.m.Usage, c.id.TenantID, bandwidth.UsageTypeNetRelay))
	})
	return c.meter.Load()
}

// Attach, kimligi dogrulanmis bir baglanti icin NodeConn olusturur. Dugum,
// ilk net_register mesajiyla kaydedilir.
func (m *Manager) Attach(id Identity, ob *Outbox) *NodeConn {
	return &NodeConn{m: m, id: id, ob: ob}
}

func sanitizeName(s, fallback string) string {
	s = strings.ToLower(s)
	var b strings.Builder
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
		default:
			b.WriteRune('-')
		}
	}
	out := b.String()
	for strings.Contains(out, "--") {
		out = strings.ReplaceAll(out, "--", "-")
	}
	out = strings.Trim(out, "-")
	if len(out) > 40 {
		out = strings.Trim(out[:40], "-")
	}
	if out == "" {
		return fallback
	}
	return out
}

func (c *NodeConn) sendJSON(v any, t protocol.MessageType) {
	b, err := protocol.Marshal(v, t)
	if err != nil {
		return
	}
	c.ob.Ctrl(b)
}

func (c *NodeConn) sendErr(code, msg string) {
	c.sendJSON(protocol.NetError{Type: protocol.TypeNetError, Code: code, Message: msg}, protocol.TypeNetError)
}

// HandleControl, net_* kontrol mesajini isler.
func (c *NodeConn) HandleControl(ctx context.Context, t protocol.MessageType, data []byte) error {
	switch t {
	case protocol.TypeNetRegister:
		var reg protocol.NetRegister
		if err := jsonUnmarshal(data, &reg); err != nil {
			return err
		}
		if c.node.Load() != nil {
			return nil // ikinci kayit yok sayilir
		}
		if err := c.register(ctx, reg); err != nil {
			c.sendErr(ErrorCode(err), err.Error())
			return err
		}
	case protocol.TypeNetEndpoints, protocol.TypeNetPunchReq, protocol.TypeNetStatus:
		c.handlePunchControl(ctx, t, data)
	case protocol.TypeNetMode:
		var md protocol.NetMode
		if err := jsonUnmarshal(data, &md); err != nil {
			return err
		}
		c.handleMode(ctx, md)
	case protocol.TypeNetAccessLog:
		c.handleAccessLog(ctx, data)
	case protocol.TypeNetResync:
		n := c.node.Load()
		if n == nil {
			return nil
		}
		c.m.mu.Lock()
		n.sentFull = false
		if t := c.m.tenants[c.id.TenantID]; t != nil {
			c.m.recomputeLocked(t)
		}
		c.m.mu.Unlock()
	case protocol.TypeNetShareCreate:
		var req protocol.NetShareCreate
		if err := jsonUnmarshal(data, &req); err != nil {
			return err
		}
		c.deviceShareCreate(ctx, req)
	case protocol.TypeNetShareDelete:
		var req protocol.NetShareDelete
		if err := jsonUnmarshal(data, &req); err != nil {
			return err
		}
		c.deviceShareDelete(ctx, req)
	}
	return nil
}

func (c *NodeConn) register(ctx context.Context, reg protocol.NetRegister) error {
	if !validKey32(reg.WGPub) {
		return invalid("invalid_key", "wg acik anahtari 32 bayt kanonik base64 olmali")
	}
	pub := reg.WGPub
	m := c.m
	m.mu.Lock()
	defer m.mu.Unlock()
	t, err := m.tenantLocked(ctx, c.id.TenantID)
	if err != nil {
		return err
	}
	if owner, ok := m.pubs[pub]; ok {
		// Ayni anahtar yalniz ayni cihaza ait olabilir (global benzersiz).
		ex := t.byClient[c.id.ClientID]
		if owner != c.id.TenantID || c.id.Kind != KindDevice || ex == nil || ex.rec.WGPub != pub {
			return conflict("key_in_use", "bu wg anahtari baska bir dugumde kayitli")
		}
	}
	var n *node
	now := m.Now()
	switch c.id.Kind {
	case KindDevice:
		if c.id.ClientID == "" {
			return invalid("no_client", "cihaz kimligi yok")
		}
		n = t.byClient[c.id.ClientID]
		if n != nil {
			if n.ob != nil && n.ob != c.ob {
				n.ob.Close()
			}
			if n.rec.WGPub != pub {
				delete(m.pubs, n.rec.WGPub)
				n.rec.WGPub = pub
				m.pubs[pub] = t.id
			}
		} else {
			ip, ok := t.alloc.alloc(func(a netip.Addr) bool { _, used := t.byIP[a]; return used })
			if !ok {
				return conflict("pool_exhausted", "ic adres havuzu dolu")
			}
			n = &node{rec: NodeRec{
				ID: "nod_" + randHex(8), TenantID: t.id, Kind: KindDevice, ClientID: c.id.ClientID,
				Name: uniqueName(t, sanitizeName(c.id.ClientName, "device")), IP: ip, WGPub: pub, CreatedAt: now,
			}}
			t.nodes[n.rec.ID] = n
			t.byIP[ip] = n
			t.byClient[c.id.ClientID] = n
			m.pubs[pub] = t.id
		}
		n.rec.LastSeen = now
		n.sub = Subject{Kind: KindDevice, ClientID: c.id.ClientID}
		if err := m.st.PutNode(ctx, n.rec); err != nil {
			return err
		}
	case KindSession:
		ip, ok := t.alloc.alloc(func(a netip.Addr) bool { _, used := t.byIP[a]; return used })
		if !ok {
			return conflict("pool_exhausted", "ic adres havuzu dolu")
		}
		base := sanitizeName(reg.Name, "s")
		n = &node{rec: NodeRec{
			ID: "nod_" + randHex(8), TenantID: t.id, Kind: KindSession,
			Name: uniqueName(t, base+"-"+randHex(2)), IP: ip, WGPub: pub, CreatedAt: now, LastSeen: now,
		}}
		n.sub = Subject{Kind: KindSession, UserID: c.id.UserID, Role: c.id.Role}
		n.expires = now.Add(SessionNodeTTL)
		t.nodes[n.rec.ID] = n
		t.byIP[ip] = n
		m.pubs[pub] = t.id
	default:
		return invalid("bad_kind", "bilinmeyen dugum turu")
	}
	if m.Hooks.Enrich != nil {
		m.Hooks.Enrich(ctx, t.id, &n.sub)
	}
	n.ob = c.ob
	n.sentFull = false
	n.discoPub, n.punch, n.endpoints, n.paths, n.punchReqAt = "", false, nil, nil, nil
	n.tun, n.sentACL, n.sentDNS = false, "", ""
	n.lastALog = time.Time{}
	if m.Punch && c.id.Punch && validKey32(reg.DiscoPub) {
		n.discoPub, n.punch = reg.DiscoPub, true
	}
	c.node.Store(n)

	nr := protocol.NetRegistered{
		Type: protocol.TypeNetRegistered, NodeID: n.rec.ID, IP: n.rec.IP.String(),
		Name: n.rec.Name, MTU: m.MTU, Kind: n.rec.Kind,
	}
	if n.punch {
		nr.Punch, nr.Stun, nr.StunPort = true, m.Stun, m.StunPort
	}
	if m.Tun && c.id.Tun && n.rec.Kind == KindDevice {
		nr.Tun, nr.Pool = true, t.alloc.pool.String()
		nr.ALog = m.ALog && c.id.ALog
	}
	c.sendJSON(nr, protocol.TypeNetRegistered)
	m.recomputeLocked(t)
	return nil
}

func uniqueName(t *tenant, base string) string {
	taken := func(s string) bool {
		for _, n := range t.nodes {
			if n.rec.Name == s {
				return true
			}
		}
		return false
	}
	if !taken(base) {
		return base
	}
	for i := 2; ; i++ {
		cand := fmt.Sprintf("%s-%d", base, i)
		if !taken(cand) {
			return cand
		}
	}
}

// Close, baglanti bitince dugumu cevrimdisi yapar (oturum dugumu silinir,
// gecici paylasimlar kaldirilir) ve esleri gunceller.
func (c *NodeConn) Close() {
	c.ob.Close()
	c.usage().Flush() // biriken relay baytlari kapanista yazilir
	n := c.node.Load()
	if n == nil {
		return
	}
	m := c.m
	m.mu.Lock()
	defer m.mu.Unlock()
	t := m.tenants[c.id.TenantID]
	if t == nil || n.ob != c.ob {
		return // baska bir baglanti devraldi
	}
	n.ob = nil
	n.tun = false
	n.vis.Store(nil)
	n.endpoints, n.paths = nil, nil
	switch n.rec.Kind {
	case KindSession:
		m.removeNodeLocked(t, n, false)
	case KindDevice:
		ctx := context.Background()
		for id, s := range t.shares {
			if s.Ephemeral && s.OwnerClientID == n.rec.ClientID {
				_ = m.st.DeleteShare(ctx, t.id, id)
				delete(t.shares, id)
				m.audit(t.id, "device:"+n.rec.ClientID, "net.share.expire", id, "gecici paylasim oturumla birlikte kaldirildi")
			}
		}
	}
	m.recomputeLocked(t)
}

func (m *Manager) removeNodeLocked(t *tenant, n *node, persist bool) {
	delete(t.nodes, n.rec.ID)
	delete(t.byIP, n.rec.IP)
	if n.rec.Kind == KindDevice {
		delete(t.byClient, n.rec.ClientID)
	}
	delete(m.pubs, n.rec.WGPub)
	if persist {
		_ = m.st.DeleteNode(context.Background(), t.id, n.rec.ID)
	}
	if n.ob != nil {
		n.ob.Close()
	}
}

// Relay, sifreli WireGuard paketini hedef dugume iletir. YALNIZ ayni kiracida
// ve kaynagin netmap'inde gorunen hedefe; aksi halde sessizce dusurulur.
func (c *NodeConn) Relay(dstID uint64, payload []byte) {
	n := c.node.Load()
	if n == nil || len(payload) == 0 {
		return
	}
	dstIP := protocol.RelayIDToIP(dstID)
	vis := n.vis.Load()
	if vis == nil {
		n.denied.Add(1)
		return
	}
	if _, ok := (*vis)[uint32(dstID)]; !ok {
		n.denied.Add(1)
		return
	}
	m := c.m
	m.mu.RLock()
	t := m.tenants[c.id.TenantID]
	var ob *Outbox
	var dst *node
	if t != nil {
		if d := t.byIP[dstIP]; d != nil {
			dst, ob = d, d.ob
		}
	}
	m.mu.RUnlock()
	if dst == nil || ob == nil {
		return
	}
	// Kiraci bant siniri: kota asildiysa / limit doluysa paket dusurulur
	// (WireGuard ic TCP'si yeniden gonderir; kuyruk tikanmaz).
	meter := c.usage()
	if !bandwidth.AllowN(meter.Limiter(context.Background()), len(payload)) {
		n.denied.Add(1)
		return
	}
	frame := protocol.BodyFrame{
		FrameType: protocol.FrameWG, ReqID: protocol.IPToRelayID(n.rec.IP),
		Seq: c.seq.Add(1), Payload: payload,
	}.Encode()
	if ob.Data(frame) {
		n.relayed.Add(int64(len(payload)))
		meter.Add(0, int64(len(payload)))
	}
}

// --- netmap -------------------------------------------------------------------

func (n *node) asPeer() protocol.NetPeer {
	return protocol.NetPeer{
		NodeID: n.rec.ID, WGPub: n.rec.WGPub, IP: n.rec.IP.String(),
		Name: n.rec.Name, Kind: n.rec.Kind, Online: n.ob != nil, DiscoPub: n.discoPub, Tun: n.tun,
	}
}

func (s *Share) active(now time.Time) bool {
	return s.ExpiresAt == nil || now.Before(*s.ExpiresAt)
}

func (m *Manager) matches(s *Share, sub Subject) bool {
	for _, g := range s.Grantees {
		if m.vis.Matches(g, sub) {
			return true
		}
	}
	return false
}

// SubjectMatches, subject'in paylasimin alicisi olup olmadigi (API filtreleme).
func (m *Manager) SubjectMatches(s Share, sub Subject) bool { return m.matches(&s, sub) }

func sig(v any) string { b, _ := jsonMarshal(v); return string(b) }

// desiredLocked: X dugumunun gormesi gereken netmap parcalari.
func (m *Manager) desiredLocked(t *tenant, x *node, now time.Time) (peers map[string]protocol.NetPeer, in map[string]protocol.NetShareIn, out map[string]protocol.NetShareOut) {
	peers = map[string]protocol.NetPeer{}
	in = map[string]protocol.NetShareIn{}
	out = map[string]protocol.NetShareOut{}
	for _, s := range t.shares {
		if !s.active(now) {
			continue
		}
		owner := t.byClient[s.OwnerClientID]
		if owner == nil {
			continue
		}
		var exp int64
		if s.ExpiresAt != nil {
			exp = s.ExpiresAt.Unix()
		}
		if owner == x {
			so := protocol.NetShareOut{ID: s.ID, Name: s.Name, Port: s.Port, Proto: s.Proto, ExpiresUnix: exp}
			for _, y := range t.nodes {
				if y == x || !m.matches(s, y.sub) || !m.gateOK(t.id, s, y.sub) {
					continue
				}
				peers[y.rec.WGPub] = y.asPeer()
				so.Allow = append(so.Allow, y.rec.IP.String())
			}
			sort.Strings(so.Allow)
			out[s.ID] = so
		} else if m.matches(s, x.sub) && m.gateOK(t.id, s, x.sub) {
			peers[owner.rec.WGPub] = owner.asPeer()
			in[s.ID] = protocol.NetShareIn{
				ID: s.ID, Name: s.Name, OwnerName: owner.rec.Name, OwnerIP: owner.rec.IP.String(),
				Port: s.Port, Proto: s.Proto, ExpiresUnix: exp,
			}
		}
	}
	return
}

// recomputeLocked: her cevrimici dugum icin netmap'i yeniden hesaplar, fark varsa gonderir.
func (m *Manager) recomputeLocked(t *tenant) {
	now := m.Now()
	t.epoch++
	var tunRules []TunRule // Mod B kurallari: recompute basina bir kez turetilir
	for _, x := range t.nodes {
		if x.ob == nil {
			continue
		}
		peers, in, out := m.desiredLocked(t, x, now)
		var acl []protocol.NetACLRule
		var dnsRecs []protocol.NetDNSRecord
		if x.tun {
			if tunRules == nil {
				tunRules = m.tunRulesLocked(t)
			}
			acl, dnsRecs = m.tunDesiredLocked(t, tunRules, x, peers, out)
		}

		visSet := make(map[uint32]struct{}, len(peers))
		for _, p := range peers {
			if a, err := netip.ParseAddr(p.IP); err == nil {
				visSet[a2u(a)] = struct{}{}
			}
		}
		x.vis.Store(&visSet)

		nm := protocol.NetMap{Type: protocol.TypeNetMap, Epoch: t.epoch, Self: x.asPeer(), MTU: m.MTU}
		changed := false
		newPeer := map[string]string{}
		newIn := map[string]string{}
		newOut := map[string]string{}
		for k, p := range peers {
			s := sig(p)
			newPeer[k] = s
			if x.sentPeer[k] != s || !x.sentFull {
				nm.Peers = append(nm.Peers, p)
				changed = true
			}
		}
		for k := range x.sentPeer {
			if _, ok := peers[k]; !ok {
				nm.RemovedPeers = append(nm.RemovedPeers, k)
				changed = true
			}
		}
		for k, p := range in {
			s := sig(p)
			newIn[k] = s
			if x.sentIn[k] != s || !x.sentFull {
				nm.SharesIn = append(nm.SharesIn, p)
				changed = true
			}
		}
		for k := range x.sentIn {
			if _, ok := in[k]; !ok {
				nm.RemovedIn = append(nm.RemovedIn, k)
				changed = true
			}
		}
		for k, p := range out {
			s := sig(p)
			newOut[k] = s
			if x.sentOut[k] != s || !x.sentFull {
				nm.SharesOut = append(nm.SharesOut, p)
				changed = true
			}
		}
		for k := range x.sentOut {
			if _, ok := out[k]; !ok {
				nm.RemovedOut = append(nm.RemovedOut, k)
				changed = true
			}
		}
		// W3: Mod B dugumlerine ACL + DNS (Mod A'ya donuste bosaltilir).
		newACL, newDNS := "", ""
		if x.tun {
			newACL = sig(acl)
			newDNS = sig([]any{t.slug, dnsRecs})
		}
		if newACL != x.sentACL || !x.sentFull && newACL != "" {
			nm.ACLSet, nm.ACL = true, acl
			changed = true
		}
		if x.tun && (newDNS != x.sentDNS || !x.sentFull) {
			nm.DNS = m.tunDNS(t, t.slug, dnsRecs)
			changed = true
		}
		if !x.sentFull {
			nm.Full = true
			changed = true
		}
		if !changed {
			continue
		}
		b, err := protocol.Marshal(nm, protocol.TypeNetMap)
		if err != nil {
			continue
		}
		if x.ob.Ctrl(b) {
			x.sentFull = true
			x.sentPeer, x.sentIn, x.sentOut = newPeer, newIn, newOut
			x.sentACL, x.sentDNS = newACL, newDNS
		}
	}
}

// --- paylasim CRUD ------------------------------------------------------------

// ShareInput, paylasim olusturma girdisi.
type ShareInput struct {
	Name          string
	OwnerClientID string
	Port          int
	Proto         string
	Grantees      []Grantee
	ExpiresAt     *time.Time
	Ephemeral     bool
}

func (m *Manager) validateGrantees(ctx context.Context, tenantID string, gs []Grantee) ([]Grantee, error) {
	if len(gs) == 0 {
		return nil, invalid("no_grantees", "en az bir alici gerekli")
	}
	if len(gs) > maxGrantees {
		return nil, invalid("too_many_grantees", "en fazla 50 alici")
	}
	seen := map[Grantee]bool{}
	out := make([]Grantee, 0, len(gs))
	for _, g := range gs {
		g.Type, g.ID = strings.ToLower(strings.TrimSpace(g.Type)), strings.TrimSpace(g.ID)
		if rs, ok := m.dir.(GranteeResolver); ok && g.Type != GranteeGroup {
			rg, err := rs.ResolveGrantee(ctx, tenantID, g)
			if err != nil {
				return nil, err
			}
			g = rg
		}
		if seen[g] {
			continue
		}
		if err := m.vis.ValidateGrantee(ctx, tenantID, g); err != nil {
			return nil, err
		}
		seen[g] = true
		out = append(out, g)
	}
	return out, nil
}

// CreateShare, paylasim olusturur ve etkilenen dugumlere netmap iter.
func (m *Manager) CreateShare(ctx context.Context, tenantID string, in ShareInput, actor string) (Share, error) {
	in.Proto = strings.ToLower(strings.TrimSpace(in.Proto))
	if in.Proto == "" {
		in.Proto = ProtoTCP
	}
	if in.Proto != ProtoTCP && in.Proto != ProtoUDP {
		return Share{}, invalid("invalid_proto", "proto tcp veya udp olmali")
	}
	if in.Port < 1 || in.Port > 65535 {
		return Share{}, invalid("invalid_port", "port 1-65535 olmali")
	}
	if in.OwnerClientID == "" {
		return Share{}, invalid("no_device", "cihaz gerekli")
	}
	devName := "device"
	if m.dir != nil {
		nm, ok, err := m.dir.DeviceName(ctx, tenantID, in.OwnerClientID)
		if err != nil {
			return Share{}, err
		}
		if !ok {
			return Share{}, invalid("device_not_found", "cihaz bulunamadi")
		}
		devName = nm
	}
	gs, err := m.validateGrantees(ctx, tenantID, in.Grantees)
	if err != nil {
		return Share{}, err
	}
	name := strings.ToLower(strings.TrimSpace(in.Name))
	if name == "" {
		name = sanitizeName(fmt.Sprintf("%s-%d", sanitizeName(devName, "device"), in.Port), "share")
	}
	if !shareNameRe.MatchString(name) {
		return Share{}, invalid("invalid_name", "ad kucuk harf, rakam ve tireden olusmali (en fazla 42)")
	}
	if in.ExpiresAt != nil && !in.ExpiresAt.After(m.Now()) {
		return Share{}, invalid("invalid_expiry", "sona erme zamani gelecekte olmali")
	}

	m.mu.Lock()
	defer m.mu.Unlock()
	t, err := m.tenantLocked(ctx, tenantID)
	if err != nil {
		return Share{}, err
	}
	active := 0
	now := m.Now()
	for _, s := range t.shares {
		if !s.active(now) {
			continue
		}
		active++
		if s.OwnerClientID == in.OwnerClientID && s.Proto == in.Proto && s.Port == in.Port {
			return Share{}, conflict("port_shared", "bu port zaten paylasiliyor")
		}
	}
	for _, s := range t.shares {
		if s.active(now) && strings.EqualFold(s.Name, name) {
			return Share{}, conflict("name_taken", "bu adda bir paylasim zaten var")
		}
	}
	if active >= maxSharesPerTenant {
		return Share{}, conflict("too_many_shares", "paylasim siniri asildi")
	}
	if m.Hooks.AllowShare != nil {
		if err := m.Hooks.AllowShare(ctx, tenantID, active); err != nil {
			return Share{}, err
		}
	}
	s := Share{
		ID: "shr_" + randHex(8), TenantID: tenantID, OwnerClientID: in.OwnerClientID, Name: name,
		Proto: in.Proto, Port: in.Port, Grantees: gs, ExpiresAt: in.ExpiresAt,
		CreatedBy: actor, CreatedAt: now.UTC(), Ephemeral: in.Ephemeral,
	}
	if err := m.st.PutShare(ctx, s); err != nil {
		return Share{}, err
	}
	t.shares[s.ID] = &s
	m.audit(tenantID, actor, "net.share.create", s.ID, fmt.Sprintf("%s %s/%d device=%s", s.Name, s.Proto, s.Port, s.OwnerClientID))
	m.recomputeLocked(t)
	return s, nil
}

// SharePatch: nil alanlar degismez.
type SharePatch struct {
	Name      *string
	Grantees  *[]Grantee
	ExpiresAt **time.Time // nil: degisme; *nil: suresiz
}

func (m *Manager) UpdateShare(ctx context.Context, tenantID, id string, p SharePatch, actor string) (Share, error) {
	var gs []Grantee
	if p.Grantees != nil {
		var err error
		if gs, err = m.validateGrantees(ctx, tenantID, *p.Grantees); err != nil {
			return Share{}, err
		}
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	t, err := m.tenantLocked(ctx, tenantID)
	if err != nil {
		return Share{}, err
	}
	cur := t.shares[id]
	if cur == nil {
		return Share{}, notFound("share_not_found", "paylasim bulunamadi")
	}
	s := *cur
	if p.Name != nil {
		nm := strings.ToLower(strings.TrimSpace(*p.Name))
		if !shareNameRe.MatchString(nm) {
			return Share{}, invalid("invalid_name", "ad gecersiz")
		}
		for _, o := range t.shares {
			if o.ID != id && strings.EqualFold(o.Name, nm) {
				return Share{}, conflict("name_taken", "bu adda bir paylasim zaten var")
			}
		}
		s.Name = nm
	}
	if p.Grantees != nil {
		s.Grantees = gs
	}
	if p.ExpiresAt != nil {
		if e := *p.ExpiresAt; e != nil && !e.After(m.Now()) {
			return Share{}, invalid("invalid_expiry", "sona erme zamani gelecekte olmali")
		}
		s.ExpiresAt = *p.ExpiresAt
	}
	if err := m.st.PutShare(ctx, s); err != nil {
		return Share{}, err
	}
	t.shares[id] = &s
	m.audit(tenantID, actor, "net.share.update", id, s.Name)
	m.recomputeLocked(t)
	return s, nil
}

// DeleteShare, paylasimi iptal eder: es netmap'ten aninda duser.
func (m *Manager) DeleteShare(ctx context.Context, tenantID, id, actor string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	t, err := m.tenantLocked(ctx, tenantID)
	if err != nil {
		return err
	}
	s := t.shares[id]
	if s == nil {
		return notFound("share_not_found", "paylasim bulunamadi")
	}
	if err := m.st.DeleteShare(ctx, tenantID, id); err != nil {
		return err
	}
	delete(t.shares, id)
	m.audit(tenantID, actor, "net.share.revoke", id, s.Name)
	m.recomputeLocked(t)
	return nil
}

// GetShare, tek paylasim.
func (m *Manager) GetShare(ctx context.Context, tenantID, id string) (Share, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	t, err := m.tenantLocked(ctx, tenantID)
	if err != nil {
		return Share{}, err
	}
	s := t.shares[id]
	if s == nil {
		return Share{}, notFound("share_not_found", "paylasim bulunamadi")
	}
	return *s, nil
}

func (c *NodeConn) deviceShareCreate(ctx context.Context, req protocol.NetShareCreate) {
	res := protocol.NetShareResult{Type: protocol.TypeNetShareResult, ReqID: req.ReqID}
	fail := func(err error) {
		res.Code, res.Message = ErrorCode(err), err.Error()
		c.sendJSON(res, protocol.TypeNetShareResult)
	}
	if c.id.Kind != KindDevice || c.node.Load() == nil {
		fail(invalid("not_device", "yalniz kayitli cihazlar kendi portunu paylasabilir"))
		return
	}
	var exp *time.Time
	if req.ExpiresSec > 0 {
		e := c.m.Now().Add(time.Duration(req.ExpiresSec) * time.Second)
		exp = &e
	}
	gs := make([]Grantee, 0, len(req.To))
	for _, g := range req.To {
		gs = append(gs, Grantee{Type: g.Type, ID: g.ID})
	}
	s, err := c.m.CreateShare(ctx, c.id.TenantID, ShareInput{
		Name: req.Name, OwnerClientID: c.id.ClientID, Port: req.Port, Proto: req.Proto,
		Grantees: gs, ExpiresAt: exp, Ephemeral: req.Ephemeral,
	}, "device:"+c.id.ClientID)
	if err != nil {
		fail(err)
		return
	}
	res.OK, res.ShareID = true, s.ID
	c.sendJSON(res, protocol.TypeNetShareResult)
}

func (c *NodeConn) deviceShareDelete(ctx context.Context, req protocol.NetShareDelete) {
	res := protocol.NetShareResult{Type: protocol.TypeNetShareResult, ReqID: req.ReqID, ShareID: req.ShareID}
	s, err := c.m.GetShare(ctx, c.id.TenantID, req.ShareID)
	if err == nil && (c.id.Kind != KindDevice || s.OwnerClientID != c.id.ClientID) {
		err = notFound("share_not_found", "paylasim bulunamadi")
	}
	if err == nil {
		err = c.m.DeleteShare(ctx, c.id.TenantID, req.ShareID, "device:"+c.id.ClientID)
	}
	if err != nil {
		res.Code, res.Message = ErrorCode(err), err.Error()
	} else {
		res.OK = true
	}
	c.sendJSON(res, protocol.TypeNetShareResult)
}

// --- sorgular / bakim ----------------------------------------------------------

func (n *node) info() NodeInfo {
	return NodeInfo{
		ID: n.rec.ID, Kind: n.rec.Kind, ClientID: n.rec.ClientID, UserID: n.sub.UserID, Name: n.rec.Name,
		IP: n.rec.IP.String(), Online: n.ob != nil, LastSeen: n.rec.LastSeen, RelayedBytes: n.relayed.Load(), Tun: n.tun,
	}
}

// Nodes, kiracinin dugumleri.
func (m *Manager) Nodes(ctx context.Context, tenantID string) ([]NodeInfo, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	t, err := m.tenantLocked(ctx, tenantID)
	if err != nil {
		return nil, err
	}
	out := make([]NodeInfo, 0, len(t.nodes))
	for _, n := range t.nodes {
		out = append(out, n.info())
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

// RevokeNode, dugumu kaldirir (ES netmap'lerinden duser); cihaz yeniden
// baglanirsa yeni kayit alir.
func (m *Manager) RevokeNode(ctx context.Context, tenantID, nodeID, actor string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	t, err := m.tenantLocked(ctx, tenantID)
	if err != nil {
		return err
	}
	n := t.nodes[nodeID]
	if n == nil {
		return notFound("node_not_found", "dugum bulunamadi")
	}
	m.removeNodeLocked(t, n, true)
	m.audit(tenantID, actor, "net.node.revoke", nodeID, n.rec.Name)
	m.recomputeLocked(t)
	return nil
}

// Shares, kiracinin aktif paylasimlari (canli durumla).
func (m *Manager) Shares(ctx context.Context, tenantID string) ([]ShareInfo, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	t, err := m.tenantLocked(ctx, tenantID)
	if err != nil {
		return nil, err
	}
	now := m.Now()
	out := make([]ShareInfo, 0, len(t.shares))
	for _, s := range t.shares {
		if !s.active(now) {
			continue
		}
		si := ShareInfo{Share: *s, Connected: []NodeInfo{}}
		owner := t.byClient[s.OwnerClientID]
		if owner != nil {
			si.OwnerName, si.OwnerIP, si.OwnerOnline = owner.rec.Name, owner.rec.IP.String(), owner.ob != nil
		}
		for _, y := range t.nodes {
			if y != owner && y.ob != nil && m.matches(s, y.sub) {
				ni := y.info()
				if owner != nil && owner.ob != nil {
					ni.Path, ni.RTTMs = pathBetween(owner, y)
				}
				si.Connected = append(si.Connected, ni)
			}
		}
		si.Path = SummarizePaths(si.Connected)
		sort.Slice(si.Connected, func(i, j int) bool { return si.Connected[i].Name < si.Connected[j].Name })
		out = append(out, si)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].CreatedAt.After(out[j].CreatedAt) })
	return out, nil
}

// Sweep, suresi dolan paylasim/oturum dugumlerini kaldirir ve uye olmayan
// kullanicilarin oturum dugumlerini dusurur.
func (m *Manager) Sweep(ctx context.Context, checkRoles bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	now := m.Now()
	for _, t := range m.tenants {
		changed := false
		for id, s := range t.shares {
			if !s.active(now) {
				_ = m.st.DeleteShare(ctx, t.id, id)
				delete(t.shares, id)
				m.audit(t.id, "system", "net.share.expire", id, s.Name)
				changed = true
			}
		}
		for _, n := range t.nodes {
			if n.rec.Kind != KindSession {
				continue
			}
			drop := !n.expires.IsZero() && now.After(n.expires)
			if !drop && checkRoles && m.dir != nil && n.sub.UserID != "" {
				if role, err := m.dir.UserRole(ctx, t.id, n.sub.UserID); err == nil {
					if role == "" {
						drop = true
					} else if role != n.sub.Role {
						n.sub.Role = role
						changed = true
					}
				}
			}
			if drop {
				m.removeNodeLocked(t, n, false)
				changed = true
			}
		}
		if changed {
			m.recomputeLocked(t)
		}
	}
}

// Run, periyodik bakim dongusu.
func (m *Manager) Run(ctx context.Context) {
	tk := time.NewTicker(15 * time.Second)
	defer tk.Stop()
	last := time.Now()
	for {
		select {
		case <-ctx.Done():
			return
		case <-tk.C:
			roles := time.Since(last) > 5*time.Minute
			if roles {
				last = time.Now()
			}
			m.Sweep(ctx, roles)
			if roles {
				if as, ok := m.st.(AccessStore); ok {
					_ = as.PruneAccess(ctx, time.Now().Add(-AccessRetention))
				}
			}
		}
	}
}
