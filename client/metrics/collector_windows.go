//go:build windows

package metrics

import (
	"math"
	"os"
	"sync"
	"syscall"
	"unsafe"

	"github.com/tkodcumpeg4/zorven/shared/protocol"
)

var (
	modkernel32            = syscall.NewLazyDLL("kernel32.dll")
	procGetSystemTimes     = modkernel32.NewProc("GetSystemTimes")
	procGlobalMemoryStatus = modkernel32.NewProc("GlobalMemoryStatusEx")
	procGetDiskFreeSpace   = modkernel32.NewProc("GetDiskFreeSpaceExW")
)

type memoryStatusEx struct {
	cbSize               uint32
	memoryLoad           uint32
	totalPhys            uint64
	availPhys            uint64
	totalPageFile        uint64
	availPageFile        uint64
	totalVirtual         uint64
	availVirtual         uint64
	availExtendedVirtual uint64
}

type windowsCollector struct {
	mu         sync.Mutex
	lastIdle   uint64
	lastKernel uint64
	lastUser   uint64
	hasPrev    bool
}

func newOSCollector() Collector {
	c := &windowsCollector{}
	c.sampleCPU() // ilk referans ornegini al
	return c
}

func fileTimeToUint64(ft syscall.Filetime) uint64 {
	return (uint64(ft.HighDateTime) << 32) | uint64(ft.LowDateTime)
}

func (c *windowsCollector) sampleCPU() float64 {
	c.mu.Lock()
	defer c.mu.Unlock()

	var idle, kernel, user syscall.Filetime
	r, _, _ := procGetSystemTimes.Call(
		uintptr(unsafe.Pointer(&idle)),
		uintptr(unsafe.Pointer(&kernel)),
		uintptr(unsafe.Pointer(&user)),
	)
	if r == 0 {
		return 0
	}

	curIdle := fileTimeToUint64(idle)
	curKernel := fileTimeToUint64(kernel)
	curUser := fileTimeToUint64(user)

	if !c.hasPrev {
		c.lastIdle = curIdle
		c.lastKernel = curKernel
		c.lastUser = curUser
		c.hasPrev = true
		return 0
	}

	dIdle := curIdle - c.lastIdle
	dKernel := curKernel - c.lastKernel
	dUser := curUser - c.lastUser

	c.lastIdle = curIdle
	c.lastKernel = curKernel
	c.lastUser = curUser

	// Windows GetSystemTimes: kernel suresi idle suresini de kapsar.
	totalSys := dKernel + dUser
	if totalSys <= 0 || totalSys < dIdle {
		return 0
	}

	cpu := (float64(totalSys-dIdle) / float64(totalSys)) * 100.0
	if cpu < 0 {
		cpu = 0
	} else if cpu > 100 {
		cpu = 100
	}
	return math.Round(cpu*10) / 10
}

func (c *windowsCollector) sampleMemory() (float64, uint64, uint64) {
	var ms memoryStatusEx
	ms.cbSize = uint32(unsafe.Sizeof(ms))
	r, _, _ := procGlobalMemoryStatus.Call(uintptr(unsafe.Pointer(&ms)))
	if r == 0 {
		return 0, 0, 0
	}

	totalMB := ms.totalPhys / (1024 * 1024)
	availMB := ms.availPhys / (1024 * 1024)
	usedMB := totalMB - availMB
	memPercent := float64(ms.memoryLoad)
	return memPercent, usedMB, totalMB
}

func (c *windowsCollector) sampleDisk() float64 {
	drive := os.Getenv("SystemDrive")
	if drive == "" {
		drive = "C:"
	}
	drive += `\`

	pDrive, err := syscall.UTF16PtrFromString(drive)
	if err != nil {
		return 0
	}

	var freeBytes, totalBytes, totalFreeBytes uint64
	r, _, _ := procGetDiskFreeSpace.Call(
		uintptr(unsafe.Pointer(pDrive)),
		uintptr(unsafe.Pointer(&freeBytes)),
		uintptr(unsafe.Pointer(&totalBytes)),
		uintptr(unsafe.Pointer(&totalFreeBytes)),
	)
	if r == 0 || totalBytes == 0 {
		return 0
	}

	usedBytes := totalBytes - totalFreeBytes
	pct := (float64(usedBytes) / float64(totalBytes)) * 100.0
	return math.Round(pct*10) / 10
}

func (c *windowsCollector) Collect() *protocol.Metrics {
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
