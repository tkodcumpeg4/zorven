package tunnel

import (
	"net/netip"
	"strings"

	"github.com/tkodcumpeg4/zorven/shared/protocol"
)

const (
	maxDeviceIPs      = 16
	maxDeviceHostname = 253
)

// sanitizeDeviceUpdate, istemciden gelen device_update'i sinirlar: gecersiz IP'leri
// atar, sayiyi ve ad uzunlugunu kisitlar, kontrol karakterlerini temizler.
func sanitizeDeviceUpdate(m protocol.DeviceUpdate) protocol.DeviceUpdate {
	out := protocol.DeviceUpdate{Type: m.Type}
	h := strings.TrimSpace(m.Hostname)
	if len(h) <= maxDeviceHostname {
		clean := true
		for i := 0; i < len(h); i++ {
			if h[i] < 0x20 || h[i] == 0x7f {
				clean = false
				break
			}
		}
		if clean {
			out.Hostname = h
		}
	}
	for _, s := range m.IPs {
		if len(out.IPs) >= maxDeviceIPs {
			break
		}
		if _, err := netip.ParseAddr(strings.TrimSpace(s)); err == nil {
			out.IPs = append(out.IPs, strings.TrimSpace(s))
		}
	}
	return out
}
