package tunnel

import (
	"context"

	"github.com/coder/websocket"
	"github.com/tkodcumpeg4/zorven/server/netctl"
	"github.com/tkodcumpeg4/zorven/shared/protocol"
)

// Zorven Net agent tarafi: net_* kontrol mesajlari ve FrameWG, netctl'ye
// devredilir. Sunucu sifreli paketleri YALNIZ iletir.

// negotiateNet, istemci hem wg_net hem wg_relay ilan ettiyse ikisini de
// etkinlestirir. Eski istemci/sunucu: bos doner, wg baslatilmaz.
func (s *Session) negotiateNet(mgr *netctl.Manager, features []string) []string {
	if mgr == nil || s.TenantID == "" {
		return nil
	}
	var net, relay, punch, tun, alog bool
	for _, f := range features {
		net = net || f == protocol.FeatureWGNet
		relay = relay || f == protocol.FeatureWGRelay
		punch = punch || f == protocol.FeatureWGPunch
		tun = tun || f == protocol.FeatureWGTun
		alog = alog || f == protocol.FeatureWGALog
	}
	if !net || !relay {
		return nil
	}
	s.netMgr = mgr
	out := []string{protocol.FeatureWGNet, protocol.FeatureWGRelay}
	if punch && mgr.Punch {
		s.netPunch = true
		out = append(out, protocol.FeatureWGPunch)
	}
	if tun && mgr.Tun {
		s.netTun = true
		out = append(out, protocol.FeatureWGTun)
		if alog && mgr.ALog {
			s.netALog = true
			out = append(out, protocol.FeatureWGALog)
		}
	}
	return out
}

func (s *Session) handleNet(ctx context.Context, t protocol.MessageType, data []byte) error {
	if s.netMgr == nil {
		s.log.Warn("net mesaji pazarlanmadan geldi", "tip", t)
		return nil
	}
	s.netMu.Lock()
	nc := s.netConn
	if nc == nil && t == protocol.TypeNetRegister {
		ob := netctl.NewOutbox()
		nc = s.netMgr.Attach(netctl.Identity{
			TenantID: s.TenantID, Kind: netctl.KindDevice, ClientID: s.ClientID, ClientName: s.ClientName,
			Punch: s.netPunch, Tun: s.netTun, ALog: s.netALog,
		}, ob)
		s.netConn = nc
		go func() {
			err := ob.Pump(ctx, func(ctx context.Context, bin bool, b []byte) error {
				return s.writeRaw(ctx, bin, b)
			})
			if err != nil || ctx.Err() == nil {
				// Kontrol kuyrugu tasti veya yazma basarisiz: istemci yeniden baglanip tam netmap alir.
				s.conn.Close(websocket.StatusTryAgainLater, "net queue")
			}
		}()
	}
	s.netMu.Unlock()
	if nc == nil {
		return nil
	}
	return nc.HandleControl(ctx, t, data)
}

func (s *Session) routeNetFrame(f protocol.BodyFrame) {
	s.netMu.Lock()
	nc := s.netConn
	s.netMu.Unlock()
	if nc != nil {
		nc.Relay(f.ReqID, f.Payload)
	}
}

func (s *Session) closeNet() {
	s.netMu.Lock()
	nc := s.netConn
	s.netMu.Unlock()
	if nc != nil {
		nc.Close()
	}
}

// writeRaw, ham bir WS mesaji yazar (net pompasi). Yazma kilidini diger
// gonderimlerle paylasir.
func (s *Session) writeRaw(ctx context.Context, binary bool, b []byte) error {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	ctx, cancel, err := writeCtx(ctx)
	if err != nil {
		return err
	}
	defer cancel()
	typ := websocket.MessageText
	if binary {
		typ = websocket.MessageBinary
	}
	return s.conn.Write(ctx, typ, b)
}
