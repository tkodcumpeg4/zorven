package main

import (
	"context"
	"log/slog"
	"net"
	"strconv"
	"strings"

	"github.com/tkodcumpeg4/zorven/server/netctl"
)

// setupNetDirect, Zorven Net W2 (dogrudan P2P / NAT delme) yapilandirmasi:
//
//	ZORVEN_NET_DIRECT=off      dogrudan yolu tamamen kapatir (yalniz relay)
//	ZORVEN_STUN_ADDR=:3478     STUN UDP dinleyici adresi (varsayilan :3478; "off" = kapali)
//	ZORVEN_STUN_PUBLIC=h:p,... istemcilerin sorgulayacagi STUN adresleri
//	                           (bos: istemci sunucu ana makinesi + STUN portu)
//
// STUN portu acilamazsa (dolu/izin yok) uyari verilir; dogrudan yol yine de
// yerel/ortak ag adaylariyla denenir, genel STUN yoksa NAT arkasi eşler relay'de kalir.
func setupNetDirect(ctx context.Context, mgr *netctl.Manager, getenv func(string) string, log *slog.Logger) {
	// W3: ZORVEN_NET_TUN=off Mod B'yi (OS TUN, wg_tun) sunucuda kapatir.
	if strings.EqualFold(strings.TrimSpace(getenv("ZORVEN_NET_TUN")), "off") {
		mgr.Tun = false
		log.Info("zorven net Mod B (TUN) kapali (ZORVEN_NET_TUN=off)")
	}
	if strings.EqualFold(strings.TrimSpace(getenv("ZORVEN_NET_DIRECT")), "off") {
		log.Info("zorven net dogrudan yol kapali (ZORVEN_NET_DIRECT=off); yalniz relay")
		return
	}
	mgr.Punch = true
	addr := strings.TrimSpace(getenv("ZORVEN_STUN_ADDR"))
	if addr == "" {
		addr = ":3478"
	}
	if strings.EqualFold(addr, "off") {
		log.Info("STUN kapali (ZORVEN_STUN_ADDR=off)")
		return
	}
	stunSrv := netctl.NewStunServer(log)
	pc, err := stunSrv.Listen(addr)
	if err != nil {
		log.Warn("STUN portu acilamadi; NAT delme icin genel adres yansimasi yok (yalniz relay/yerel adaylar)",
			"addr", addr, "hata", err)
		return
	}
	if _, p, err := net.SplitHostPort(pc.LocalAddr().String()); err == nil {
		mgr.StunPort, _ = strconv.Atoi(p)
	}
	for _, h := range strings.Split(getenv("ZORVEN_STUN_PUBLIC"), ",") {
		if h = strings.TrimSpace(h); h != "" {
			mgr.Stun = append(mgr.Stun, h)
		}
	}
	go func() {
		if err := stunSrv.Serve(ctx, pc); err != nil {
			log.Warn("STUN dinleyicisi durdu", "hata", err)
		}
	}()
}
