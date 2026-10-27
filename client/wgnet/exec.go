package wgnet

import (
	"context"
	"os/exec"
	"strings"
)

// execOutput, komutu calistirir; birlesik cikti ve hata doner. Argumanlar kabuga
// VERILMEZ (enjeksiyon yok); calistirilan komutlar sabittir.
func execOutput(ctx context.Context, name string, args ...string) (string, error) {
	out, err := exec.CommandContext(ctx, name, args...).CombinedOutput()
	return strings.TrimSpace(string(out)), err
}

// firstLine, komut ciktisinin ilk satirini (yoksa hata metnini) verir.
func firstLine(out string, err error) string {
	if out != "" {
		if i := strings.IndexByte(out, '\n'); i > 0 {
			out = out[:i]
		}
		return out
	}
	return err.Error()
}
