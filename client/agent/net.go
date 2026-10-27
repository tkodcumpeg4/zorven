package agent

import (
	"context"
	"net"
	"os"
	"path/filepath"
	"strings"

	"github.com/tkodcumpeg4/zorven/client/wgnet"
	"github.com/tkodcumpeg4/zorven/shared/protocol"
)

// Zorven Net (WireGuard Mod A) agent entegrasyonu. wg_net + wg_relay hem
// istemci hem sunucu tarafindan pazarlanmissa ajan bir wg dugumu calistirir:
// kontrol mesajlari ve FrameWG mevcut agent WSS'inden gider.

// NetShareSpec, `zorven share`: bu cihazin bir portunu paylas.
type NetShareSpec struct {
	Name       string
	Port       int
	Proto      string // tcp | udp
	To         []protocol.NetGrantee
	ExpiresSec int
	// Persist false ise paylasim ajan oturumu bitince silinir.
	Persist bool
}

// NetAccessSpec: bu cihaza (cihaz grant'i ile) acilan bir paylasima yerel dinleyici.
type NetAccessSpec struct {
	Target string // "paylasim-adi[:port]"
	Local  string // "127.0.0.1:15432"
}

// csTransport, wgnet.Transport'u agent oturumuna baglar.
type csTransport struct{ cs *clientSession }

func (t csTransport) SendControl(ctx context.Context, v any, mt protocol.MessageType) error {
	return t.cs.sendControl(ctx, v, mt)
}
func (t csTransport) SendBinary(ctx context.Context, b []byte) error { return t.cs.sendBinary(ctx, b) }

func (a *Agent) netKeyPath() string {
	if a.NetKeyPath != "" {
		return a.NetKeyPath
	}
	dir, err := os.UserConfigDir()
	if err != nil {
		return ""
	}
	return filepath.Join(dir, "zorven", "wg.key")
}

// startNet, wg dugumunu kurar ve kaydeder. Hata halinde ajan tunel islevine
// devam eder (nil doner).
func (a *Agent) startNet(ctx context.Context, cs *clientSession) *wgnet.Node {
	var key wgnet.Key
	var err error
	if p := a.netKeyPath(); p != "" {
		key, err = wgnet.LoadOrCreateKey(p)
	} else {
		key, err = wgnet.GenerateKey()
	}
	if err != nil {
		a.Log.Warn("wg anahtari hazirlanamadi, zorven net kapali", "hata", err)
		return nil
	}
	n := wgnet.NewNode(a.Log, csTransport{cs}, key, deviceHostname())
	if h, _, err := net.SplitHostPort(a.ServerAddr); err == nil {
		n.ServerHost = h
	}
	n.DisableDirect = n.DisableDirect || a.NoP2P
	n.ALogInterval = a.NetALogInterval
	if err := n.Register(ctx); err != nil {
		a.Log.Warn("net_register gonderilemedi", "hata", err)
		n.Close()
		return nil
	}
	go a.netBootstrap(ctx, n)
	return n
}

func (a *Agent) netEvent(msg string) {
	if a.OnNetEvent != nil {
		a.OnNetEvent(msg)
	}
}

func (a *Agent) netBootstrap(ctx context.Context, n *wgnet.Node) {
	if err := n.WaitReady(ctx); err != nil {
		if ctx.Err() == nil {
			a.netEvent("zorven net baslatilamadi: " + err.Error())
		}
		return
	}
	st := n.State()
	a.netEvent("zorven net hazir: " + st.Self.Name + " (" + st.Self.IP + ")")
	a.resumeTun(n)
	for _, sp := range a.NetShares {
		proto := sp.Proto
		if proto == "" {
			proto = "tcp"
		}
		id, err := n.CreateShare(ctx, protocol.NetShareCreate{
			Name: sp.Name, Port: sp.Port, Proto: proto, To: sp.To, ExpiresSec: sp.ExpiresSec,
			Ephemeral: !sp.Persist,
		})
		if err != nil {
			a.netEvent("paylasim olusturulamadi: " + err.Error())
			continue
		}
		a.netEvent("paylasim olusturuldu: " + id)
	}
	for _, ac := range a.NetAccess {
		a.serveNetAccess(ctx, n, ac)
	}
}

// serveNetAccess, cihazin kendi dugumuyle bir paylasima yerel dinleyici acar.
func (a *Agent) serveNetAccess(ctx context.Context, n *wgnet.Node, ac NetAccessSpec) {
	target := strings.TrimSpace(ac.Target)
	proto, err := n.ShareProto(target)
	if err != nil {
		a.netEvent("erisim hedefi cozulemedi (" + target + "): " + err.Error())
		return
	}
	dial := func(c context.Context) (net.Conn, error) { return n.DialShare(c, target) }
	if proto == "udp" {
		pc, err := net.ListenPacket("udp", ac.Local)
		if err != nil {
			a.netEvent("dinlenemedi: " + err.Error())
			return
		}
		go wgnet.ServeUDP(ctx, pc, dial, nil)
	} else {
		ln, err := net.Listen("tcp", ac.Local)
		if err != nil {
			a.netEvent("dinlenemedi: " + err.Error())
			return
		}
		go wgnet.ServeTCP(ctx, ln, dial, nil)
	}
	a.netEvent("erisim: " + target + " -> " + ac.Local)
}
