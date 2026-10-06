package api

import (
	"testing"
	"time"

	"github.com/tkodcumpeg4/zorven/server/store"
	"github.com/tkodcumpeg4/zorven/shared/protocol"
)

// Canli oturum yokken son BILINEN surum gosterilir; bos kalmaz.
func TestDeviceOutFallsBackToLastKnownVersion(t *testing.T) {
	d := deviceOut(store.Client{AgentVersion: "0.1.0"})
	if d.Version != "0.1.0" {
		t.Errorf("Version = %q, son bilinen surum beklenirdi", d.Version)
	}
}

// Canli surum varsa EZILMEZ: o an baglı olanın gerçeği daha değerlidir.
func TestDeviceOutKeepsLiveVersion(t *testing.T) {
	d := deviceOut(store.Client{Version: "0.2.0", AgentVersion: "0.1.0"})
	if d.Version != "0.2.0" {
		t.Errorf("Version = %q, canli surum korunmaliydi", d.Version)
	}
}

// Kalici cihaz alanlari cevaba tasinmali.
func TestDeviceOutCarriesDeviceFields(t *testing.T) {
	seen := time.Date(2026, 9, 24, 10, 0, 0, 0, time.UTC)
	d := deviceOut(store.Client{
		ID: "cli_1", Name: "ofis-pc",
		Hostname: "OFIS-PC", OS: "windows", Arch: "amd64",
		IPs:         []string{"192.168.1.24"},
		LastSeenAt:  &seen,
		LastMetrics: &protocol.Metrics{CPUPercent: 12.5},
	})
	if d.Hostname != "OFIS-PC" || d.OS != "windows" || d.Arch != "amd64" {
		t.Errorf("cihaz alanlari kayboldu: %+v", d)
	}
	if len(d.IPs) != 1 || d.IPs[0] != "192.168.1.24" {
		t.Errorf("IP listesi = %v", d.IPs)
	}
	if d.LastMetrics == nil || d.LastMetrics.CPUPercent != 12.5 {
		t.Errorf("son metrikler kayboldu: %+v", d.LastMetrics)
	}
	if d.LastSeenAt == nil || !d.LastSeenAt.Equal(seen) {
		t.Errorf("son gorulme = %v", d.LastSeenAt)
	}
	// Listede tunel doldurulmaz (N+1 kacinmasi).
	if d.Tunnels != nil {
		t.Errorf("liste goruntusunde tunel dolduruldu: %v", d.Tunnels)
	}
}
