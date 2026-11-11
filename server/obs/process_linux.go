//go:build linux

package obs

import (
	"bytes"
	"fmt"
	"os"
	"strconv"
	"strings"
	"syscall"
	"time"
)

// Surec metrikleri (Prometheus "process_*" adlariyla uyumlu; client_golang'in
// process collector'u eklenmeden /proc'tan okunur). Neden: konteyner bellek
// tavanina (mem_limit) yaklasma ve fd tukenmesi (K2: EMFILE) alarmlari icin RSS
// ve acik fd sayisi gerekiyordu; go_memstats RSS'i gostermez.

var processStart = time.Now()

func writeProcess(b *bytes.Buffer) {
	var ru syscall.Rusage
	if syscall.Getrusage(syscall.RUSAGE_SELF, &ru) == nil {
		cpu := float64(ru.Utime.Sec+ru.Stime.Sec) + float64(ru.Utime.Usec+ru.Stime.Usec)/1e6
		head(b, "process_cpu_seconds_total", "Kullanici + cekirdek CPU suresi.", "counter")
		fmt.Fprintf(b, "process_cpu_seconds_total %s\n", fmtF(cpu))
	}
	if rss, ok := readRSS(); ok {
		head(b, "process_resident_memory_bytes", "Surecin yerlesik bellegi (RSS).", "gauge")
		fmt.Fprintf(b, "process_resident_memory_bytes %d\n", rss)
	}
	if ents, err := os.ReadDir("/proc/self/fd"); err == nil {
		head(b, "process_open_fds", "Acik dosya tanimlayici sayisi.", "gauge")
		fmt.Fprintf(b, "process_open_fds %d\n", len(ents))
	}
	var rl syscall.Rlimit
	if syscall.Getrlimit(syscall.RLIMIT_NOFILE, &rl) == nil {
		head(b, "process_max_fds", "Dosya tanimlayici yumusak siniri.", "gauge")
		fmt.Fprintf(b, "process_max_fds %d\n", rl.Cur)
	}
	head(b, "process_start_time_seconds", "Surecin baslangic zamani (unix sn).", "gauge")
	fmt.Fprintf(b, "process_start_time_seconds %s\n", fmtF(float64(processStart.UnixNano())/1e9))
}

// readRSS, /proc/self/statm'in ikinci alani (yerlesik sayfa) x sayfa boyutu.
func readRSS() (int64, bool) {
	raw, err := os.ReadFile("/proc/self/statm")
	if err != nil {
		return 0, false
	}
	f := strings.Fields(string(raw))
	if len(f) < 2 {
		return 0, false
	}
	pages, err := strconv.ParseInt(f[1], 10, 64)
	if err != nil {
		return 0, false
	}
	return pages * int64(os.Getpagesize()), true
}
