package localctl

import (
	"context"
	"errors"
	"time"
)

// W3 / Mod B (OS TUN) IPC islemleri:
//
//	net_up     — tam ag katmanini (TUN) ac (servis/root ajan yapar)
//	net_down   — kapat
//	net_status — durum (arayuz, IP, esler, yol, DNS)
//
// Ajan bir servis olarak (root/SYSTEM) calisiyorsa yetkiyi o tasir; CLI ve
// masaustu uygulamasi yalniz bu kanaldan komut verir. Soket izinleri
// localctl.go'daki gibidir (servis soketi: yalniz root/SYSTEM+Administrators).

// TunInfo, Mod B durumu.
type TunInfo struct {
	Active    bool   `json:"active"`
	Interface string `json:"interface,omitempty"`
	Name      string `json:"name,omitempty"` // bu cihazin Zorven adi
	IP        string `json:"ip,omitempty"`
	Pool      string `json:"pool,omitempty"`
	Resolver  string `json:"resolver,omitempty"`
	MTU       int    `json:"mtu,omitempty"`
	Platform  string `json:"platform,omitempty"`
	// ServerTun: sunucu Mod B'yi pazarladi.
	ServerTun bool `json:"server_tun"`
	// Connected: ajanin sunucuyla etkin bir Zorven Net oturumu var. false ise
	// Reason nedenini verir (or. sunucuya baglanti bekleniyor).
	Connected bool   `json:"connected"`
	Reason    string `json:"reason,omitempty"`
	// Wanted: kullanici Mod B'yi istedi (yeniden baglaninca otomatik yeniden acilir).
	Wanted    bool   `json:"wanted"`
	DNSOK     bool   `json:"dns_ok"`
	DNSMethod string `json:"dns_method,omitempty"`
	DNSNote   string `json:"dns_note,omitempty"`
	// Sayaclar.
	DroppedIn  int64 `json:"dropped_in"`
	DroppedOut int64 `json:"dropped_out"`
	DNSServed  int64 `json:"dns_served"`
	// Peers: netmap'teki esler, yol ve RTT.
	Peers []PathInfo `json:"peers,omitempty"`
	// Service: yanit servis ajanindan geldi.
	Service bool `json:"service,omitempty"`
}

// TunController, ajanin Mod B IPC yuzeyi (Controller bunu da uygularsa net_up,
// net_down ve net_status acilir).
type TunController interface {
	NetUp(ctx context.Context) (TunInfo, error)
	NetDown(ctx context.Context) (TunInfo, error)
	NetTunStatus() (TunInfo, error)
}

// ErrNoTun, ajan Mod B IPC'sini desteklemiyor.
var ErrNoTun = errors.New("bilinmeyen islem")

func (e Endpoint) tunOp(ctl Controller, req Request, resp *Response) error {
	tc, ok := ctl.(TunController)
	if !ok {
		return ErrNoTun
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	var info TunInfo
	var err error
	switch req.Op {
	case "net_up":
		info, err = tc.NetUp(ctx)
	case "net_down":
		info, err = tc.NetDown(ctx)
	default:
		info, err = tc.NetTunStatus()
	}
	if err != nil {
		return err
	}
	info.Service = e.Service
	resp.Tun = &info
	return nil
}
