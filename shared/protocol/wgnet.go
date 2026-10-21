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
)

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
}

type NetRegistered struct {
	Type   MessageType `json:"type"`
	NodeID string      `json:"node_id"`
	IP     string      `json:"ip"`
	Name   string      `json:"name"`
	MTU    int         `json:"mtu"`
	Kind   string      `json:"kind"` // device | session
}

// NetPeer, netmap'teki bir es.
type NetPeer struct {
	NodeID string `json:"node_id"`
	WGPub  string `json:"wg_pub"`
	IP     string `json:"ip"`
	Name   string `json:"name"`
	Kind   string `json:"kind"`
	Online bool   `json:"online"`
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
