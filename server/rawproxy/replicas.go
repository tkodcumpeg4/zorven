package rawproxy

// Ham TCP/UDP tunellerinde replika kullanimi.
//
// Bir tunel birincil istemciye ek olarak replika istemcilerce de servis edilebilir
// (TunnelInfo.Replicas). HTTP/WebSocket ile ayni mantik:
//
//   - Aday kumesi = birincil + replikalar. Canli oturumu olan (ve, yuk dengeleme
//     kaydi varsa, saglikli olan) adaylar arasindan Balancer secer; strateji,
//     agirlik, saglik ve panik modu HTTP ile AYNI kodla (ingress.Router) uygulanir.
//   - TCP: HER yeni baglanti icin secim yapilir. Secilen oturumda akis acilamazsa
//     ya da ajan yerel hedefe baglanamazsa siradaki adaya gecilir (failover).
//   - UDP: secim yalnizca YENI flow icin yapilir; flow (kaynak adres:port) omru
//     boyunca ayni oturuma yapisiktir. Oturum gidince akis kapanir, flow silinir
//     ve sonraki paket yeniden eslenir.
//
// Tunel basina baglanti/IP sinirlari (TCP) ve PPS/flow sinirlari (UDP) secimden
// ONCE uygulanir; bu dosya onlari degistirmez. Replikasiz tunelde
// (TunnelInfo.Replicas bos) buradaki yollara HIC girilmez.

import (
	"context"
	"sync"
	"sync/atomic"
	"time"

	"github.com/tkodcumpeg4/zorven/server/tunnel"
	"github.com/tkodcumpeg4/zorven/shared/protocol"
)

// replicaAckTimeout, yedek aday kaldiysa ajanin akis onayi icin beklenecek sure.
// Son aday streamAckTimeout kadar bekler. Yari olu bir oturum (Hub henuz
// dusurmedi ama yanit vermiyor) yuzunden ziyaretci 15 sn beklemeden siradaki
// adaya gecilsin diye daha kisadir; saglik denetimi zaman asimi varsayilani
// (3 sn) ile ayni mertebededir.
var replicaAckTimeout = 5 * time.Second

// Balancer, replikali ham tunellerde aday istemcileri secim sirasina dizer.
// ingress.Router uygular: HTTP yuk dengelemeyle ayni algoritma ve saglik tablosu.
// nil ise cevrimici adaylar arasinda sirali (round-robin) dagitim yapilir.
type Balancer interface {
	// OrderClients, adaylari (birincil + replikalar) secim sirasina dizer: ilk
	// eleman secilen istemci, kalanlar failover sirasidir. online(c) false donen
	// aday elenir; hic aday cevrimici degilse nil doner. start, tunel basina artan
	// round-robin sayacidir (cagiran tutar).
	OrderClients(tunnelID string, candidates []string, start uint64, online func(clientID string) bool) []string
	// ClientBegin / ClientEnd, istemciye yonlendirilen acik baglanti/flow sayacini
	// tutar (least_connections; HTTP isteklerinin sayaciyla ortaktir).
	ClientBegin(clientID string)
	ClientEnd(clientID string)
}

// clientStreams, aday istemcilerin oturumlarina erisim yuzeyidir. Uretimde Hub
// uygular; testler sahte oturum verir.
type clientStreams interface {
	// online, istemcinin canli oturumu var mi.
	online(clientID string) bool
	// open, istemcinin oturumundan ham akis acar. offline=true: canli oturum yok.
	open(ctx context.Context, ti TunnelInfo, clientID, proto, remote string) (st bridgeStream, offline bool, err error)
}

// hubStreams, clientStreams'in Hub uzerindeki uretim uygulamasidir.
type hubStreams struct{ hub *tunnel.Hub }

func (h hubStreams) online(clientID string) bool {
	if h.hub == nil {
		return false
	}
	_, ok := h.hub.Get(clientID)
	return ok
}

func (h hubStreams) open(ctx context.Context, ti TunnelInfo, clientID, proto, remote string) (bridgeStream, bool, error) {
	if h.hub == nil {
		return nil, true, nil
	}
	sess, ok := h.hub.Get(clientID)
	if !ok {
		return nil, true, nil
	}
	rs, err := sess.OpenStream(ctx, ti.TunnelID, proto, remote)
	if err != nil {
		return nil, false, err
	}
	return rs, false, nil
}

// clients, aday oturumlarina erisim yuzeyini doner (testlerde m.streams).
func (m *Manager) clients() clientStreams {
	if m.streams != nil {
		return m.streams
	}
	if m.Remote != nil {
		return remoteStreams{local: hubStreams{hub: m.Hub}, remote: m.Remote}
	}
	return hubStreams{hub: m.Hub}
}

// normalized, Replicas'tan birincil istemciyi, bos kimlikleri ve tekrarlari ayiklar;
// kalan yoksa nil yapar. Boylece (ornegin birincil sonradan bir replika istemciye
// tasinmis) fiilen tek istemcili tunel replika yoluna girmez, hizli yolda kalir.
func (ti TunnelInfo) normalized() TunnelInfo {
	if len(ti.Replicas) == 0 {
		return ti
	}
	var out []string
	for _, c := range ti.Replicas {
		if c == "" || c == ti.ClientID || containsID(out, c) {
			continue
		}
		out = append(out, c)
	}
	ti.Replicas = out
	return ti
}

// candidates, tunelin aday istemcileri: birincil + replikalar (tekrarlar ve bos
// kimlikler ayiklanmis, birincil basta).
func (ti TunnelInfo) candidates() []string {
	out := make([]string, 0, 1+len(ti.Replicas))
	add := func(c string) {
		if c == "" {
			return
		}
		for _, x := range out {
			if x == c {
				return
			}
		}
		out = append(out, c)
	}
	add(ti.ClientID)
	for _, c := range ti.Replicas {
		add(c)
	}
	return out
}

// sameCandidates, iki tunel bilgisinin aday kumesi (birincil + replikalar, sirali) ayni mi.
func sameCandidates(a, b TunnelInfo) bool {
	if a.ClientID != b.ClientID || len(a.Replicas) != len(b.Replicas) {
		return false
	}
	for i := range a.Replicas {
		if a.Replicas[i] != b.Replicas[i] {
			return false
		}
	}
	return true
}

func containsID(ids []string, id string) bool {
	for _, x := range ids {
		if x == id {
			return true
		}
	}
	return false
}

// rotateOnline, Balancer yokken adaylari start ofsetinden itibaren dondurup
// yalnizca cevrimici olanlari sirayla doner (ingress.orderOnline ile ayni).
func rotateOnline(cands []string, start uint64, online func(string) bool) []string {
	n := len(cands)
	if n == 0 {
		return nil
	}
	var out []string
	for i := 0; i < n; i++ {
		c := cands[(int(start%uint64(n))+i)%n]
		if online(c) {
			out = append(out, c)
		}
	}
	return out
}

// nextRR, tunel icin bir sonraki round-robin sayacini doner (ilk cagri 0).
func (m *Manager) nextRR(tunnelID string) uint64 {
	v, ok := m.rr.Load(tunnelID)
	if !ok {
		v, _ = m.rr.LoadOrStore(tunnelID, new(atomic.Uint64))
	}
	return v.(*atomic.Uint64).Add(1) - 1
}

// pickOrder, replikali tunelin adaylarini bu baglanti/flow icin secim sirasina
// dizer: ilk eleman secilen, kalanlar failover sirasi. Hic aday cevrimici
// degilse nil.
func (m *Manager) pickOrder(ti TunnelInfo, cs clientStreams) []string {
	cands := ti.candidates()
	start := m.nextRR(ti.TunnelID)
	if m.Balancer != nil {
		return m.Balancer.OrderClients(ti.TunnelID, cands, start, cs.online)
	}
	return rotateOnline(cands, start, cs.online)
}

func (m *Manager) clientBegin(clientID string) {
	if m.Balancer != nil {
		m.Balancer.ClientBegin(clientID)
	}
}

func (m *Manager) clientEnd(clientID string) {
	if m.Balancer != nil {
		m.Balancer.ClientEnd(clientID)
	}
}

// logDebug / logWarn, Log nil olabilen (testler) yoneticiler icin guvenli loglama.
func (m *Manager) logDebug(msg string, args ...any) {
	if m.Log != nil {
		m.Log.Debug(msg, args...)
	}
}

func (m *Manager) logWarn(msg string, args ...any) {
	if m.Log != nil {
		m.Log.Warn(msg, args...)
	}
}

// ackResult, ajanin akis onayi beklemesinin sonucu.
type ackResult int

const (
	ackOK       ackResult = iota // ajan yerel hedefe baglandi
	ackRejected                  // ajan baglanamadi ya da akis/oturum kapandi
	ackTimeout                   // sure doldu
	ackCanceled                  // ziyaretci vazgecti / baglam iptal
)

// awaitAck, ajanin akis onayini bekler. Donen StreamAck yalniz ackRejected'ta
// anlamlidir (hata kodu).
func awaitAck(ctx context.Context, st bridgeStream, timeout time.Duration) (ackResult, protocol.StreamAck) {
	t := time.NewTimer(timeout)
	defer t.Stop()
	select {
	case ack := <-st.Accept():
		if ack.Code != "" {
			return ackRejected, ack
		}
		return ackOK, ack
	case <-st.Closed(): // oturum dustu ya da ajan akisi kapatti
		// Onay ve kapanis birlikte hazir olabilir (ajan baglandi, yerel hedef hemen
		// kapatti): replikasiz yoldaki gibi onay ONCE islenir, rastgele secilmez.
		select {
		case ack := <-st.Accept():
			if ack.Code != "" {
				return ackRejected, ack
			}
			return ackOK, ack
		default:
		}
		return ackRejected, protocol.StreamAck{}
	case <-t.C:
		return ackTimeout, protocol.StreamAck{}
	case <-ctx.Done():
		return ackCanceled, protocol.StreamAck{}
	}
}

// openReplicaTCP, replikali tunelde ham TCP akisini siralanmis adaylarda dener
// (order: pickOrder sonucu). Ilk aday acilamaz ya da ajan yerel hedefe
// baglanamazsa siradakine gecilir. Basarida onayi alinmis akisi ve acik-baglanti
// sayacini birakan fonksiyonu doner. stream == nil ise basarisizdir: rej bos
// degilse reddedilen sayilmalidir (bos = ziyaretci vazgecti).
//
// Ziyaretci baytlari bu fonksiyon donene kadar okunmaz; yeniden deneme veri
// kaybettirmez. Basarisiz denemeler reddedilen baglanti sayilmaz.
func (m *Manager) openReplicaTCP(ctx context.Context, ti TunnelInfo, order []string, remote string) (stream bridgeStream, release func(), rej string) {
	cs := m.clients()
	offline, failed := 0, 0
	for i, cid := range order {
		if ctx.Err() != nil {
			return nil, nil, ""
		}
		st, off, err := cs.open(ctx, ti, cid, protocol.ProtoTCP, remote)
		if off {
			offline++
			continue
		}
		if err != nil {
			failed++
			m.logWarn("rawproxy: replikada akis acilamadi, siradaki deneniyor", "tunnel", ti.TunnelID, "client", cid, "hata", err)
			continue
		}
		m.clientBegin(cid)
		timeout := replicaAckTimeout
		if i == len(order)-1 {
			timeout = streamAckTimeout
		}
		res, ack := awaitAck(ctx, st, timeout)
		switch res {
		case ackOK:
			m.logDebug("rawproxy: replika secildi", "tunnel", ti.TunnelID, "client", cid, "sira", i)
			return st, func() { m.clientEnd(cid) }, ""
		case ackCanceled:
			st.Close("canceled")
			m.clientEnd(cid)
			return nil, nil, ""
		}
		st.Close("failover")
		m.clientEnd(cid)
		failed++
		m.logDebug("rawproxy: replika yerel hedefe baglanamadi, siradaki deneniyor",
			"tunnel", ti.TunnelID, "client", cid, "code", ack.Code, "msg", ack.Message, "zaman_asimi", res == ackTimeout)
	}
	if failed == 0 {
		return nil, nil, TCPRejectOffline
	}
	return nil, nil, TCPRejectDial
}

// openReplicaUDP, yeni UDP flow'u icin adaylari siraya dizip ilk acilan akisi ve
// secilen istemciyi doner (acik-flow sayaci artirilmistir; release birakir).
// Hicbiri acilamazsa stream nil. UDP'de onay beklenmez (pumpFromLocal bekler):
// onay hatasi flow'u siler, sonraki paket yeniden esler.
func (m *Manager) openReplicaUDP(ctx context.Context, ti TunnelInfo, key string) (stream bridgeStream, clientID string, release func()) {
	cs := m.clients()
	for _, cid := range m.pickOrder(ti, cs) {
		st, off, err := cs.open(ctx, ti, cid, protocol.ProtoUDP, key)
		if off {
			continue
		}
		if err != nil {
			m.logWarn("rawproxy: replikada udp akisi acilamadi, siradaki deneniyor", "tunnel", ti.TunnelID, "client", cid, "hata", err)
			continue
		}
		m.clientBegin(cid)
		return st, cid, sync.OnceFunc(func() { m.clientEnd(cid) })
	}
	return nil, "", nil
}
