//go:build !linux && !darwin && !windows

package wgnet

import (
	"net/netip"

	"golang.zx2c4.com/wireguard/tun"
)

// Desteklenmeyen platformlar (FreeBSD vb.): Mod B kapali; Mod A etkilenmez.

type unsupportedOS struct{}

// DefaultOS, derleme hedefinin OSNet uygulamasi.
func DefaultOS() OSNet { return unsupportedOS{} }

func (unsupportedOS) Name() string { return "unsupported" }
func (unsupportedOS) CreateTUN(string, int) (tun.Device, error) {
	return nil, ErrTUNUnsupported
}
func (unsupportedOS) Configure(tun.Device, netip.Addr, netip.Prefix, int) (func(), error) {
	return nil, ErrTUNUnsupported
}
func (unsupportedOS) SetupDNS(tun.Device, netip.Addr, string) DNSSetup {
	return DNSSetup{Note: ErrTUNUnsupported.Error()}
}
func (unsupportedOS) CleanupStale() {}
