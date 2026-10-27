package agent

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/tkodcumpeg4/zorven/client/localctl"
	"github.com/tkodcumpeg4/zorven/client/wgnet"
)

// Zorven Net Mod B (W3, OS TUN) agent entegrasyonu. Ayni wg dugumu (cihaz
// kimligi) netstack yerine OS TUN'a gecirilir. IPC (localctl) net_up / net_down /
// net_status ile yonetilir. Dugum cihaz kimligiyle (cihaz token'i) girdigi icin
// ayni anda yalniz bu cihazin organizasyonu icin TUN acik olur.
//
// Yeniden baglanmada (WSS koptu) dugum yeniden kurulur; kullanici Mod B'yi
// istediyse (wanted) hazir olunca otomatik yeniden acilir.

var staleOnce sync.Once

// tunDenyInboundEnv: ZORVEN_NET_DENY_INBOUND=1 -> yeni gelen baglantilar yerelde reddedilir.
func tunDenyInbound(a *Agent) bool {
	if a.TUNDenyInbound {
		return true
	}
	v := strings.ToLower(strings.TrimSpace(os.Getenv("ZORVEN_NET_DENY_INBOUND")))
	return v == "1" || v == "true" || v == "on"
}

func (a *Agent) tunSupported() bool {
	return !a.NoNet && !a.NoTUN && (a.TUNOptions.OS != nil || wgnet.DefaultOS().Name() != "unsupported")
}

func (a *Agent) tunOpts() wgnet.TUNOptions {
	o := a.TUNOptions
	o.DenyInbound = tunDenyInbound(a)
	return o
}

func (r *netRuntime) setWanted(v bool) {
	r.mu.Lock()
	r.tunWanted = v
	r.mu.Unlock()
}

func (r *netRuntime) wanted() bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.tunWanted
}

// resumeTun, dugum hazir olunca (yeniden baglanma dahil) istenmisse Mod B'yi acar.
func (a *Agent) resumeTun(n *wgnet.Node) {
	if !a.netRT.wanted() || !a.tunSupported() {
		return
	}
	if _, err := n.EnableTUN(a.tunOpts()); err != nil {
		a.netEvent("Mod B yeniden acilamadi: " + err.Error())
		return
	}
	a.netEvent("Mod B (TUN) yeniden acildi")
}

// NetUp (localctl.TunController): tam ag katmanini acar.
func (a *Agent) NetUp(ctx context.Context) (localctl.TunInfo, error) {
	if !a.tunSupported() {
		return localctl.TunInfo{}, fmt.Errorf("tam ag katmani (Mod B) bu istemcide kapali (--no-net / --no-tun) veya bu isletim sisteminde desteklenmiyor")
	}
	_, n, err := a.netRT.get()
	if err != nil {
		return localctl.TunInfo{}, err
	}
	wctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	if err := n.WaitReady(wctx); err != nil {
		return localctl.TunInfo{}, fmt.Errorf("zorven net hazir degil (sunucuya baglanti bekleniyor): %w", err)
	}
	a.staleCleanup()
	// Windows: wintun.dll yoksa imzali manifest uzerinden sunucudan indirilir.
	if err := a.EnsureWintun(ctx); err != nil {
		return localctl.TunInfo{}, err
	}
	if _, err := n.EnableTUN(a.tunOpts()); err != nil {
		if !a.IsService {
			err = fmt.Errorf("%w\n(ajan servis olarak calismiyor; Mod B ayricalikli bir servis ister: "+
				"'zorven service install' ile kurup 'zorven net up' komutunu yetkili calistirin)", err)
		}
		return localctl.TunInfo{}, err
	}
	a.netRT.setWanted(true)
	a.netEvent("Mod B (TUN) acik")
	return a.tunInfo(n), nil
}

// NetDown (localctl.TunController): tam ag katmanini kapatir (Mod A'ya doner).
func (a *Agent) NetDown(ctx context.Context) (localctl.TunInfo, error) {
	a.netRT.setWanted(false)
	_, n, err := a.netRT.get()
	if err != nil {
		return localctl.TunInfo{}, err
	}
	if err := n.DisableTUN(); err != nil {
		return localctl.TunInfo{}, err
	}
	a.netEvent("Mod B (TUN) kapali")
	return a.tunInfo(n), nil
}

// NetTunStatus (localctl.TunController): Mod B durumu.
func (a *Agent) NetTunStatus() (localctl.TunInfo, error) {
	_, n, err := a.netRT.get()
	if err != nil {
		reason := "sunucuya baglanti bekleniyor"
		if errors.Is(err, localctl.ErrNoNet) {
			reason = "Zorven Net bu oturumda etkin degil (sunucuya bagli degil ya da --no-net)"
		}
		return localctl.TunInfo{Wanted: a.netRT.wanted(), Reason: reason}, nil
	}
	return a.tunInfo(n), nil
}

func (a *Agent) staleCleanup() {
	staleOnce.Do(func() { wgnet.DefaultOS().CleanupStale() })
}

func (a *Agent) tunInfo(n *wgnet.Node) localctl.TunInfo {
	st := n.TUNStatus()
	ns := n.State()
	info := localctl.TunInfo{
		Active: st.Active, Interface: st.Interface, Name: ns.Self.Name, IP: st.IP, Pool: st.Pool,
		Resolver: st.Resolver, MTU: st.MTU, Platform: st.Platform, ServerTun: st.ServerTun, Connected: true,
		Wanted: a.netRT.wanted(), DNSOK: st.DNSOK, DNSMethod: st.DNSMethod, DNSNote: st.DNSNote,
		DroppedIn: st.Filter.DroppedIn, DroppedOut: st.Filter.DroppedOut, DNSServed: st.Filter.DNSServed,
	}
	if info.IP == "" {
		info.IP = ns.Self.IP
	}
	for _, p := range n.Paths() {
		info.Peers = append(info.Peers, localctl.PathInfo{
			Name: p.Name, IP: p.IP, Online: p.Online, Path: p.Path, Addr: p.Addr,
			RTTMs: int(p.RTT / time.Millisecond), DirectCapable: p.DirectCapable,
		})
	}
	return info
}
