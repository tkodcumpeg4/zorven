package ingress

import (
	"net/http"
	"testing"
	"time"
)

func TestIPListClientTimeouts(t *testing.T) {
	c := newIPListClient()
	tr, ok := c.Transport.(*http.Transport)
	if !ok || c.Timeout != ipListFetchTimeout || tr.TLSHandshakeTimeout != 10*time.Second ||
		tr.ResponseHeaderTimeout != 30*time.Second || tr.DialContext == nil || tr.Proxy == nil {
		t.Fatalf("istemci zaman asimlari eksik: %+v", c)
	}
}
