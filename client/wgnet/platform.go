package wgnet

import (
	"context"
	"errors"
	"net/netip"
	"runtime"
	"time"

	"golang.zx2c4.com/wireguard/tun"
)

// OSNet, Mod B'nin isletim sistemine ozgu kismi: TUN aygitini olusturma, IP/rota
// ayari ve split-DNS kaydi. Linux (netlink + resolvectl), Windows (wintun +
// winipcfg + NRPT), macOS (utun + ifconfig/route + /etc/resolver). Diger her sey
// (anahtar, peer, bind, yol secimi, paket filtresi) platformdan bagimsiz ortak
// koddur.
type OSNet interface {
	// Name: "linux" | "windows" | "darwin" (teshis).
	Name() string
	// CreateTUN, sanal arayuzu olusturur. Yetki yoksa kullaniciya yonelik acik hata doner.
	CreateTUN(name string, mtu int) (tun.Device, error)
	// Configure, arayuze ip/32 adresini ve havuz rotasini verir; undo geri alir.
	Configure(dev tun.Device, ip netip.Addr, pool netip.Prefix, mtu int) (undo func(), err error)
	// SetupDNS, zone (zorven.internal) icin split-DNS kaydini yapar. Basarisizlik
	// OLUMCUL DEGILDIR: sonuc DNSSetup'ta raporlanir.
	SetupDNS(dev tun.Device, resolver netip.Addr, zone string) DNSSetup
	// CleanupStale, onceki bir calismadan (cokme) kalan yetim kayitlari (NRPT
	// kurali, /etc/resolver dosyasi) temizler.
	CleanupStale()
}

// DNSSetup, OS cozucu entegrasyonunun sonucu.
type DNSSetup struct {
	OK     bool
	Method string // systemd-resolved | nrpt | resolver-file | ""
	Note   string // basarisizsa neden / ipucu
	Undo   func()
}

// ErrTUNUnsupported, bu platformda Mod B yok.
var ErrTUNUnsupported = errors.New("tam ag katmani (Mod B) bu isletim sisteminde desteklenmiyor")

// DefaultTUNName, arayuz adi.
const DefaultTUNName = "zorven0"

// runCmd, kisa omurlu bir sistem komutu calistirir (zaman asimli).
func runCmd(name string, args ...string) (string, error) {
	return runCmdCtx(5*time.Second, name, args...)
}

func runCmdCtx(d time.Duration, name string, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), d)
	defer cancel()
	return execOutput(ctx, name, args...)
}

// PlatformName, derleme hedefinin Mod B uyumlulugu (durum ciktisi).
func PlatformName() string { return runtime.GOOS }
