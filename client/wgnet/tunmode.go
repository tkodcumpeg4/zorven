package wgnet

import (
	"context"
	"errors"
	"fmt"
	"net/netip"
	"os"
	"strings"
	"time"

	"github.com/tkodcumpeg4/zorven/shared/protocol"
	"golang.zx2c4.com/wireguard/tun"
	"golang.zx2c4.com/wireguard/tun/netstack"
)

// Mod B (W3): ayni Node, wireguard-go tun.Device'i olarak netstack yerine OS TUN
// (paket filtresiyle sarili) kullanir. Anahtar, peer, bind, yol secimi (direct /
// relay) kodu degismez; yalnizca aygit degisir:
//
//	Mod A  : netstack.Net  <-> wireguard-go <-> relayBind
//	Mod B  : OS TUN -> Filter -> wireguard-go <-> relayBind
//
// Mod A'daki paylasim sunucusu (netstack uzerinden 127.0.0.1 koprusu) Mod B'de
// calismaz: gelen baglantiyi OS yigini karsilar (servis 0.0.0.0'da dinlemeli) ve
// izin ACL'den (paylasimlardan turetilir) gelir.

// TUNOptions, Mod B ayarlari.
type TUNOptions struct {
	// Name: arayuz adi (varsayilan zorven0).
	Name string
	// OS: bos ise platform varsayilani (testlerde sahte).
	OS OSNet
	// LocalAddrs: havuz cakisma denetimi icin yerel arayuz adresleri (testlerde sahte).
	LocalAddrs func() ([]IfaceAddr, error)
	// DenyInbound: yeni gelen baglantilari yerelde reddet (yalniz giden akislar).
	DenyInbound bool
}

// TUNStatus, Mod B durumu.
type TUNStatus struct {
	Active    bool
	Interface string
	IP        string
	Pool      string
	MTU       int
	Resolver  string
	// DNS: OS split-DNS kaydi sonucu.
	DNSOK     bool
	DNSMethod string
	DNSNote   string
	Filter    FilterStats
	Platform  string
	// ServerTun: sunucu Mod B'yi pazarladi (false ise EnableTUN reddeder).
	ServerTun bool
}

var errNotReady = errors.New("wg dugumu hazir degil (sunucuya kayit bekleniyor)")

// ResolverFor, havuzun sanal DNS cozucu adresi (.1) -- sunucudaki netctl.ResolverIP ile aynidir.
func ResolverFor(pool netip.Prefix) netip.Addr {
	b := pool.Masked().Addr().As4()
	v := uint32(b[0])<<24 | uint32(b[1])<<16 | uint32(b[2])<<8 | uint32(b[3])
	v++
	return netip.AddrFrom4([4]byte{byte(v >> 24), byte(v >> 16), byte(v >> 8), byte(v)})
}

// SupportsTUN, sunucu pazarlikla Mod B'yi etkinlestirdi mi.
func (n *Node) SupportsTUN() bool {
	n.mu.Lock()
	defer n.mu.Unlock()
	return n.serverTun
}

// TUNActive, Mod B acik mi.
func (n *Node) TUNActive() bool {
	n.mu.Lock()
	defer n.mu.Unlock()
	return n.tunOn
}

// takeTunUndoLocked, kayitli geri alma islemlerini alir (cagiran kilidi biraktiktan sonra calistirir).
func (n *Node) takeTunUndoLocked() []func() {
	u := n.tunUndo
	n.tunUndo = nil
	n.tunOn = false
	if n.filt != nil {
		n.filt.SetAccessLog(false)
	}
	if n.alogDone != nil {
		close(n.alogDone)
		n.alogDone = nil
	}
	n.filt = nil
	return u
}

// updateFilterLocked, netmap'ten filtre kurallarini/esleri/DNS tablosunu tazeler.
func (n *Node) updateFilterLocked() {
	if n.filt == nil {
		return
	}
	n.filt.SetRules(n.state.ACL)
	ips := make([]netip.Addr, 0, len(n.state.Peers))
	for _, p := range n.state.Peers {
		if a, err := netip.ParseAddr(p.IP); err == nil {
			ips = append(ips, a)
		}
	}
	n.filt.SetPeers(ips)
	n.filt.SetDNSNames(DNSNames(n.state))
}

// EnableTUN, dugumu Mod B'ye (OS TUN, tam ag katmani) gecirir. Yetki/platform/havuz
// cakismasi hatalarinda hicbir sey degismez (Mod A calismaya devam eder).
func (n *Node) EnableTUN(opts TUNOptions) (TUNStatus, error) {
	st, err := n.enableTUN(opts)
	if err != nil {
		return TUNStatus{}, err
	}
	if err := n.sendMode(true); err != nil {
		_ = n.DisableTUN()
		return TUNStatus{}, fmt.Errorf("sunucuya Mod B bildirilemedi: %w", err)
	}
	return st, nil
}

func (n *Node) sendMode(on bool) error {
	n.mu.Lock()
	serverTun := n.serverTun
	n.mu.Unlock()
	if !serverTun {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	return n.tr.SendControl(ctx, protocol.NetMode{Type: protocol.TypeNetMode, TUN: on}, protocol.TypeNetMode)
}

func (n *Node) enableTUN(opts TUNOptions) (TUNStatus, error) {
	n.mu.Lock()
	defer n.mu.Unlock()
	if n.closed || n.dev == nil {
		return TUNStatus{}, errNotReady
	}
	if n.tunOn {
		return n.tunStatusLocked(), nil
	}
	if !n.serverTun {
		return TUNStatus{}, errors.New("sunucu tam ag katmanini (Mod B / wg_tun) desteklemiyor veya bu surumde kapali")
	}
	if !n.pool.IsValid() {
		return TUNStatus{}, errors.New("sunucu ag araligini (havuz) bildirmedi")
	}
	osn := opts.OS
	if osn == nil {
		osn = DefaultOS()
	}
	name := opts.Name
	if name == "" {
		name = DefaultTUNName
	}
	la := opts.LocalAddrs
	if la == nil {
		la = LocalIfaceAddrs
	}
	if addrs, err := la(); err == nil {
		if cs := CheckPoolConflict(n.pool, addrs, name); len(cs) > 0 {
			return TUNStatus{}, PoolConflictError(n.pool, cs)
		}
	}
	osn.CleanupStale()
	mtu := n.mtu
	raw, err := osn.CreateTUN(name, mtu)
	if err != nil {
		return TUNStatus{}, err
	}
	resolver := ResolverFor(n.pool)
	filt := NewFilter(raw, n.selfIP, resolver, protoZone)
	filt.SetDenyInbound(opts.DenyInbound)
	undoCfg, err := osn.Configure(raw, n.selfIP, n.pool, mtu)
	if err != nil {
		_ = raw.Close()
		return TUNStatus{}, fmt.Errorf("ag arayuzu yapilandirilamadi: %w", err)
	}
	if err := n.swapDeviceLocked(filt); err != nil {
		undoCfg()
		_ = raw.Close()
		if rerr := n.restoreNetstackLocked(); rerr != nil {
			n.log.Error("Mod A geri yuklenemedi", "hata", rerr)
		}
		return TUNStatus{}, fmt.Errorf("wireguard cihazi TUN'a gecirilemedi: %w", err)
	}
	n.tunOn, n.filt, n.tunOS = true, filt, osn
	if ifn, err := raw.Name(); err == nil {
		name = ifn
	}
	n.tunName = name
	n.updateFilterLocked()
	if n.serverALog && !alogDisabledByEnv() {
		filt.SetAccessLog(true)
		n.alogDone = make(chan struct{})
		go n.alogLoop(filt, n.alogDone)
	}
	n.tunDNS = osn.SetupDNS(raw, resolver, protoZone)
	if n.tunDNS.Undo != nil {
		n.tunUndo = append(n.tunUndo, n.tunDNS.Undo)
	}
	n.tunUndo = append(n.tunUndo, undoCfg)
	n.log.Info("zorven net Mod B (TUN) acik", "arayuz", name, "ip", n.selfIP, "havuz", n.pool, "dns", n.tunDNS.Method)
	return n.tunStatusLocked(), nil
}

// protoZone, sanal DNS bolgesi (sunucudaki netctl.TunZone ile ayni).
const protoZone = "zorven.internal"

// swapDeviceLocked, calisan wireguard cihazini kapatip verilen tun aygiti uzerinde
// yenisini acar; esleri yeniden yapilandirir. n.mu altinda.
func (n *Node) swapDeviceLocked(tdev tun.Device) error {
	old := n.dev
	if n.shares != nil {
		n.shares.closeAll()
		n.shares = nil
	}
	for tc := range n.dialed {
		_ = tc.Conn.Close()
		delete(n.dialed, tc)
	}
	n.dev, n.tnet = nil, nil
	old.Close() // bind'i de kapatir; yeni cihaz Open'da yeniden acar
	n.peers = map[string]netip.Addr{}
	nd, err := n.newDeviceLocked(tdev)
	if err != nil {
		return err
	}
	n.dev = nd
	n.syncPeersLocked()
	return nil
}

// restoreNetstackLocked, Mod A (netstack) cihazini yeniden kurar.
func (n *Node) restoreNetstackLocked() error {
	tdev, tnet, err := netstack.CreateNetTUN([]netip.Addr{n.selfIP}, nil, n.mtu)
	if err != nil {
		return err
	}
	if n.dev != nil {
		n.dev.Close()
		n.dev = nil
	}
	nd, err := n.newDeviceLocked(tdev)
	if err != nil {
		return err
	}
	n.dev, n.tnet = nd, tnet
	n.peers = map[string]netip.Addr{}
	n.shares = newShareServer(n.log, tnet)
	n.syncPeersLocked()
	return nil
}

// DisableTUN, Mod B'den cikar: OS kayitlarini (DNS, rota) geri alir, TUN'u kapatir
// ve Mod A'ya (netstack) doner.
func (n *Node) DisableTUN() error {
	n.mu.Lock()
	if !n.tunOn || n.closed {
		n.mu.Unlock()
		return nil
	}
	undo := n.takeTunUndoLocked()
	// Once DNS/rota geri al (aygit hala acikken), sonra aygiti Mod A ile degistir.
	n.mu.Unlock()
	for _, u := range undo {
		u()
	}
	n.mu.Lock()
	err := n.restoreNetstackLocked()
	var outs []protocol.NetShareOut
	if !n.DisableShares {
		for _, s := range n.state.SharesOut {
			outs = append(outs, s)
		}
	}
	ss := n.shares
	n.mu.Unlock()
	if ss != nil {
		ss.reconcile(outs)
	}
	_ = n.sendMode(false)
	n.log.Info("zorven net Mod B (TUN) kapali")
	return err
}

// TUNStatus, Mod B durumu (Active=false ise yalniz sunucu/platform bilgisi dolu).
func (n *Node) TUNStatus() TUNStatus {
	n.mu.Lock()
	defer n.mu.Unlock()
	return n.tunStatusLocked()
}

func (n *Node) tunStatusLocked() TUNStatus {
	st := TUNStatus{ServerTun: n.serverTun, Platform: PlatformName()}
	if n.pool.IsValid() {
		st.Pool = n.pool.String()
		st.Resolver = ResolverFor(n.pool).String()
	}
	if !n.tunOn {
		return st
	}
	st.Active, st.Interface, st.IP, st.MTU = true, n.tunName, n.selfIP.String(), n.mtu
	st.DNSOK, st.DNSMethod, st.DNSNote = n.tunDNS.OK, n.tunDNS.Method, n.tunDNS.Note
	if n.filt != nil {
		st.Filter = n.filt.Stats()
	}
	return st
}

// alogDisabledByEnv: ZORVEN_NET_ACCESS_LOG=0|off|false|no ise erisim gunlugu gonderilmez
// (yerel gizlilik tercihi; sunucu zaten yalniz ust veri alir).
func alogDisabledByEnv() bool {
	switch strings.ToLower(strings.TrimSpace(os.Getenv("ZORVEN_NET_ACCESS_LOG"))) {
	case "0", "off", "false", "no":
		return true
	}
	return false
}
