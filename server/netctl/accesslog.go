package netctl

import (
	"context"
	"net/netip"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/tkodcumpeg4/zorven/shared/protocol"
)

// Z5: Mod B erisim gunlugu. Hedef Mod B dugumu gelen akislari ozetler
// (net_access_log); sunucu ust veriyi dogrular, kaynak/hedef adlarini ekler,
// hiz sinirini uygular ve AccessStore'a yazar. ICERIK YOKTUR.

const (
	// AccessRetention, erisim kayitlarinin saklama suresi.
	AccessRetention = 30 * 24 * time.Hour
	// accessMinInterval: bir dugumden iki net_access_log arasi en az sure
	// (daha sik gelenler SESSIZCE atilir).
	accessMinInterval = 10 * time.Second
	// accessDenyAudit: tek mesajdan denetim kaydina yazilan en fazla ret ozeti.
	accessDenyAudit = 5
	// accessTenantPerMin: kiraci basina dakikada kabul edilen en fazla kayit.
	accessTenantPerMin = 2000
)

// AccessRecord, kalici erisim kaydi (bir ozet satiri).
type AccessRecord struct {
	ID          int64     `json:"id,omitempty"`
	TenantID    string    `json:"-"`
	At          time.Time `json:"at"`
	SrcIP       string    `json:"src_ip"`
	SrcName     string    `json:"src_name,omitempty"`
	SrcClientID string    `json:"src_client_id,omitempty"`
	DstIP       string    `json:"dst_ip"`
	DstName     string    `json:"dst_name,omitempty"`
	DstClientID string    `json:"dst_client_id,omitempty"`
	Proto       string    `json:"proto"`
	Port        int       `json:"port,omitempty"`
	Decision    string    `json:"decision"`
	Rule        string    `json:"rule,omitempty"`
	Resource    string    `json:"resource,omitempty"`
	Flows       int       `json:"flows"`
	Packets     int64     `json:"packets"`
	Bytes       int64     `json:"bytes"`
}

// AccessQuery, erisim kaydi sorgusu. Bos alanlar filtre degildir.
type AccessQuery struct {
	Decision string
	Resource string
	// Node: kaynak veya hedef cihaz adi / kimligi (tam eslesme).
	Node  string
	Since time.Time
	Limit int
}

// AccessStore (opsiyonel; Store ayrica uygularsa): erisim kayitlari. Uygulanmazsa
// kayitlar atilir (yalniz denetim kaydi yazilir).
type AccessStore interface {
	AppendAccess(ctx context.Context, tenantID string, recs []AccessRecord) error
	ListAccess(ctx context.Context, tenantID string, q AccessQuery) ([]AccessRecord, error)
	PruneAccess(ctx context.Context, before time.Time) error
}

// accessLimiter: kiraci basina dakika penceresi sayaci.
type accessLimiter struct {
	mu     sync.Mutex
	window map[string]*accessWin
}

type accessWin struct {
	start time.Time
	n     int
}

// take, n kaydin kabul edilebilen kismini doner (0..n).
func (l *accessLimiter) take(tenantID string, n int, now time.Time) int {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.window == nil {
		l.window = map[string]*accessWin{}
	}
	w := l.window[tenantID]
	if w == nil || now.Sub(w.start) >= time.Minute {
		w = &accessWin{start: now}
		l.window[tenantID] = w
	}
	room := accessTenantPerMin - w.n
	if room <= 0 {
		return 0
	}
	if n > room {
		n = room
	}
	w.n += n
	return n
}

// handleAccessLog, hedef dugumun net_access_log mesajini isler.
func (c *NodeConn) handleAccessLog(ctx context.Context, data []byte) {
	m := c.m
	if !m.ALog || !c.id.ALog {
		return // pazarlanmadi: yok say
	}
	n := c.node.Load()
	if n == nil || n.rec.Kind != KindDevice {
		return
	}
	var msg protocol.NetAccessLog
	if err := jsonUnmarshal(data, &msg); err != nil {
		return
	}
	now := m.Now()
	m.mu.Lock()
	t := m.tenants[c.id.TenantID]
	if t == nil || n.ob != c.ob || !n.tun {
		m.mu.Unlock()
		return
	}
	if !n.lastALog.IsZero() && now.Sub(n.lastALog) < accessMinInterval {
		m.mu.Unlock()
		return
	}
	n.lastALog = now
	if len(msg.Entries) > protocol.MaxAccessLogEntries {
		msg.Entries = msg.Entries[:protocol.MaxAccessLogEntries]
	}
	ruleRes := map[string]string{}
	for _, r := range m.tunRulesLocked(t) {
		if r.Resource != "" && !r.DNSOnly {
			ruleRes[r.ID] = r.Resource
		}
	}
	recs := make([]AccessRecord, 0, len(msg.Entries))
	for _, e := range msg.Entries {
		src, err := netip.ParseAddr(e.SrcIP)
		if err != nil || !src.Is4() {
			continue
		}
		dec := e.Decision
		if dec != protocol.AccessAllow && dec != protocol.AccessDeny {
			continue
		}
		proto := strings.ToLower(e.Proto)
		switch proto {
		case "tcp", "udp", "icmp", "other":
		default:
			continue
		}
		if e.Port < 0 || e.Port > 65535 || e.Flows < 0 || e.Packets < 0 || e.Bytes < 0 {
			continue
		}
		r := AccessRecord{
			TenantID: t.id, At: now.UTC(), SrcIP: src.String(), DstIP: n.rec.IP.String(),
			DstName: n.rec.Name, DstClientID: n.rec.ClientID, Proto: proto, Port: e.Port, Decision: dec,
			Flows: e.Flows, Packets: e.Packets, Bytes: e.Bytes,
		}
		if sn := t.byIP[src]; sn != nil {
			r.SrcName, r.SrcClientID = sn.rec.Name, sn.rec.ClientID
		}
		if dec == protocol.AccessAllow && len(e.Rule) <= 128 {
			r.Rule = e.Rule
			r.Resource = ruleRes[e.Rule]
		}
		recs = append(recs, r)
	}
	tenantID := t.id
	m.mu.Unlock()
	if len(recs) == 0 {
		return
	}
	recs = recs[:m.alogLimiter.take(tenantID, len(recs), now)]
	if len(recs) == 0 {
		return
	}
	// Denetim: reddedilen akis ozetlerinden en agirlari (hepsi degil: gurultu).
	denies := make([]AccessRecord, 0, len(recs))
	for _, r := range recs {
		if r.Decision == protocol.AccessDeny {
			denies = append(denies, r)
		}
	}
	sort.Slice(denies, func(i, j int) bool { return denies[i].Packets > denies[j].Packets })
	for i, r := range denies {
		if i >= accessDenyAudit {
			break
		}
		who := r.SrcName
		if who == "" {
			who = r.SrcIP
		}
		m.audit(tenantID, "device:"+r.DstClientID, "net.access.deny", r.DstName,
			who+" "+r.Proto+":"+itoa(r.Port)+" paket="+itoa64(r.Packets))
	}
	if as, ok := m.st.(AccessStore); ok {
		sctx, cancel := context.WithTimeout(ctx, 5*time.Second)
		defer cancel()
		if err := as.AppendAccess(sctx, tenantID, recs); err != nil {
			m.log.Warn("erisim kaydi yazilamadi", "hata", err)
		}
	}
}

// AccessLog, kiracinin Mod B erisim kayitlari (en yeni once).
func (m *Manager) AccessLog(ctx context.Context, tenantID string, q AccessQuery) ([]AccessRecord, error) {
	as, ok := m.st.(AccessStore)
	if !ok {
		return nil, nil
	}
	if q.Limit <= 0 || q.Limit > 500 {
		q.Limit = 200
	}
	return as.ListAccess(ctx, tenantID, q)
}

func itoa(n int) string     { return strconv.Itoa(n) }
func itoa64(n int64) string { return strconv.FormatInt(n, 10) }
