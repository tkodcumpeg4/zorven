package protocol

import (
	"encoding/binary"
	"net/netip"
)

// Zorven Net (WireGuard, Mod A): istemciler arasi uctan uca sifreli port
// paylasimi. Sunucu kontrol duzlemidir (anahtar/IP/netmap) ve sifreli WireGuard
// paketlerini FrameWG ile iletir; icerigi goremez.

const (
	// FeatureWGNet: net_* kontrol mesajlari ve netmap.
	FeatureWGNet = "wg_net"
	// FeatureWGRelay: FrameWG ikili cerceveleri (sunucu uzerinden relay).
	FeatureWGRelay = "wg_relay"
	// FeatureWGPunch (W2): dogrudan P2P UDP yolu (STUN adaylari, net_punch,
	// disco yoklamasi). Yalniz wg_net + wg_relay ile birlikte anlamlidir; sunucu
	// etkinlestirirse HelloAck.Features'ta yankilar. Etkin degilse trafik
	// yalnizca relay uzerinden akar (W1 davranisi).
	FeatureWGPunch = "wg_punch"
	// FeatureWGTun (W3): Mod B (OS TUN). net_mode mesaji, netmap'te ACL kurallari
	// ve DNS bilgisi. Yalniz wg_net + wg_relay ile birlikte anlamlidir. Eski
	// istemci/sunucu: ozellik pazarlanmaz, Mod B kapali kalir (yalniz Mod A).
	FeatureWGTun = "wg_tun"
	// FeatureWGALog (Z5): Mod B erisim gunlugu. Istemci, gelen akislarin UST VERISINI
	// (kaynak IP, proto, port, karar, paket/bayt sayisi; ICERIK YOK) ozetleyip
	// net_access_log olarak gonderir. Sunucu etkinlestirirse NetRegistered.ALog=true.
	FeatureWGALog = "wg_alog"
)

// FrameWG, sifreli bir WireGuard UDP paketini tasir. Istemci->sunucu yonunde
// ReqID = HEDEF dugumun ic IPv4 adresi (uint32), sunucu->istemci yonunde
// ReqID = KAYNAK dugumun ic IPv4 adresi. Seq yalniz teshis icindir. Akis
// kontrolu yoktur: kuyruk dolunca paket dusurulur (datagram semantigi).
const FrameWG uint8 = 8

// WGDefaultMTU, relay yolunda wg arayuzu MTU'su (w0 PoC onerisi).
const WGDefaultMTU = 1380

// Net mesaj tipleri.
const (
	TypeNetRegister    MessageType = "net_register"     // c->s
	TypeNetRegistered  MessageType = "net_registered"   // s->c
	TypeNetMap         MessageType = "net_map"          // s->c (tam veya delta)
	TypeNetResync      MessageType = "net_resync"       // c->s
	TypeNetShareCreate MessageType = "net_share_create" // c->s (cihaz kendi portunu paylasir)
	TypeNetShareDelete MessageType = "net_share_delete" // c->s
	TypeNetShareResult MessageType = "net_share_result" // s->c
	TypeNetError       MessageType = "net_error"        // s->c

	// W2 (FeatureWGPunch / NetRegistered.Punch).
	TypeNetEndpoints MessageType = "net_endpoints" // c->s: bu dugumun UDP aday adresleri
	TypeNetPunch     MessageType = "net_punch"     // s->c: es adaylari + "simdi yokla" koordinasyonu
	TypeNetPunchReq  MessageType = "net_punch_req" // c->s: bir esle dogrudan yol kurulmasini iste
	TypeNetStatus    MessageType = "net_status"    // c->s: es basina yol durumu (panel/CLI gorunurlugu)

	// W3 (FeatureWGTun).
	TypeNetMode MessageType = "net_mode" // c->s: dugum OS TUN (Mod B) moduna gecti/cikti

	// Z5 (FeatureWGALog).
	TypeNetAccessLog MessageType = "net_access_log" // c->s: Mod B gelen akis ozeti
)

// Yol durumlari (NetPathStatus.Path).
const (
	PathDirect = "direct"
	PathRelay  = "relay"
)

// MaxNetEndpoints, bir dugumun bildirebilecegi/aldigi en fazla aday sayisi.
const MaxNetEndpoints = 16

// NetEndpoints (c->s): dugumun UDP aday adresleri ("ip:port"). Yerel arayuz
// adresleri + STUN yansima adresi. Sunucu yalniz ACL ile yetkili esler icin
// dagitir (net_punch).
type NetEndpoints struct {
	Type      MessageType `json:"type"`
	Endpoints []string    `json:"endpoints"`
}

// NetPunch (s->c): "peer ile simdi eszamanli yokla". Hem yerel dugume hem
// ese ayni anda gonderilir (delik acma). Endpoints = ESIN adaylari.
type NetPunch struct {
	Type         MessageType `json:"type"`
	PeerWGPub    string      `json:"peer_wg_pub"`
	PeerDiscoPub string      `json:"peer_disco_pub"`
	Endpoints    []string    `json:"endpoints"`
	Nonce        string      `json:"nonce,omitempty"`
}

// NetPunchReq (c->s): bir esle dogrudan yol kurulmasini iste.
type NetPunchReq struct {
	Type      MessageType `json:"type"`
	PeerWGPub string      `json:"peer_wg_pub"`
}

// NetMode (c->s): dugum Mod B'ye (OS TUN) gecti (TUN=true) veya Mod A'ya dondu.
// Sunucu bunu gorunce dugume ACL kurallari, DNS bilgisi ve kural tabanli
// esleri gonderir; TUN=false ise bunlari geri ceker. Yalniz wg_tun pazarlandiysa
// gecerlidir.
type NetMode struct {
	Type MessageType `json:"type"`
	TUN  bool        `json:"tun"`
}

// NetACLRule (s->c, yalniz Mod B dugumlerine): bu dugume GELEN trafik icin izin
// kurali. Src = izinli kaynak ic IP'leri ("100.64.0.7" veya CIDR). Proto:
// tcp | udp | icmp | any. Ports: "" (hepsi), "22" veya "1000-2000" (tcp/udp icin).
// Hicbir kurala uymayan gelen paket dusurulur; yerel olarak baslatilan akislarin
// donusu baglanti takibiyle ayrica kabul edilir.
type NetACLRule struct {
	ID    string   `json:"id,omitempty"`
	Src   []string `json:"src"`
	Proto string   `json:"proto"`
	Ports string   `json:"ports,omitempty"`
}

// NetDNS (s->c, yalniz Mod B): sanal DNS. Resolver = sanal cozucu IP'si (havuzun
// .1 adresi); Zone = "zorven.internal"; Suffix = kurulus kisa adi (bos olabilir).
// Adlar netmap'teki esler ve kendisinden istemcide uretilir:
// <ad>.zorven.internal ve (Suffix varsa) <ad>.<suffix>.zorven.internal.
type NetDNS struct {
	Resolver string `json:"resolver"`
	Zone     string `json:"zone"`
	Suffix   string `json:"suffix,omitempty"`
	// Records (Z5): ozel kaynak adlari. Name = ozel kaynak adi (or. "db.internal"),
	// IP = kaynagi yayinlayan cihazin sanal IP'si. Istemci bunlari
	// <name>.<zone> ve (Suffix varsa) <name>.<suffix>.<zone> olarak cevaplar.
	// Yalniz bu dugumun ERISEBILDIGI kaynaklar gonderilir.
	Records []NetDNSRecord `json:"records,omitempty"`
}

// NetDNSRecord, tek bir ozel kaynak adi.
type NetDNSRecord struct {
	Name string `json:"name"`
	IP   string `json:"ip"`
}

// NetAccessLog (c->s, Z5): Mod B hedef dugumunun gelen akis ozeti. Her ~30 sn'de
// bir, yalniz degisiklik varsa gonderilir; en fazla MaxAccessLogEntries girdi.
// Gizlilik: paket ICERIGI yoktur; yalniz (kaynak, proto, port, karar) ve sayaclar.
type NetAccessLog struct {
	Type      MessageType      `json:"type"`
	WindowSec int              `json:"window_sec"`
	Entries   []NetAccessEntry `json:"entries"`
	// Truncated: pencere icinde sinira takildigi icin atlanan farkli akis anahtari sayisi.
	Truncated int `json:"truncated,omitempty"`
}

// MaxAccessLogEntries, tek net_access_log mesajindaki en fazla girdi.
const MaxAccessLogEntries = 200

// Erisim karari degerleri.
const (
	AccessAllow = "allow"
	AccessDeny  = "deny"
)

// NetAccessEntry: tek bir (kaynak IP, proto, port, karar) ozeti.
type NetAccessEntry struct {
	SrcIP    string `json:"src_ip"`
	Proto    string `json:"proto"` // tcp | udp | icmp | other
	Port     int    `json:"port,omitempty"`
	Decision string `json:"decision"` // allow | deny
	// Rule: izin veren ACL kuralinin kimligi (allow ise).
	Rule    string `json:"rule,omitempty"`
	Flows   int    `json:"flows"`   // yeni akis (TCP SYN / ilk UDP-ICMP paketi) sayisi
	Packets int64  `json:"packets"` // paket sayisi
	Bytes   int64  `json:"bytes"`   // IP paket bayti (gelen yon)
}

// NetPathStatus: bir esle secili yol.
type NetPathStatus struct {
	PeerWGPub string `json:"peer_wg_pub"`
	Path      string `json:"path"` // direct | relay
	RTTMs     int    `json:"rtt_ms,omitempty"`
}

// NetStatus (c->s): yol durumu. Her gonderim TAM listedir.
type NetStatus struct {
	Type  MessageType     `json:"type"`
	Paths []NetPathStatus `json:"paths"`
}

// IPToRelayID / RelayIDToIP, IPv4 <-> FrameWG.ReqID.
func IPToRelayID(a netip.Addr) uint64 {
	if !a.Is4() {
		return 0
	}
	b := a.As4()
	return uint64(binary.BigEndian.Uint32(b[:]))
}

func RelayIDToIP(id uint64) netip.Addr {
	var b [4]byte
	binary.BigEndian.PutUint32(b[:], uint32(id))
	return netip.AddrFrom4(b)
}

// NetRegister: dugum kaydi. WGPub 32 baytlik Curve25519 acik anahtarin base64'u.
// Ozel anahtar ASLA gonderilmez.
type NetRegister struct {
	Type    MessageType `json:"type"`
	WGPub   string      `json:"wg_pub"`
	Name    string      `json:"name,omitempty"`
	Version string      `json:"version,omitempty"`
	// DiscoPub (W2): disco yoklamalarini muhurleyen ayri Curve25519 acik anahtari
	// (32 bayt base64). Bos ise dugum dogrudan yol istemez (yalniz relay).
	DiscoPub string `json:"disco_pub,omitempty"`
}

type NetRegistered struct {
	Type   MessageType `json:"type"`
	NodeID string      `json:"node_id"`
	IP     string      `json:"ip"`
	Name   string      `json:"name"`
	MTU    int         `json:"mtu"`
	Kind   string      `json:"kind"` // device | session
	// W2: sunucu dogrudan yolu destekliyor ve bu dugum DiscoPub verdiyse true.
	Punch bool `json:"punch,omitempty"`
	// Stun: istemcinin yansima adresi icin sorgulayacagi "host:port" listesi.
	// Bossa ve StunPort > 0 ise istemci sunucu ana makinesi:StunPort kullanir.
	Stun     []string `json:"stun,omitempty"`
	StunPort int      `json:"stun_port,omitempty"`
	// W3: sunucu Mod B'yi (wg_tun) destekliyor; Pool = kiracinin ic adres havuzu
	// (CIDR) — istemci yerel arayuzlerle cakismayi buna gore denetler.
	Tun  bool   `json:"tun,omitempty"`
	Pool string `json:"pool,omitempty"`
	// Z5: sunucu erisim gunlugunu (net_access_log) kabul ediyor.
	ALog bool `json:"alog,omitempty"`
}

// NetPeer, netmap'teki bir es.
type NetPeer struct {
	NodeID string `json:"node_id"`
	WGPub  string `json:"wg_pub"`
	IP     string `json:"ip"`
	Name   string `json:"name"`
	Kind   string `json:"kind"`
	Online bool   `json:"online"`
	// DiscoPub (W2): esin disco acik anahtari (base64); bos = dogrudan yol yok.
	DiscoPub string `json:"disco_pub,omitempty"`
	// Tun (W3): es Mod B (OS TUN) modunda.
	Tun bool `json:"tun,omitempty"`
}

// NetShareIn: bu dugumun ERISEBILDIGI paylasim.
type NetShareIn struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	OwnerName   string `json:"owner_name"`
	OwnerIP     string `json:"owner_ip"`
	Port        int    `json:"port"`
	Proto       string `json:"proto"` // tcp | udp
	ExpiresUnix int64  `json:"expires_unix,omitempty"`
}

// NetShareOut: bu dugumun SUNDUGU paylasim. Allow = baglanmaya yetkili es IP'leri.
type NetShareOut struct {
	ID          string   `json:"id"`
	Name        string   `json:"name"`
	Port        int      `json:"port"`
	Proto       string   `json:"proto"`
	Allow       []string `json:"allow"`
	ExpiresUnix int64    `json:"expires_unix,omitempty"`
}

// NetMap: Full=true ise alicinin tum durumunu degistirir; degilse delta'dir
// (Peers/SharesIn/SharesOut upsert, Removed* silme).
type NetMap struct {
	Type  MessageType `json:"type"`
	Epoch int64       `json:"epoch"`
	Full  bool        `json:"full"`
	Self  NetPeer     `json:"self"`
	MTU   int         `json:"mtu,omitempty"`

	Peers        []NetPeer     `json:"peers,omitempty"`
	RemovedPeers []string      `json:"removed_peers,omitempty"` // wg_pub
	SharesIn     []NetShareIn  `json:"shares_in,omitempty"`
	RemovedIn    []string      `json:"removed_shares_in,omitempty"`
	SharesOut    []NetShareOut `json:"shares_out,omitempty"`
	RemovedOut   []string      `json:"removed_shares_out,omitempty"`

	// W3 (yalniz Mod B dugumlerine): ACLSet=true ise ACL listesi TAMAMEN
	// degisir (bos olabilir). DNS != nil ise sanal DNS bilgisi degisir.
	ACLSet bool         `json:"acl_set,omitempty"`
	ACL    []NetACLRule `json:"acl,omitempty"`
	DNS    *NetDNS      `json:"dns,omitempty"`
}

// NetGrantee: paylasim alicisi. Type: user | group | device.
type NetGrantee struct {
	Type string `json:"type"`
	ID   string `json:"id"`
}

type NetShareCreate struct {
	Type       MessageType  `json:"type"`
	ReqID      string       `json:"req_id"`
	Name       string       `json:"name,omitempty"`
	Port       int          `json:"port"`
	Proto      string       `json:"proto,omitempty"`
	To         []NetGrantee `json:"to"`
	ExpiresSec int          `json:"expires_sec,omitempty"`
	// Ephemeral: cihaz oturumu bitince paylasim silinir.
	Ephemeral bool `json:"ephemeral,omitempty"`
}

type NetShareDelete struct {
	Type    MessageType `json:"type"`
	ReqID   string      `json:"req_id"`
	ShareID string      `json:"share_id"`
}

type NetShareResult struct {
	Type    MessageType `json:"type"`
	ReqID   string      `json:"req_id"`
	OK      bool        `json:"ok"`
	ShareID string      `json:"share_id,omitempty"`
	Code    string      `json:"code,omitempty"`
	Message string      `json:"message,omitempty"`
}

type NetError struct {
	Type    MessageType `json:"type"`
	Code    string      `json:"code"`
	Message string      `json:"message"`
}

// NetState, tam/delta netmap'in uygulandigi durum (istemci ve testler ortak).
type NetState struct {
	Self      NetPeer
	MTU       int
	Epoch     int64
	Peers     map[string]NetPeer // wg_pub -> peer
	SharesIn  map[string]NetShareIn
	SharesOut map[string]NetShareOut
	ACL       []NetACLRule
	DNS       *NetDNS
}

func NewNetState() *NetState {
	return &NetState{Peers: map[string]NetPeer{}, SharesIn: map[string]NetShareIn{}, SharesOut: map[string]NetShareOut{}}
}

func (s *NetState) Apply(m NetMap) {
	if m.Full {
		s.Peers = map[string]NetPeer{}
		s.SharesIn = map[string]NetShareIn{}
		s.SharesOut = map[string]NetShareOut{}
	}
	s.Epoch = m.Epoch
	s.Self = m.Self
	if m.MTU > 0 {
		s.MTU = m.MTU
	}
	if m.Full {
		s.ACL, s.DNS = nil, nil
	}
	if m.ACLSet {
		s.ACL = m.ACL
	}
	if m.DNS != nil {
		d := *m.DNS
		s.DNS = &d
	}
	for _, k := range m.RemovedPeers {
		delete(s.Peers, k)
	}
	for _, k := range m.RemovedIn {
		delete(s.SharesIn, k)
	}
	for _, k := range m.RemovedOut {
		delete(s.SharesOut, k)
	}
	for _, p := range m.Peers {
		s.Peers[p.WGPub] = p
	}
	for _, x := range m.SharesIn {
		s.SharesIn[x.ID] = x
	}
	for _, x := range m.SharesOut {
		s.SharesOut[x.ID] = x
	}
}
