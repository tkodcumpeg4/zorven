package tunnel

import (
	"strings"
	"testing"

	"github.com/tkodcumpeg4/zorven/shared/protocol"
)

func TestSanitizeDeviceUpdate(t *testing.T) {
	var many []string
	for i := 0; i < 40; i++ {
		many = append(many, "10.0.0.1")
	}
	got := sanitizeDeviceUpdate(protocol.DeviceUpdate{
		Hostname: " pc-1 ",
		IPs:      append([]string{"192.168.1.5", "bozuk", "<script>"}, many...),
	})
	if got.Hostname != "pc-1" {
		t.Fatalf("hostname = %q", got.Hostname)
	}
	if len(got.IPs) != maxDeviceIPs || got.IPs[0] != "192.168.1.5" || got.IPs[1] != "10.0.0.1" {
		t.Fatalf("ips = %v", got.IPs)
	}
	if bad := sanitizeDeviceUpdate(protocol.DeviceUpdate{Hostname: "a\nb"}); bad.Hostname != "" {
		t.Fatal("kontrol karakterli ad reddedilmeli")
	}
	if long := sanitizeDeviceUpdate(protocol.DeviceUpdate{Hostname: strings.Repeat("a", 300)}); long.Hostname != "" {
		t.Fatal("cok uzun ad reddedilmeli")
	}
}
