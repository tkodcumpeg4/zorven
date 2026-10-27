//go:build darwin

package wgnet

import (
	"errors"
	"fmt"
	"net/netip"
	"os"
	"strings"
	"syscall"

	"golang.zx2c4.com/wireguard/tun"
)

// macOS Mod B: utun (kernel control soketi) -- ROOT gerekir. Zorven bir root
// LaunchDaemon olarak calisir (`sudo zorven service install`; plist
// /Library/LaunchDaemons/zorven.plist). Adres/rota ifconfig + route ile, split-DNS
// /etc/resolver/zorven.internal dosyasiyla (yerlesik bolgesel cozum) yapilir.
// NetworkExtension yolu bu surumde YOK (Apple Developer hesabi + entitlement).
//
// NOT: bu dosya macOS makinesinde calistirilarak DOGRULANMADI (capraz derleme ile
// derlenir; test edilmedi).

type darwinOS struct{}

// DefaultOS, derleme hedefinin OSNet uygulamasi.
func DefaultOS() OSNet { return darwinOS{} }

func (darwinOS) Name() string { return "darwin" }

const resolverDir = "/etc/resolver"

func (darwinOS) CreateTUN(name string, mtu int) (tun.Device, error) {
	// macOS arayuz adini cekirdek verir ("utunN"); "utun" = sonraki bos.
	d, err := tun.CreateTUN("utun", mtu)
	if err != nil {
		if errors.Is(err, syscall.EPERM) || errors.Is(err, syscall.EACCES) || os.IsPermission(err) {
			return nil, fmt.Errorf("utun olusturulamadi: root yetkisi gerekli. " +
				"Zorven'i LaunchDaemon olarak kurun: sudo zorven service install && sudo zorven net up")
		}
		return nil, fmt.Errorf("utun olusturulamadi: %w", err)
	}
	return d, nil
}

func (darwinOS) Configure(dev tun.Device, ip netip.Addr, pool netip.Prefix, mtu int) (func(), error) {
	name, err := dev.Name()
	if err != nil {
		return nil, err
	}
	if out, err := runCmd("/sbin/ifconfig", name, "inet", ip.String(), ip.String(), "netmask", "255.255.255.255", "mtu", fmt.Sprint(mtu), "up"); err != nil {
		return nil, fmt.Errorf("ifconfig basarisiz: %s", firstLine(out, err))
	}
	// Havuz rotasi: arayuze bagli (-interface).
	if out, err := runCmd("/sbin/route", "-q", "-n", "add", "-inet", pool.Masked().String(), "-interface", name); err != nil {
		return nil, fmt.Errorf("route basarisiz: %s", firstLine(out, err))
	}
	return func() {
		_, _ = runCmd("/sbin/route", "-q", "-n", "delete", "-inet", pool.Masked().String(), "-interface", name)
	}, nil
}

func (darwinOS) SetupDNS(dev tun.Device, resolver netip.Addr, zone string) DNSSetup {
	if err := os.MkdirAll(resolverDir, 0o755); err != nil {
		return DNSSetup{Note: "/etc/resolver olusturulamadi: " + err.Error()}
	}
	path := resolverDir + "/" + zone
	body := "# zorven (otomatik; zorven net down ile silinir)\nnameserver " + resolver.String() + "\n"
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		return DNSSetup{Note: "split-DNS dosyasi yazilamadi: " + err.Error()}
	}
	return DNSSetup{OK: true, Method: "resolver-file", Undo: func() { _ = os.Remove(path) }}
}

// CleanupStale: onceki calismadan kalan zorven isaretli resolver dosyasini siler.
func (darwinOS) CleanupStale() {
	path := resolverDir + "/zorven.internal"
	if b, err := os.ReadFile(path); err == nil && strings.HasPrefix(string(b), "# zorven") {
		_ = os.Remove(path)
	}
}
