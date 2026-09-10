//go:build !windows && !linux

package metrics

import (
	"runtime"

	"github.com/tkodcumpeg4/zorven/shared/protocol"
)

type fallbackCollector struct{}

func newOSCollector() Collector {
	return &fallbackCollector{}
}

func (c *fallbackCollector) Collect() *protocol.Metrics {
	var ms runtime.MemStats
	runtime.ReadMemStats(&ms)

	allocMB := ms.Alloc / (1024 * 1024)
	sysMB := ms.Sys / (1024 * 1024)

	return &protocol.Metrics{
		CPUPercent:    0,
		MemoryPercent: 0,
		MemoryUsedMB:  allocMB,
		MemoryTotalMB: sysMB,
		DiskPercent:   0,
	}
}
