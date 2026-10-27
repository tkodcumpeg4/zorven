package agent

import (
	"context"
	"errors"
	"fmt"
	"net"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/tkodcumpeg4/zorven/client/localctl"
	"github.com/tkodcumpeg4/zorven/client/wgnet"
	"github.com/tkodcumpeg4/zorven/shared/protocol"
)

// netRuntime, calisan wg dugumunu ve IPC (localctl) ile acilan paylasim /
// erisim dinleyicilerini tutar. Oturum yeniden baslayinca set/clear yenilenir.
type netRuntime struct {
	mu     sync.Mutex
	ctx    context.Context
	node   *wgnet.Node
	shares map[string]localctl.ShareInfo // id -> IPC ile olusturulan paylasim
	access map[string]context.CancelFunc // yerel adres -> kapatici
	// tunWanted: kullanici Mod B'yi (OS TUN) istedi; yeniden baglaninca otomatik acilir.
	tunWanted bool
}

func (r *netRuntime) set(ctx context.Context, n *wgnet.Node) {
	r.mu.Lock()
	r.ctx, r.node = ctx, n
	r.mu.Unlock()
}

func (r *netRuntime) clear(n *wgnet.Node) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.node == n {
		r.node, r.ctx = nil, nil
		r.shares, r.access = nil, nil
	}
}

func (r *netRuntime) get() (context.Context, *wgnet.Node, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.node == nil {
		return nil, nil, localctl.ErrNoNet
	}
	return r.ctx, r.node, nil
}

// ShareCreate (localctl.NetController): calisan dugumle paylasim olusturur.
func (a *Agent) ShareCreate(ctx context.Context, sp localctl.ShareSpec) (localctl.ShareInfo, error) {
	_, n, err := a.netRT.get()
	if err != nil {
		return localctl.ShareInfo{}, err
	}
	if sp.Port < 1 || sp.Port > 65535 {
		return localctl.ShareInfo{}, fmt.Errorf("gecersiz port %d", sp.Port)
	}
	if len(sp.To) == 0 {
		return localctl.ShareInfo{}, errors.New("alici (to) gerekli")
	}
	if err := n.WaitReady(ctx); err != nil {
		return localctl.ShareInfo{}, fmt.Errorf("zorven net hazir degil: %w", err)
	}
	proto := sp.Proto
	if proto == "" {
		proto = "tcp"
	}
	req := protocol.NetShareCreate{Name: sp.Name, Port: sp.Port, Proto: proto, ExpiresSec: sp.ExpiresSec, Ephemeral: !sp.Persist}
	for _, g := range sp.To {
		req.To = append(req.To, protocol.NetGrantee{Type: g.Type, ID: g.ID})
	}
	id, err := n.CreateShare(ctx, req)
	if err != nil {
		return localctl.ShareInfo{}, err
	}
	name := sp.Name
	if name == "" {
		name = fmt.Sprintf("%s-%d", n.State().Self.Name, sp.Port)
	}
	si := localctl.ShareInfo{ID: id, Name: name, Port: sp.Port, Proto: proto, To: sp.To, Persistent: sp.Persist}
	if sp.ExpiresSec > 0 {
		si.ExpiresUnix = time.Now().Add(time.Duration(sp.ExpiresSec) * time.Second).Unix()
	}
	// Sunucunun verdigi gercek ad/allow netmap'te gelmisse onu tercih et.
	if o, ok := n.State().SharesOut[id]; ok {
		si.Name, si.Allow, si.ExpiresUnix = o.Name, o.Allow, o.ExpiresUnix
	}
	a.netRT.mu.Lock()
	if a.netRT.shares == nil {
		a.netRT.shares = map[string]localctl.ShareInfo{}
	}
	a.netRT.shares[id] = si
	a.netRT.mu.Unlock()
	return si, nil
}

// ShareDelete: id veya ada gore kendi paylasimini siler.
func (a *Agent) ShareDelete(ctx context.Context, idOrName string) (localctl.ShareInfo, error) {
	_, n, err := a.netRT.get()
	if err != nil {
		return localctl.ShareInfo{}, err
	}
	idOrName = strings.TrimSpace(idOrName)
	if idOrName == "" {
		return localctl.ShareInfo{}, errors.New("paylasim id veya adi gerekli")
	}
	var hit []protocol.NetShareOut
	for _, o := range n.State().SharesOut {
		if o.ID == idOrName {
			hit = []protocol.NetShareOut{o}
			break
		}
		if o.Name == idOrName {
			hit = append(hit, o)
		}
	}
	switch len(hit) {
	case 0:
		return localctl.ShareInfo{}, errors.New("paylasim bulunamadi: " + idOrName)
	case 1:
	default:
		return localctl.ShareInfo{}, errors.New("birden fazla paylasim bu adla eslesti; id kullanin: " + idOrName)
	}
	o := hit[0]
	if err := n.DeleteShare(ctx, o.ID); err != nil {
		return localctl.ShareInfo{}, err
	}
	a.netRT.mu.Lock()
	si, ok := a.netRT.shares[o.ID]
	delete(a.netRT.shares, o.ID)
	a.netRT.mu.Unlock()
	if !ok {
		si = localctl.ShareInfo{ID: o.ID, Name: o.Name, Port: o.Port, Proto: o.Proto, Allow: o.Allow}
	}
	return si, nil
}

// ShareList: bu cihazin sundugu paylasimlar.
func (a *Agent) ShareList() ([]localctl.ShareInfo, error) {
	_, n, err := a.netRT.get()
	if err != nil {
		return nil, err
	}
	a.netRT.mu.Lock()
	defer a.netRT.mu.Unlock()
	var out []localctl.ShareInfo
	paths := n.Paths()
	for _, o := range n.State().SharesOut {
		si := a.netRT.shares[o.ID]
		si.ID, si.Name, si.Port, si.Proto, si.Allow, si.ExpiresUnix = o.ID, o.Name, o.Port, o.Proto, o.Allow, o.ExpiresUnix
		si.Path = pathSummary(o.Allow, paths)
		out = append(out, si)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

// NetPaths (localctl.NetStatuser): netmap'teki esler ve secili yol (dogrudan/relay).
func (a *Agent) NetPaths() ([]localctl.PathInfo, error) {
	_, n, err := a.netRT.get()
	if err != nil {
		return nil, err
	}
	var out []localctl.PathInfo
	for _, p := range n.Paths() {
		out = append(out, localctl.PathInfo{
			Name: p.Name, IP: p.IP, Online: p.Online, Path: p.Path, Addr: p.Addr,
			RTTMs: int(p.RTT / time.Millisecond), DirectCapable: p.DirectCapable,
		})
	}
	return out, nil
}

// pathSummary: izinli esler arasindaki cevrimici olanlarin yol ozeti.
func pathSummary(allow []string, paths []wgnet.PathInfo) string {
	by := map[string]wgnet.PathInfo{}
	for _, p := range paths {
		by[p.IP] = p
	}
	var d, r int
	for _, ip := range allow {
		p, ok := by[ip]
		if !ok || !p.Online {
			continue
		}
		if p.Path == protocol.PathDirect {
			d++
		} else {
			r++
		}
	}
	switch {
	case d > 0 && r > 0:
		return "mixed"
	case d > 0:
		return protocol.PathDirect
	case r > 0:
		return protocol.PathRelay
	}
	return ""
}

// AccessOpen: bu cihazin dugumuyle bir paylasima yerel dinleyici acar.
func (a *Agent) AccessOpen(ctx context.Context, target, local string) (string, error) {
	rctx, n, err := a.netRT.get()
	if err != nil {
		return "", err
	}
	h, _, err := net.SplitHostPort(local)
	if err != nil {
		return "", fmt.Errorf("gecersiz yerel adres %q (host:port)", local)
	}
	if ip := net.ParseIP(h); ip == nil || !ip.IsLoopback() {
		return "", errors.New("yerel adres yalnizca loopback olabilir")
	}
	if err := n.WaitReady(ctx); err != nil {
		return "", fmt.Errorf("zorven net hazir degil: %w", err)
	}
	target = strings.TrimSpace(target)
	proto, err := n.ShareProto(target)
	if err != nil {
		return "", err
	}
	lctx, cancel := context.WithCancel(rctx)
	dial := func(c context.Context) (net.Conn, error) { return n.DialShare(c, target) }
	var addr string
	if proto == "udp" {
		pc, err := net.ListenPacket("udp", local)
		if err != nil {
			cancel()
			return "", fmt.Errorf("dinlenemedi: %w", err)
		}
		addr = pc.LocalAddr().String()
		go func() { <-lctx.Done(); pc.Close() }()
		go wgnet.ServeUDP(lctx, pc, dial, nil)
	} else {
		ln, err := net.Listen("tcp", local)
		if err != nil {
			cancel()
			return "", fmt.Errorf("dinlenemedi: %w", err)
		}
		addr = ln.Addr().String()
		go wgnet.ServeTCP(lctx, ln, dial, nil)
	}
	a.netRT.mu.Lock()
	if a.netRT.access == nil {
		a.netRT.access = map[string]context.CancelFunc{}
	}
	if old := a.netRT.access[addr]; old != nil {
		old()
	}
	a.netRT.access[addr] = cancel
	a.netRT.mu.Unlock()
	a.netEvent("erisim: " + target + " -> " + addr)
	return addr, nil
}

// AccessClose: AccessOpen ile acilan dinleyiciyi kapatir.
func (a *Agent) AccessClose(local string) error {
	a.netRT.mu.Lock()
	defer a.netRT.mu.Unlock()
	c := a.netRT.access[local]
	if c == nil {
		return errors.New("bu adreste IPC ile acilmis erisim dinleyicisi yok: " + local)
	}
	c()
	delete(a.netRT.access, local)
	return nil
}
