//go:build linux

package metrics

import (
	"bufio"
	"math"
	"os"
	"strconv"
	"strings"
	"sync"
	"syscall"

	"github.com/tkodcumpeg4/zorven/shared/protocol"
)

type linuxCollector struct {
	mu          sync.Mutex
	lastIdle    uint64
	lastNonIdle uint64
	hasPrev     bool
}

func newOSCollector() Collector {
	c := &linuxCollector{}
	c.sampleCPU()
	return c
}

func (c *linuxCollector) sampleCPU() float64 {
	c.mu.Lock()
	defer c.mu.Unlock()

	f, err := os.Open("/proc/stat")
	if err != nil {
		return 0
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	if !scanner.Scan() {
		return 0
	}
	fields := strings.Fields(scanner.Text())
	if len(fields) < 5 || fields[0] != "cpu" {
		return 0
	}

	var vals [10]uint64
	for i := 1; i < len(fields) && i <= 10; i++ {
		vals[i-1], _ = strconv.ParseUint(fields[i], 10, 64)
	}

	// vals: 0:user, 1:nice, 2:system, 3:idle, 4:iowait, 5:irq, 6:softirq, 7:steal
	idle := vals[3] + vals[4]
	nonIdle := vals[0] + vals[1] + vals[2] + vals[5] + vals[6] + vals[7]

	if !c.hasPrev {
		c.lastIdle = idle
		c.lastNonIdle = nonIdle
		c.hasPrev = true
		return 0
	}

	prevTotal := c.lastIdle + c.lastNonIdle
	curTotal := idle + nonIdle

	totalDiff := curTotal - prevTotal
	idleDiff := idle - c.lastIdle

	c.lastIdle = idle
	c.lastNonIdle = nonIdle

	if totalDiff <= 0 {
		return 0
	}

	cpu := (float64(totalDiff-idleDiff) / float64(totalDiff)) * 100.0
	if cpu < 0 {
		cpu = 0
	} else if cpu > 100 {
		cpu = 100
	}
	return math.Round(cpu*10) / 10
}

func (c *linuxCollector) sampleMemory() (float64, uint64, uint64) {
	f, err := os.Open("/proc/meminfo")
	if err != nil {
		return 0, 0, 0
	}
	defer f.Close()

	var memTotalKB, memAvailKB uint64
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := scanner.Text()
		if strings.HasPrefix(line, "MemTotal:") {
			fields := strings.Fields(line)
			if len(fields) >= 2 {
				memTotalKB, _ = strconv.ParseUint(fields[1], 10, 64)
			}
		} else if strings.HasPrefix(line, "MemAvailable:") {
			fields := strings.Fields(line)
			if len(fields) >= 2 {
				memAvailKB, _ = strconv.ParseUint(fields[1], 10, 64)
			}
		}
		if memTotalKB > 0 && memAvailKB > 0 {
			break
		}
	}

	if memTotalKB == 0 {
		return 0, 0, 0
	}

	totalMB := memTotalKB / 1024
	usedMB := (memTotalKB - memAvailKB) / 1024
	pct := (float64(usedMB) / float64(totalMB)) * 100.0
	return math.Round(pct*10) / 10, usedMB, totalMB
}

func (c *linuxCollector) sampleDisk() float64 {
	var stat syscall.Statfs_t
	if err := syscall.Statfs("/", &stat); err != nil {
		return 0
	}
	total := stat.Blocks * uint64(stat.Bsize)
	free := stat.Bavail * uint64(stat.Bsize)
	if total == 0 {
		return 0
	}
	used := total - free
	pct := (float64(used) / float64(total)) * 100.0
	return math.Round(pct*10) / 10
}

func (c *linuxCollector) Collect() *protocol.Metrics {
	cpu := c.sampleCPU()
	memPct, memUsed, memTotal := c.sampleMemory()
	diskPct := c.sampleDisk()

	return &protocol.Metrics{
		CPUPercent:    cpu,
		MemoryPercent: memPct,
		MemoryUsedMB:  memUsed,
		MemoryTotalMB: memTotal,
		DiskPercent:   diskPct,
	}
}
