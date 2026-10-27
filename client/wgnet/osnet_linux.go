//go:build linux

package wgnet

import (
	"errors"
	"fmt"
	"net"
	"net/netip"
	"os"
	"os/exec"
	"strings"
	"syscall"

	"github.com/vishvananda/netlink"
	"golang.zx2c4.com/wireguard/tun"
)

// Linux Mod B: /dev/net/tun (wireguard-go tun.CreateTUN), netlink ile adres/rota
// (`ip` komutu KULLANILMAZ), split-DNS icin systemd-resolved (resolvectl).
//
// Yetki: CAP_NET_ADMIN (veya root). Servis olarak calisiyorsa systemd unit'i
// AmbientCapabilities=CAP_NET_ADMIN tasir (bkz. client/service_opts_linux.go).

type linuxOS struct{}

// DefaultOS, derleme hedefinin OSNet uygulamasi.
func DefaultOS() OSNet { return linuxOS{} }

func (linuxOS) Name() string { return "linux" }

func (linuxOS) CreateTUN(name string, mtu int) (tun.Device, error) {
	d, err := tun.CreateTUN(name, mtu)
	if err != nil {
		switch {
		case errors.Is(err, syscall.EPERM), errors.Is(err, syscall.EACCES), os.IsPermission(err):
			return nil, fmt.Errorf("TUN arayuzu olusturulamadi: yetki yok (CAP_NET_ADMIN gerekli). " +
				"Zorven'i servis olarak kurun ve komutu root ile calistirin: sudo zorven service install && sudo zorven net up")
		case errors.Is(err, os.ErrNotExist):
			return nil, fmt.Errorf("TUN arayuzu olusturulamadi: /dev/net/tun yok (konteynerde: --device /dev/net/tun --cap-add NET_ADMIN)")
		}
		return nil, fmt.Errorf("TUN arayuzu olusturulamadi: %w", err)
	}
	return d, nil
}

func (linuxOS) Configure(dev tun.Device, ip netip.Addr, pool netip.Prefix, mtu int) (func(), error) {
	name, err := dev.Name()
	if err != nil {
		return nil, err
	}
	link, err := netlink.LinkByName(name)
	if err != nil {
		return nil, fmt.Errorf("arayuz bulunamadi (%s): %w", name, err)
	}
	addr := &netlink.Addr{IPNet: &net.IPNet{IP: net.IP(ip.AsSlice()), Mask: net.CIDRMask(32, 32)}}
	if err := netlink.AddrAdd(link, addr); err != nil {
		return nil, fmt.Errorf("adres atanamadi (%s): %w", ip, err)
	}
	if mtu > 0 {
		_ = netlink.LinkSetMTU(link, mtu)
	}
	if err := netlink.LinkSetUp(link); err != nil {
		return nil, fmt.Errorf("arayuz acilamadi: %w", err)
	}
	route := &netlink.Route{
		LinkIndex: link.Attrs().Index,
		Dst:       &net.IPNet{IP: net.IP(pool.Masked().Addr().AsSlice()), Mask: net.CIDRMask(pool.Bits(), 32)},
		Scope:     netlink.SCOPE_LINK,
		Src:       net.IP(ip.AsSlice()),
	}
	if err := netlink.RouteReplace(route); err != nil {
		return nil, fmt.Errorf("rota eklenemedi (%s): %w", pool, err)
	}
	// Arayuz (TUN fd kapaninca) kendiliginden kalkar; rota/adres onunla gider.
	return func() {}, nil
}

func (linuxOS) SetupDNS(dev tun.Device, resolver netip.Addr, zone string) DNSSetup {
	name, err := dev.Name()
	if err != nil {
		return DNSSetup{Note: err.Error()}
	}
	if _, err := exec.LookPath("resolvectl"); err != nil {
		return DNSSetup{Note: "systemd-resolved (resolvectl) yok: split-DNS kaydedilmedi. " +
			"Adlari kullanmak icin /etc/hosts'a elle ekleyin veya sunucuyu IP ile kullanin"}
	}
	steps := [][]string{
		{"dns", name, resolver.String()},
		{"domain", name, "~" + zone},
		{"default-route", name, "no"},
	}
	for _, s := range steps {
		if out, err := runCmd("resolvectl", s...); err != nil {
			_, _ = runCmd("resolvectl", "revert", name)
			return DNSSetup{Note: fmt.Sprintf("systemd-resolved ayari basarisiz (resolvectl %s): %s",
				strings.Join(s, " "), firstLine(out, err))}
		}
	}
	return DNSSetup{OK: true, Method: "systemd-resolved", Undo: func() { _, _ = runCmd("resolvectl", "revert", name) }}
}

func (linuxOS) CleanupStale() {}
