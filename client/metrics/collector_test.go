package metrics

import (
	"testing"
	"time"
)

func TestCollect(t *testing.T) {
	c := Default()
	if c == nil {
		t.Fatal("Default() nil dondu")
	}

	// Ilk ornek CPU baseline alir
	m1 := c.Collect()
	if m1 == nil {
		t.Fatal("m1 nil dondu")
	}

	// Kisa bir bekleme sonrasi CPU delta ornegi
	time.Sleep(50 * time.Millisecond)
	m2 := c.Collect()
	if m2 == nil {
		t.Fatal("m2 nil dondu")
	}

	t.Logf("Metrikler: CPU=%.1f%%, RAM=%.1f%% (%d/%d MB), Disk=%.1f%%",
		m2.CPUPercent, m2.MemoryPercent, m2.MemoryUsedMB, m2.MemoryTotalMB, m2.DiskPercent)

	if m2.MemoryPercent < 0 || m2.MemoryPercent > 100 {
		t.Errorf("Gecersiz bellek yuzdesi: %.2f", m2.MemoryPercent)
	}
	if m2.CPUPercent < 0 || m2.CPUPercent > 100 {
		t.Errorf("Gecersiz CPU yuzdesi: %.2f", m2.CPUPercent)
	}
	if m2.DiskPercent < 0 || m2.DiskPercent > 100 {
		t.Errorf("Gecersiz Disk yuzdesi: %.2f", m2.DiskPercent)
	}
}
