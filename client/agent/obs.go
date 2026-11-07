package agent

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"sync"
	"time"

	"github.com/tkodcumpeg4/zorven/client/config"
	"github.com/tkodcumpeg4/zorven/client/localctl"
	"github.com/tkodcumpeg4/zorven/shared/protocol"
)

// --- gozlemlenebilirlik durumu ---------------------------------------------

const maxDisconnects = 10

type agentObs struct {
	tm *tunnelMetrics

	mu            sync.Mutex
	startedAt     time.Time
	reconnects    int
	everConnected bool
	disconnects   []localctl.DisconnectInfo
	lastErr       string
	tunnels       []protocol.TunnelSpec
}

func (a *Agent) observ() *agentObs {
	a.obsOnce.Do(func() { a.obsV = &agentObs{tm: newTunnelMetrics()} })
	return a.obsV
}

func (o *agentObs) markStarted() {
	o.mu.Lock()
	if o.startedAt.IsZero() {
		o.startedAt = time.Now()
	}
	o.mu.Unlock()
}

// onConnected, her basarili el sikismada cagrilir. Ilk baglanti sayilmaz;
// sonrakiler "yeniden baglanma" olarak sayilir.
func (o *agentObs) onConnected(t []protocol.TunnelSpec) {
	o.mu.Lock()
	if o.everConnected {
		o.reconnects++
	}
	o.everConnected = true
	o.lastErr = ""
	o.tunnels = append([]protocol.TunnelSpec(nil), t...)
	o.mu.Unlock()
}

func (o *agentObs) setTunnels(t []protocol.TunnelSpec) {
	o.mu.Lock()
	o.tunnels = append([]protocol.TunnelSpec(nil), t...)
	o.mu.Unlock()
}

// recordDisconnect, bir baglanti dongusunun bitisini nedeniyle halka tampona yazar.
func (o *agentObs) recordDisconnect(err error) {
	reason := "baglanti kapandi"
	if err != nil {
		reason = err.Error()
	}
	o.mu.Lock()
	o.disconnects = append(o.disconnects, localctl.DisconnectInfo{At: time.Now().UTC().Format(time.RFC3339), Reason: reason})
	if len(o.disconnects) > maxDisconnects {
		o.disconnects = o.disconnects[len(o.disconnects)-maxDisconnects:]
	}
	if err != nil {
		o.lastErr = reason
	}
	o.mu.Unlock()
}

// StatusInfo, localctl "status" yanitinin gozlemlenebilirlik alanlarini doldurur
// (localctl.StatusReporter).
func (a *Agent) StatusInfo() localctl.StatusInfo {
	o := a.observ()
	a.mu.Lock()
	st := a.lastStatus
	a.mu.Unlock()
	o.mu.Lock()
	defer o.mu.Unlock()
	info := localctl.StatusInfo{
		Connected:   st.State == StateConnected,
		State:       string(st.State),
		LastError:   o.lastErr,
		Server:      a.serverAddr(),
		Reconnects:  o.reconnects,
		Disconnects: append([]localctl.DisconnectInfo{}, o.disconnects...),
		Tunnels:     tunnelInfos(o.tunnels, o.tm),
		Deploy:      a.apps != nil,
	}
	cur, pinned, nodes := a.NodeStatus()
	{
		info.NodePinned = pinned
		if cur != "" && info.Connected {
			info.Server = cur
		}
		for _, n := range nodes {
			info.Nodes = append(info.Nodes, localctl.NodeInfo{ID: n.ID, Address: n.Address, Region: n.Region,
				Priority: n.Priority, Healthy: n.Healthy, Current: n.Current, RTTMs: n.RTTMs})
		}
	}
	if info.State == "" {
		info.State = "idle"
	}
	if !o.startedAt.IsZero() {
		info.StartedAt = o.startedAt.UTC().Format(time.RFC3339)
	}
	return info
}

// --- ag degisimi / uyanma / keepalive ---------------------------------------

const (
	netWatchTick   = 5 * time.Second
	keepaliveEvery = 20 * time.Second
	keepaliveWait  = 15 * time.Second
)

// wakeJump, bir izleme adiminin uyku/duraksama sonrasi oldugunu soyler: duvar
// saati monotonik saatten belirgin ileri gitmisse (Linux/macOS'ta uyku monotonik
// saati ilerletmez) ya da tik beklenenden cok gec gelmisse (surec donmasi).
func wakeJump(wallDelta, monoDelta, tick time.Duration) bool {
	return wallDelta-monoDelta > 2*tick || wallDelta > 4*tick
}

func equalStrings(a, b []string) bool { return slices.Equal(a, b) }

// watchNetwork, Run omru boyunca uyanma ve IP degisimini izler.
func (a *Agent) watchNetwork(ctx context.Context) {
	t := time.NewTicker(netWatchTick)
	defer t.Stop()
	last := time.Now()
	ips := localIPs()
	n := 0
	for {
		select {
		case <-ctx.Done():
			return
		case now := <-t.C:
			wall := now.Round(0).Sub(last.Round(0))
			mono := now.Sub(last)
			last = now
			if wakeJump(wall, mono, netWatchTick) {
				a.Log.Info("uyanma / duraksama algilandi, baglanti yenileniyor", "sure", wall.Round(time.Second))
				a.onNetworkEvent()
				ips = localIPs()
				continue
			}
			n++
			if n%3 != 0 { // IP kontrolu ~15 sn'de bir
				continue
			}
			cur := localIPs()
			if !equalStrings(cur, ips) {
				ips = cur
				a.onIPChange(ctx, cur)
			}
		}
	}
}

// signalWake, geri cekilme beklemesini keser (bloklamaz).
func (a *Agent) signalWake() {
	select {
	case a.wakeCh <- struct{}{}:
	default:
	}
}

// onNetworkEvent: baglanti sagligi bilinemez (uyku/ag degisimi) -> mevcut baglantiyi
// kapat, geri cekilmeyi sifirla ve hemen yeniden dene.
func (a *Agent) onNetworkEvent() {
	a.netChanged.Store(true)
	a.mu.Lock()
	c := a.curConn
	a.mu.Unlock()
	if c != nil {
		c.CloseNow()
	}
	a.signalWake()
}

// onIPChange: IP kumesi degisti. Baglantiysa sunucuyu bilgilendir (device_update) ve
// baglantiyi hizlica yokla; yanit yoksa yenile. Baglanti yoksa beklemeyi kes.
func (a *Agent) onIPChange(ctx context.Context, ips []string) {
	a.mu.Lock()
	c, cs := a.curConn, a.curCS
	du := a.deviceUpdate
	a.mu.Unlock()
	if c == nil {
		a.netChanged.Store(true)
		a.signalWake()
		return
	}
	if du && cs != nil {
		if err := cs.sendControl(ctx, protocol.DeviceUpdate{Type: protocol.TypeDeviceUpdate, Hostname: deviceHostname(), IPs: ips}, protocol.TypeDeviceUpdate); err != nil {
			a.Log.Debug("device_update gonderilemedi", "hata", err)
		}
	}
	pctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	err := c.Ping(pctx)
	cancel()
	if err != nil && ctx.Err() == nil {
		a.Log.Info("ag degisti ve baglanti yanit vermiyor, yenileniyor", "hata", err)
		a.onNetworkEvent()
	}
}

// keepalive, istemci tarafinda WS ping gonderir; yanit gelmezse baglanti olu
// sayilir (sunucu pingini 90 sn beklemekten cok daha hizli).
func (a *Agent) keepalive(ctx context.Context, conn interface {
	Ping(context.Context) error
	CloseNow() error
}) {
	t := time.NewTicker(keepaliveEvery)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			pctx, cancel := context.WithTimeout(ctx, keepaliveWait)
			err := conn.Ping(pctx)
			cancel()
			if err != nil {
				if ctx.Err() == nil {
					a.Log.Warn("keepalive yanitsiz, baglanti kapatiliyor", "hata", err)
					conn.CloseNow()
				}
				return
			}
		}
	}
}

// --- reload -------------------------------------------------------------------

// loadReloadConfig, "reload" icin ayar dosyasini okur.
func (a *Agent) loadReloadConfig() (config.Config, error) {
	switch {
	case a.ConfigPath != "":
		return config.LoadFile(a.ConfigPath)
	case a.IsService:
		return config.LoadFile(config.SystemPath())
	}
	return config.Load()
}

// Reload (localctl.Reloader), ajanin ayar dosyasini yeniden okur ve uygular:
//   - terminal / ekran izin tavani canli degisir (kapanan izne ait oturumlar kapanir);
//   - sunucu adresi veya token degistiyse yeni degerler alinir ve yeniden baglanilir;
//   - deploy tavani canli degismez (degisim varsa "deploy_restart" bildirilir).
func (a *Agent) Reload(ctx context.Context) (localctl.ReloadInfo, error) {
	cfg, err := a.loadReloadConfig()
	if err != nil {
		return localctl.ReloadInfo{}, fmt.Errorf("ayar dosyasi okunamadi: %w", err)
	}
	return a.applyReload(cfg)
}

func (a *Agent) applyReload(cfg config.Config) (localctl.ReloadInfo, error) {
	if a.settings == nil {
		return localctl.ReloadInfo{}, errors.New("ajan henuz hazir degil")
	}
	info := localctl.ReloadInfo{Changed: []string{}}
	before := a.settings.snapshot()
	if before.TerminalCap != cfg.EnableTerminal || before.ScreenCap != cfg.EnableScreen {
		t, s := cfg.EnableTerminal, cfg.EnableScreen
		a.SetCaps(&t, &s)
		if before.TerminalCap != t {
			info.Changed = append(info.Changed, "terminal")
		}
		if before.ScreenCap != s {
			info.Changed = append(info.Changed, "screen")
		}
	}
	if (a.apps != nil) != cfg.EnableDeploy {
		info.Changed = append(info.Changed, "deploy_restart")
	}
	a.cfgMu.Lock()
	reconnect := false
	if cfg.ServerAddr != "" && cfg.ServerAddr != a.ServerAddr {
		a.ServerAddr = cfg.ServerAddr
		info.Changed = append(info.Changed, "server")
		reconnect = true
	}
	if cfg.Token != "" && cfg.Token != a.Token {
		a.Token = cfg.Token
		info.Changed = append(info.Changed, "token")
		reconnect = true
	}
	a.cfgMu.Unlock()
	if reconnect {
		info.Reconnecting = true
		a.onNetworkEvent()
	}
	return info, nil
}
