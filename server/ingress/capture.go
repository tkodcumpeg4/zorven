package ingress

import (
	crand "crypto/rand"
	"encoding/hex"
	"io"
)

// FAZ 2 — İstek inspector yardimcilari: istek/yanit govdesini TAVANA kadar
// yakalayan sarmalayicilar. Tavan asilirsa truncated isaretlenir; akis bozulmaz.

// newCaptureID, bir yakalama/log kimligi uretir (log ile eslesir).
func newCaptureID() string {
	b := make([]byte, 8)
	if _, err := crand.Read(b); err != nil {
		return "req_unknown"
	}
	return "req_" + hex.EncodeToString(b)
}

// captureReadCloser, r'den okunani dst'ye (tavana kadar) kopyalar. Okuma
// semantigi degismez; yalnizca ilk max bayt saklanir.
func captureReadCloser(rc io.ReadCloser, dst *[]byte, truncated *bool, max int) io.ReadCloser {
	return &capturingReadCloser{rc: rc, dst: dst, truncated: truncated, max: max}
}

type capturingReadCloser struct {
	rc        io.ReadCloser
	dst       *[]byte
	truncated *bool
	max       int
}

func (c *capturingReadCloser) Read(p []byte) (int, error) {
	n, err := c.rc.Read(p)
	if n > 0 {
		captureInto(c.dst, c.truncated, p[:n], c.max)
	}
	return n, err
}

func (c *capturingReadCloser) Close() error { return c.rc.Close() }

// captureInto, src'yi dst'ye tavana kadar ekler; sigmayan olursa truncated=true.
func captureInto(dst *[]byte, truncated *bool, src []byte, max int) {
	remaining := max - len(*dst)
	if remaining <= 0 {
		*truncated = true
		return
	}
	if len(src) > remaining {
		*dst = append(*dst, src[:remaining]...)
		*truncated = true
		return
	}
	*dst = append(*dst, src...)
}

// captureWriter, w'ye yazilani dst'ye (tavana kadar) kopyalar (yanit govdesi).
type captureWriter struct {
	w         io.Writer
	dst       *[]byte
	truncated *bool
	max       int
}

func (cw *captureWriter) Write(p []byte) (int, error) {
	n, err := cw.w.Write(p)
	if n > 0 {
		captureInto(cw.dst, cw.truncated, p[:n], cw.max)
	}
	return n, err
}
