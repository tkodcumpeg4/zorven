//go:build !linux

package obs

import (
	"bytes"
	"fmt"
	"time"
)

var processStart = time.Now()

// writeProcess: Linux disinda yalniz baslangic zamani (sunucu prod'da Linux'ta calisir).
func writeProcess(b *bytes.Buffer) {
	head(b, "process_start_time_seconds", "Surecin baslangic zamani (unix sn).", "gauge")
	fmt.Fprintf(b, "process_start_time_seconds %s\n", fmtF(float64(processStart.UnixNano())/1e9))
}
