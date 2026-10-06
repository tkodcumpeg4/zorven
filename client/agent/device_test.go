package agent

import (
	"net"
	"testing"
)

// localIPs loopback ve link-local adresleri ELEMELI: her makinede ayni
// olduklari icin cihazi ayirt etmezler.
func TestLocalIPsExcludesLoopbackAndLinkLocal(t *testing.T) {
	for _, ip := range localIPs() {
		parsed := net.ParseIP(ip)
		if parsed == nil {
			t.Errorf("gecersiz adres dondu: %q", ip)
			continue
		}
		if parsed.IsLoopback() {
			t.Errorf("loopback adres elenmedi: %s", ip)
		}
		if parsed.IsLinkLocalUnicast() || parsed.IsLinkLocalMulticast() {
			t.Errorf("link-local adres elenmedi: %s", ip)
		}
	}
}

// Sonuc sirali olmali: panelde her yenilemede sira degismesin.
func TestLocalIPsSorted(t *testing.T) {
	got := localIPs()
	for i := 1; i < len(got); i++ {
		if got[i-1] > got[i] {
			t.Fatalf("siralanmamis: %v", got)
		}
	}
}

// Hostname okunamazsa bos doner; uydurma ad URETILMEZ.
func TestDeviceHostnameNeverPanics(t *testing.T) {
	_ = deviceHostname()
}
