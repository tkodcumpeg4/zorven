package netctl

import (
	"context"
	"net/netip"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/tkodcumpeg4/zorven/shared/protocol"
)

// W3 / Mod B (OS TUN) kontrol duzlemi.
//
// Karar (tasarim 5.5, R8): Mod B dugumu mevcut "device" kimligiyle girer (ajan
// WSS'i; cihaz token'i). ACL'de "hangi kullanici" bilgisi YOKTUR: kurallar
// cihazdan cihaza tanimlanir (kaynak = cihaz veya group:everyone, hedef = cihaz
// veya "*"). Kullanici/rol alicili paylasimlar `zorven access` oturum dugumleri
// (Mod A) icindir. Varsayilan: hicbir sey (default deny). Bir paylasimin
// (net_shares) sahibi Mod B'de ise paylasimin alicilari o porta TUN uzerinden de
// erisir (paylasim ACL'den turetilir). Hooks.TunRules ile ek kural eklenebilir.

const (
	// TunZone, sanal DNS bolgesi (rezerve).
	TunZone = "zorven.internal"

	ProtoAny  = "any"
	ProtoICMP = "icmp"

	// MaxTunRules, kiraci basina kural siniri.
	MaxTunRules = 500
)

// TunRule, bir Mod B izin kurali (cihazdan cihaza).
type TunRule struct {
	ID       string `json:"id"`
	TenantID string `json:"tenant_id"`
	Name     string `json:"name,omitempty"`
	// Src: izinli kaynak. Type device | group (yalniz "everyone").
	Src Grantee `json:"src"`
	// Dst: hedef cihaz kimligi (client id) veya "*" (Mod B'deki tum cihazlar).
	Dst string `json:"dst"`
	// Proto: tcp | udp | icmp | any. Ports: "" (hepsi) | "22" | "1000-2000".
	Proto     string    `json:"proto"`
	Ports     string    `json:"ports,omitempty"`
	CreatedBy string    `json:"created_by"`
	CreatedAt time.Time `json:"created_at"`

	// --- Z5: politika turevi kurallar (Hooks.TunRules; depoda SAKLANMAZ) ---
	// Origin dolu ise (or. "policy:pol_x") kaynak/hedef cihaz kimlikleri
	// SrcDevices/DstDevices ile belirlenir; Src/Dst yok sayilir.
	Origin     string   `json:"origin,omitempty"`
	SrcDevices []string `json:"src_devices,omitempty"`
	DstDevices []string `json:"dst_devices,omitempty"`
	// Resource: ozel kaynak adi (or. db.internal); bu kural o kaynaga aittir.
	// DNSOnly: ACL uretmez, yalniz Resource adinin kaynaklara cozulmesini saglar.
	Resource string `json:"resource,omitempty"`
	DNSOnly  bool   `json:"dns_only,omitempty"`
}

// TunStore (opsiyonel; Store ayrica uygularsa): Mod B kurallari ve kiraci agi.
// Uygulamayan depoda kurallar bellekte yasar ve havuz varsayilandir.
type TunStore interface {
	GetNetwork(ctx context.Context, tenantID string) (netip.Prefix, bool, error)
	PutNetwork(ctx context.Context, tenantID string, cidr netip.Prefix) error
	ListTunRules(ctx context.Context, tenantID string) ([]TunRule, error)
	PutTunRule(ctx context.Context, r TunRule) error
	DeleteTunRule(ctx context.Context, tenantID, id string) error
}

// SlugResolver (opsiyonel; Directory ayrica uygularsa): kuruluş kisa adi
// (DNS: <cihaz>.<slug>.zorven.internal).
type SlugResolver interface {
	TenantSlug(ctx context.Context, tenantID string) (string, error)
}

// TunRuleInput, kural olusturma girdisi.
type TunRuleInput struct {
	Name  string
	Src   Grantee
	Dst   string
	Proto string
	Ports string
}

// ParsePorts, "22" veya "1000-2000" -> lo, hi. Bos = (0,0,true) hepsi.
func ParsePorts(s string) (lo, hi uint16, ok bool) {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0, 0, true
	}
	a, b, isRange := strings.Cut(s, "-")
	l, err := strconv.Atoi(strings.TrimSpace(a))
	if err != nil || l < 1 || l > 65535 {
		return 0, 0, false
	}
	h := l
	if isRange {
		h, err = strconv.Atoi(strings.TrimSpace(b))
		if err != nil || h < l || h > 65535 {
			return 0, 0, false
		}
	}
	return uint16(l), uint16(h), true
}

func normProto(p string) string {
	p = strings.ToLower(strings.TrimSpace(p))
	if p == "" {
		return ProtoAny
	}
	return p
}

func validateTunRule(in TunRuleInput) (TunRuleInput, error) {
	in.Proto = normProto(in.Proto)
	switch in.Proto {
	case ProtoTCP, ProtoUDP, ProtoICMP, ProtoAny:
	default:
		return in, invalid("invalid_proto", "protokol tcp, udp, icmp veya any olmali")
	}
	in.Ports = strings.TrimSpace(in.Ports)
	if _, _, ok := ParsePorts(in.Ports); !ok {
		return in, invalid("invalid_ports", "port 1-65535 veya aralik (1000-2000) olmali")
	}
	if in.Ports != "" && (in.Proto == ProtoICMP) {
		return in, invalid("invalid_ports", "icmp icin port verilemez")
	}
	in.Dst = strings.TrimSpace(in.Dst)
	if in.Dst == "" {
		in.Dst = "*"
	}
	switch in.Src.Type {
	case GranteeDevice:
		if in.Src.ID == "" || len(in.Src.ID) > 128 {
			return in, invalid("invalid_grantee", "kaynak cihaz gecersiz")
		}
	case GranteeGroup:
		if in.Src.ID != "everyone" {
			return in, invalid("invalid_group", "Mod B kurallarinda kaynak grup yalniz everyone olabilir (cihaz kimligi ile calisir)")
		}
	default:
		return in, invalid("invalid_grantee", "kaynak turu device veya group:everyone olmali")
	}
	if len(in.Name) > 80 {
		in.Name = in.Name[:80]
	}
	return in, nil
}

// --- ag (havuz) -----------------------------------------------------------------

// ResolverIP, havuzun sanal DNS cozucu adresi (ilk /24 blogunun .1'i).
func ResolverIP(pool netip.Prefix) netip.Addr {
	return u2a(a2u(pool.Masked().Addr()) + 1)
}

// ValidateNetworkCIDR, kiraci ag araligini dogrular: IPv4, ozel/CGNAT bloklarindan
// biri icinde, /8 ile /24 arasi.
func ValidateNetworkCIDR(p netip.Prefix) error {
	if !p.IsValid() || !p.Addr().Is4() {
		return invalid("invalid_cidr", "gecerli bir IPv4 CIDR gerekli")
	}
	if p.Bits() < 10 || p.Bits() > 24 {
		return invalid("invalid_cidr", "on ek uzunlugu /10 ile /24 arasi olmali")
	}
	p = p.Masked()
	for _, blk := range []string{"100.64.0.0/10", "10.0.0.0/8", "172.16.0.0/12", "192.168.0.0/16"} {
		b := netip.MustParsePrefix(blk)
		if b.Contains(p.Addr()) && p.Bits() >= b.Bits() {
			return nil
		}
	}
	return invalid("invalid_cidr", "aralik 100.64.0.0/10, 10.0.0.0/8, 172.16.0.0/12 veya 192.168.0.0/16 icinde olmali")
}

// Network, kiracinin ic adres havuzu (CIDR).
func (m *Manager) Network(ctx context.Context, tenantID string) (netip.Prefix, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	t, err := m.tenantLocked(ctx, tenantID)
	if err != nil {
		return netip.Prefix{}, err
	}
	return t.alloc.pool, nil
}

// SetNetwork, kiracinin havuzunu degistirir. YALNIZ hic dugum yokken (IP'ler
// kalici; havuz degisince cihazlarin IP'si degisirdi). Mod B yerel agla cakisirsa
// yonetici bu uctan baska bir aralik secer.
func (m *Manager) SetNetwork(ctx context.Context, tenantID string, cidr netip.Prefix, actor string) error {
	if err := ValidateNetworkCIDR(cidr); err != nil {
		return err
	}
	cidr = cidr.Masked()
	ts, ok := m.st.(TunStore)
	if !ok {
		return invalid("unsupported", "bu depo kiraci agi ayarini desteklemiyor")
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	t, err := m.tenantLocked(ctx, tenantID)
	if err != nil {
		return err
	}
	if t.alloc.pool == cidr {
		return nil
	}
	if len(t.nodes) > 0 {
		return conflict("network_in_use", "ag araligi yalnizca hic dugum kayitli degilken degistirilebilir (once dugumleri kaldirin)")
	}
	if err := ts.PutNetwork(ctx, tenantID, cidr); err != nil {
		return err
	}
	t.alloc = newAlloc(cidr)
	m.audit(tenantID, actor, "net.network.set", cidr.String(), "")
	return nil
}

// --- kural CRUD -------------------------------------------------------------------

// TunRules, kiracinin Mod B kurallari.
func (m *Manager) TunRules(ctx context.Context, tenantID string) ([]TunRule, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	t, err := m.tenantLocked(ctx, tenantID)
	if err != nil {
		return nil, err
	}
	out := make([]TunRule, 0, len(t.rules))
	for _, r := range t.rules {
		out = append(out, *r)
	}
	sort.Slice(out, func(i, j int) bool {
		if !out[i].CreatedAt.Equal(out[j].CreatedAt) {
			return out[i].CreatedAt.Before(out[j].CreatedAt)
		}
		return out[i].ID < out[j].ID
	})
	return out, nil
}

// CreateTunRule, Mod B izin kurali ekler ve netmap'leri yeniler.
func (m *Manager) CreateTunRule(ctx context.Context, tenantID string, in TunRuleInput, actor string) (TunRule, error) {
	in, err := validateTunRule(in)
	if err != nil {
		return TunRule{}, err
	}
	if in.Src.Type == GranteeDevice && m.dir != nil {
		if _, ok, err := m.dir.DeviceName(ctx, tenantID, in.Src.ID); err != nil {
			return TunRule{}, err
		} else if !ok {
			return TunRule{}, invalid("grantee_not_found", "kaynak cihaz bulunamadi")
		}
	}
	if in.Dst != "*" && m.dir != nil {
		if _, ok, err := m.dir.DeviceName(ctx, tenantID, in.Dst); err != nil {
			return TunRule{}, err
		} else if !ok {
			return TunRule{}, invalid("device_not_found", "hedef cihaz bulunamadi")
		}
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	t, err := m.tenantLocked(ctx, tenantID)
	if err != nil {
		return TunRule{}, err
	}
	if len(t.rules) >= MaxTunRules {
		return TunRule{}, conflict("too_many_rules", "kural siniri doldu")
	}
	r := TunRule{
		ID: "tr_" + randHex(8), TenantID: tenantID, Name: in.Name, Src: in.Src, Dst: in.Dst,
		Proto: in.Proto, Ports: in.Ports, CreatedBy: actor, CreatedAt: m.Now().UTC(),
	}
	if ts, ok := m.st.(TunStore); ok {
		if err := ts.PutTunRule(ctx, r); err != nil {
			return TunRule{}, err
		}
	}
	t.rules[r.ID] = &r
	m.audit(tenantID, actor, "net.tunrule.create", r.ID, r.Src.Type+":"+r.Src.ID+" -> "+r.Dst+" "+r.Proto+" "+r.Ports)
	m.recomputeLocked(t)
	return r, nil
}

// DeleteTunRule, kurali kaldirir.
func (m *Manager) DeleteTunRule(ctx context.Context, tenantID, id, actor string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	t, err := m.tenantLocked(ctx, tenantID)
	if err != nil {
		return err
	}
	if _, ok := t.rules[id]; !ok {
		return notFound("rule_not_found", "kural bulunamadi")
	}
	if ts, ok := m.st.(TunStore); ok {
		if err := ts.DeleteTunRule(ctx, tenantID, id); err != nil {
			return err
		}
	}
	delete(t.rules, id)
	m.audit(tenantID, actor, "net.tunrule.delete", id, "")
	m.recomputeLocked(t)
	return nil
}

// --- netmap turetimi ---------------------------------------------------------------

func containsStr(l []string, v string) bool {
	for _, x := range l {
		if x == v {
			return true
		}
	}
	return false
}

// Politika turevi kurallar (Origin dolu) cihaz kimligi listeleriyle eslesir;
// elle kurallar (Origin bos) eski Src/Dst alanlariyla.
func (r *TunRule) dstMatches(y *node) bool {
	if r.Origin != "" {
		return containsStr(r.DstDevices, y.rec.ClientID)
	}
	return r.Dst == "*" || r.Dst == y.rec.ClientID
}

func (m *Manager) srcMatches(r *TunRule, y *node) bool {
	if r.Origin != "" {
		return y.rec.Kind == KindDevice && containsStr(r.SrcDevices, y.rec.ClientID)
	}
	return m.vis.Matches(r.Src, y.sub)
}

// tunRulesLocked: depodaki kurallar + ticari kanca kurallari (politika turevi).
func (m *Manager) tunRulesLocked(t *tenant) []TunRule {
	out := make([]TunRule, 0, len(t.rules))
	for _, r := range t.rules {
		out = append(out, *r)
	}
	if m.Hooks.TunRules != nil {
		var tunClients []string
		for _, n := range t.nodes {
			if n.tun && n.ob != nil && n.rec.Kind == KindDevice {
				tunClients = append(tunClients, n.rec.ClientID)
			}
		}
		sort.Strings(tunClients)
		for _, r := range m.Hooks.TunRules(t.id, tunClients) {
			if r.Origin == "" {
				continue // kanca kurallari kaynaklarini Origin ile isaretlemeli
			}
			r.TenantID = t.id
			out = append(out, r)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

// TunRuleView, etkin kural goruntusu (elle + politika turevi).
type TunRuleView struct {
	TunRule
	// SrcNodes / DstNodes: kuralin su an eslestigi Mod B cihaz adlari.
	SrcNodes []string `json:"src_nodes,omitempty"`
	DstNodes []string `json:"dst_nodes,omitempty"`
}

// EffectiveTunRules, kiracinin etkin Mod B kurallarini (elle + politika turevi)
// ve eslestikleri cevrimici Mod B cihazlariyla doner.
func (m *Manager) EffectiveTunRules(ctx context.Context, tenantID string) ([]TunRuleView, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	t, err := m.tenantLocked(ctx, tenantID)
	if err != nil {
		return nil, err
	}
	rules := m.tunRulesLocked(t)
	out := make([]TunRuleView, 0, len(rules))
	for i := range rules {
		r := &rules[i]
		v := TunRuleView{TunRule: *r}
		for _, y := range t.nodes {
			if !y.tun || y.ob == nil {
				continue
			}
			if m.srcMatches(r, y) {
				v.SrcNodes = append(v.SrcNodes, y.rec.Name)
			}
			if r.dstMatches(y) {
				v.DstNodes = append(v.DstNodes, y.rec.Name)
			}
		}
		sort.Strings(v.SrcNodes)
		sort.Strings(v.DstNodes)
		out = append(out, v)
	}
	return out, nil
}

// tunDesiredLocked: Mod B dugumu x icin ek es kumesi, ACL ve ozel kaynak DNS
// kayitlari. peers, paylasimlardan gelen mevcut esleri tasir; kural tabanli esler
// eklenir. out = x'in sundugu paylasimlar (alicilari Mod B ACL'ine turetilir).
func (m *Manager) tunDesiredLocked(t *tenant, rules []TunRule, x *node, peers map[string]protocol.NetPeer, out map[string]protocol.NetShareOut) ([]protocol.NetACLRule, []protocol.NetDNSRecord) {
	// Gelen kurallar (hedef x): kaynak ic IP'leri.
	var acl []protocol.NetACLRule
	records := map[string]string{}
	for i := range rules {
		r := &rules[i]
		in := r.dstMatches(x) && !r.DNSOnly
		for _, y := range t.nodes {
			if y == x || !y.tun || y.ob == nil {
				continue
			}
			if r.DNSOnly {
				// Yalniz ad cozumu: x kaynaksa ve y kaynagi yayinliyorsa kayit.
				if r.Resource != "" && r.dstMatches(y) && m.srcMatches(r, x) {
					records[r.Resource] = y.rec.IP.String()
				}
				continue
			}
			// y -> x izni: x'in ACL'ine y'nin IP'si girer; y esi x'te gorunur olmali.
			if in && m.srcMatches(r, y) {
				peers[y.rec.WGPub] = y.asPeer()
			}
			// x -> y izni: x, y'ye baglanabilsin diye y es olarak gorunur.
			if r.dstMatches(y) && m.srcMatches(r, x) {
				peers[y.rec.WGPub] = y.asPeer()
				if r.Resource != "" {
					records[r.Resource] = y.rec.IP.String()
				}
			}
		}
		if !in {
			continue
		}
		var src []string
		for _, y := range t.nodes {
			if y == x || !y.tun || y.ob == nil {
				continue
			}
			if m.srcMatches(r, y) {
				src = append(src, y.rec.IP.String())
			}
		}
		if len(src) == 0 {
			continue
		}
		sort.Strings(src)
		acl = append(acl, protocol.NetACLRule{ID: r.ID, Src: src, Proto: r.Proto, Ports: r.Ports})
	}
	// Paylasim ACL'i: sundugum paylasimin alicilari o porta erisebilir.
	for _, so := range out {
		if len(so.Allow) == 0 {
			continue
		}
		acl = append(acl, protocol.NetACLRule{
			ID: "share:" + so.ID, Src: append([]string(nil), so.Allow...), Proto: so.Proto, Ports: strconv.Itoa(so.Port),
		})
	}
	sort.SliceStable(acl, func(i, j int) bool { return acl[i].ID < acl[j].ID })
	var recs []protocol.NetDNSRecord
	for name, ip := range records {
		recs = append(recs, protocol.NetDNSRecord{Name: name, IP: ip})
	}
	sort.Slice(recs, func(i, j int) bool { return recs[i].Name < recs[j].Name })
	return acl, recs
}

// tunDNS: Mod B dugumu icin sanal DNS bilgisi.
func (m *Manager) tunDNS(t *tenant, suffix string, recs []protocol.NetDNSRecord) *protocol.NetDNS {
	return &protocol.NetDNS{Resolver: ResolverIP(t.alloc.pool).String(), Zone: TunZone, Suffix: suffix, Records: recs}
}

func (m *Manager) slug(ctx context.Context, tenantID string) string {
	if sr, ok := m.dir.(SlugResolver); ok && sr != nil {
		if s, err := sr.TenantSlug(ctx, tenantID); err == nil {
			return sanitizeName(s, "")
		}
	}
	return ""
}

// handleMode: net_mode (Mod B giris/cikis).
func (c *NodeConn) handleMode(ctx context.Context, mode protocol.NetMode) {
	n := c.node.Load()
	if n == nil || !c.id.Tun || n.rec.Kind != KindDevice {
		return
	}
	m := c.m
	m.mu.Lock()
	defer m.mu.Unlock()
	t := m.tenants[c.id.TenantID]
	if t == nil || n.ob != c.ob {
		return
	}
	if n.tun == mode.TUN {
		return
	}
	n.tun = mode.TUN
	if mode.TUN && t.slug == "" {
		// Kilit altinda DB'ye gitmemek icin bir kez yuklenir (Directory hafif).
		t.slug = m.slug(ctx, t.id)
	}
	m.audit(t.id, "device:"+n.rec.ClientID, "net.tun.mode", n.rec.ID, strconv.FormatBool(mode.TUN))
	m.recomputeLocked(t)
}
